package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Collection reads and writes a set of named elements, such as the repository
// variables or its rulesets.
//
// Where a Resource has one object to patch, a collection has elements that
// come and go, so it needs create and delete alongside update.
type Collection interface {
	// Name is the top-level key in settings.yml.
	Name() string

	// FetchAll returns the current elements keyed by name.
	//
	// Elements are shaped like the body that creates one, so that comparing
	// them with the declared elements is the same field-by-field comparison a
	// single-object resource gets. Read-only fields the API reports are kept:
	// Update and Delete may need them to address the element.
	FetchAll(ctx context.Context, c Client, repo Repo) (map[string]map[string]any, error)

	// Create adds an element that GitHub does not have.
	Create(ctx context.Context, c Client, repo Repo, desired map[string]any) error

	// Update sends the declared element in full, as Resource.Apply does.
	//
	// It receives the current element as well because a collection may address
	// its elements by something GitHub issued rather than by name.
	Update(ctx context.Context, c Client, repo Repo, current, desired map[string]any) error

	// Delete removes an element the settings file no longer declares. It takes
	// the current element for the same reason Update does.
	Delete(ctx context.Context, c Client, repo Repo, current map[string]any) error
}

// elementName is the key an element is matched by. Every element carries it,
// whether the API takes it in the request body or in the path.
const elementName = "name"

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
func eachPage(perPage int, fetch func(page int) (count int, err error)) error {
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

// send performs a request with a JSON body, discarding the response: what the
// settings now are is a question for the next plan, which asks the API rather
// than trusting this reply.
func send(ctx context.Context, c Client, method, path string, body map[string]any) error {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	// A nil *bytes.Reader in an io.Reader interface is not a nil interface, so
	// the two cases are kept apart deliberately.
	if reader == nil {
		return c.DoWithContext(ctx, method, path, nil, nil)
	}
	return c.DoWithContext(ctx, method, path, reader, nil)
}

// deleteAt removes the resource at path.
func deleteAt(ctx context.Context, c Client, path string) error {
	return c.DoWithContext(ctx, http.MethodDelete, path, nil, nil)
}
