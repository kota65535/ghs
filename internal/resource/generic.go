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

// Normalize implements Object. GitHub stores what it is sent, so there is
// nothing to put into another form.
func (GenericObject) Normalize(node schema.Node, desired map[string]any) map[string]any {
	return desired
}

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
	return byName(elements, node.KeyField(), node.Segment)
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

// byName keys elements by the field they are identified by, failing on an
// element that lacks it: without it there is nothing to match the element
// against.
func byName(elements []map[string]any, key, what string) (map[string]map[string]any, error) {
	out := make(map[string]map[string]any, len(elements))
	for _, element := range elements {
		name, ok := element[key].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("%s: the API reported an element with no %s", what, key)
		}
		out[name] = element
	}
	return out, nil
}

// idField is what GitHub issues for an element of some collections and what
// addresses it afterwards. It is not declared in the settings file, so it is
// only ever read from the current state.
const idField = "id"

// pathByID addresses an element by the id GitHub issued for it, reporting
// false where the element carries none. A collection matched on a name GitHub
// does not address by needs it: a ruleset, an autolink.
//
// The id arrives as a JSON number, so it is a float64 here; rendering it with
// %v would spell a large one in exponent notation and produce a path the API
// does not recognize.
func pathByID(path Path, element map[string]any) (Path, bool) {
	switch id := element[idField].(type) {
	case float64:
		return path.Element(fmt.Sprintf("%d", int64(id))), true
	case int64:
		return path.Element(fmt.Sprintf("%d", id)), true
	case int:
		return path.Element(fmt.Sprintf("%d", id)), true
	case string:
		return path.Element(id), true
	default:
		return Path{}, false
	}
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

// enabledField is what a node standing for an endpoint that is only an on and
// an off declares. The name is ghs's own: such an endpoint takes no request
// body, so there is no field in the API description to borrow it from.
const enabledField = "enabled"

// applyToggle sends the request that stands for the declared value. An
// endpoint whose whole shape is an on and an off turns the setting on with PUT
// and off with DELETE, and neither request carries a body, so the declared
// value is not sent anywhere: it chooses the method.
func applyToggle(ctx context.Context, c Client, path Path, desired map[string]any) error {
	enabled, ok := desired[enabledField].(bool)
	if !ok {
		// There are two requests and nothing else to send -- a null, which
		// elsewhere means "clear this field", has no request to be sent as.
		return fmt.Errorf("%s: %s must be true or false, got %v", path, enabledField, desired[enabledField])
	}

	method := http.MethodPut
	if !enabled {
		method = http.MethodDelete
	}
	if err := c.DoWithContext(ctx, method, path.String(), nil, nil); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

// isNotFound reports an error that is the API saying there is nothing here,
// rather than one that means the request failed.
func isNotFound(err error) bool {
	var httpErr *api.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

// doesNotApply reports an error that means the setting has no place in this
// repository, rather than one that means the request failed.
//
// GitHub answers 409 where the allowed actions are not being selected, and 422
// where a setting is for private repositories only. Both say the same thing:
// there is nothing here to read.
//
// 403 is not among them. A node being out of reach is not the same as its
// having nothing to report, and reading the two alike would have a token short
// of a permission report settings as absent. Where a 403 does mean absence --
// autolinks, which GitHub Free does not have -- it is read that way by the
// resource that knows it, not here.
func doesNotApply(err error) bool {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusConflict || httpErr.StatusCode == http.StatusUnprocessableEntity
}

// isForbidden reports the 403 GitHub answers where a feature is not part of
// the repository's plan.
func isForbidden(err error) bool {
	var httpErr *api.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusForbidden
}

// escape makes a name safe to put in a path. Environment names allow
// characters that mean something in a URL.
func escape(name string) string { return url.PathEscape(name) }
