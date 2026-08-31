package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/kota65535/ghs/internal/schema"
)

// GenericObject reads a node with GET and writes it with the method the schema
// records, which is what most of GitHub's settings endpoints amount to.
type GenericObject struct{}

// Fetch implements Object.
func (GenericObject) Fetch(ctx context.Context, c Client, node schema.Node, path Path) (map[string]any, error) {
	var current map[string]any
	if err := c.DoWithContext(ctx, http.MethodGet, path.String(), nil, &current); err != nil {
		if node.Conditional && doesNotApply(err) {
			// The path is not there to be read, which is not the same as the
			// read failing. A field declared against it shows up as a change
			// with nothing on the other side.
			return nil, nil
		}
		return nil, fmt.Errorf("get %s: %w", path, err)
	}
	return current, nil
}

// Apply implements Object.
func (GenericObject) Apply(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error {
	if err := send(ctx, c, node.Method, path.String(), desired); err != nil {
		return fmt.Errorf("%s %s: %w", node.Method, path, err)
	}
	return nil
}

// GenericCollection reads a collection with GET on the collection itself and
// addresses its elements by name, which is how the variables endpoints work.
type GenericCollection struct{}

// FetchAll implements Collection.
func (GenericCollection) FetchAll(ctx context.Context, c Client, node schema.Node, path Path) (map[string]map[string]any, error) {
	elements, err := listElements(ctx, c, path, node.Segment)
	if err != nil {
		return nil, err
	}
	return byName(elements, node.Segment)
}

// Create implements Collection.
func (GenericCollection) Create(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error {
	if err := send(ctx, c, http.MethodPost, path.String(), desired); err != nil {
		return fmt.Errorf("create %s in %s: %w", desired[elementName], path, err)
	}
	return nil
}

// Update implements Collection.
func (g GenericCollection) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	target, err := g.ElementPath(path, desired)
	if err != nil {
		return err
	}
	if err := send(ctx, c, http.MethodPatch, target.String(), desired); err != nil {
		return fmt.Errorf("update %s: %w", target, err)
	}
	return nil
}

// Delete implements Collection.
func (g GenericCollection) Delete(ctx context.Context, c Client, node schema.Node, path Path, current map[string]any) error {
	target, err := g.ElementPath(path, current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, target.String()); err != nil {
		return fmt.Errorf("delete %s: %w", target, err)
	}
	return nil
}

// ElementPath implements Collection.
func (GenericCollection) ElementPath(path Path, element map[string]any) (Path, error) {
	name, err := nameOf(element)
	if err != nil {
		return Path{}, err
	}
	return path.Element(name), nil
}

// elementName is the key an element is matched by. Every element carries it,
// whether the API takes it in the request body or in the path.
const elementName = schema.NameField

// listElements reads every page of a list endpoint.
//
// GitHub wraps some of these lists in an object keyed by the resource name and
// returns others bare, so both shapes are accepted.
func listElements(ctx context.Context, c Client, path Path, key string) ([]map[string]any, error) {
	var all []map[string]any

	err := eachPage(func(page int) (int, error) {
		var body json.RawMessage
		query := fmt.Sprintf("%s?per_page=%d&page=%d", path, perPage, page)
		if err := c.DoWithContext(ctx, http.MethodGet, query, nil, &body); err != nil {
			return 0, fmt.Errorf("list %s: %w", path, err)
		}

		items, err := unwrapList(body, key)
		if err != nil {
			return 0, fmt.Errorf("list %s: %w", path, err)
		}
		all = append(all, items...)
		return len(items), nil
	})
	if err != nil {
		return nil, err
	}

	return all, nil
}

// unwrapList reads a page of a list, whether the items come back bare or
// wrapped in an object alongside a total.
func unwrapList(body json.RawMessage, key string) ([]map[string]any, error) {
	var bare []map[string]any
	if err := json.Unmarshal(body, &bare); err == nil {
		return bare, nil
	}

	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	raw, ok := wrapped[key]
	if !ok {
		return nil, fmt.Errorf("response has no %q", key)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	return items, nil
}

// perPage is what these endpoints are asked for at a time. The variables
// endpoints accept at most thirty, which is the lowest of the ones ghs reads,
// so that is what all of them get: a page size below the maximum costs an extra
// request now and again, one above it is an error.
const perPage = 30

// maxPages caps how far paging goes. It stands well above any repository's
// worth of variables or rulesets and exists so that an API that never reports
// a last page cannot hold the process forever.
const maxPages = 100

// eachPage calls fetch with successive page numbers until a page comes back
// with fewer items than a full one, which is the last page.
//
// A short page is the signal because the REST client from go-gh reports only
// the decoded body, leaving the Link header that would answer the question
// directly out of reach.
func eachPage(fetch func(page int) (count int, err error)) error {
	for page := 1; page <= maxPages; page++ {
		count, err := fetch(page)
		if err != nil {
			return err
		}
		if count < perPage {
			return nil
		}
	}
	return fmt.Errorf("stopped after %d pages: the API keeps reporting more", maxPages)
}

// byName keys elements by their name field, failing on an element that has
// none: without a name there is nothing to match it against.
func byName(elements []map[string]any, what string) (map[string]map[string]any, error) {
	out := make(map[string]map[string]any, len(elements))
	for _, element := range elements {
		name, ok := element[elementName].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("%s: the API reported an element with no name", what)
		}
		out[name] = element
	}
	return out, nil
}

// nameOf returns the name of an element, which is how most collections address
// one.
func nameOf(element map[string]any) (string, error) {
	name, ok := element[elementName].(string)
	if !ok || name == "" {
		return "", fmt.Errorf("element has no name: %v", element)
	}
	return name, nil
}

// send performs a request with a JSON body, discarding the response: what the
// settings now are is a question for the next plan, which asks the API rather
// than trusting this reply.
func send(ctx context.Context, c Client, method, path string, body map[string]any) error {
	if body == nil {
		return c.DoWithContext(ctx, method, path, nil, nil)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request body: %w", err)
	}
	return c.DoWithContext(ctx, method, path, bytes.NewReader(encoded), nil)
}

// deleteAt removes the resource at path.
func deleteAt(ctx context.Context, c Client, path string) error {
	return c.DoWithContext(ctx, http.MethodDelete, path, nil, nil)
}

// doesNotApply reports an error that means the setting has no place in this
// repository, rather than one that means the request failed.
//
// GitHub answers 409 where the allowed actions are not being selected, and 422
// where a setting is for private repositories only. Both say the same thing:
// there is nothing here to read.
func doesNotApply(err error) bool {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusConflict || httpErr.StatusCode == http.StatusUnprocessableEntity
}

// escape makes a name safe to put in a path. Environment names allow
// characters that mean something in a URL.
func escape(name string) string { return url.PathEscape(name) }
