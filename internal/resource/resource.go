// Package resource maps the top-level keys of settings.yml onto the GitHub
// REST API operations that read and write them.
package resource

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Client is the subset of go-gh's REST client that ghs uses.
//
// Keeping it to one method makes the API traffic easy to stand in for during
// tests, which matters because the alternative is exercising these calls
// against real repository settings.
type Client interface {
	DoWithContext(ctx context.Context, method, path string, body io.Reader, response any) error
}

// Repo identifies the repository being managed.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Resource reads and writes one API resource.
//
// Only single-object resources fit this interface. Collections keyed by name,
// such as rulesets or variables, need create, update and delete, and are
// served by Collection instead.
type Resource interface {
	// Name is the top-level key in settings.yml.
	Name() string

	// Fetch returns the current state as the API reports it.
	Fetch(ctx context.Context, c Client, repo Repo) (map[string]any, error)

	// Apply sends the declared fields. The whole declaration is sent rather
	// than only the differing fields: the underlying request is idempotent, so
	// there is nothing to gain from the more complicated option.
	Apply(ctx context.Context, c Client, repo Repo, desired map[string]any) error
}

var registry = map[string]Resource{
	"repository": Repository{},
	"actions":    Actions{},
}

var collections = map[string]Collection{
	"variables":    Variables{},
	"rulesets":     Rulesets{},
	"environments": Environments{},
}

// Lookup returns the single-object resource registered under name.
func Lookup(name string) (Resource, error) {
	r, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown resource %q (this build manages: %s)", name, joinNames())
	}
	return r, nil
}

// LookupCollection returns the collection registered under name.
func LookupCollection(name string) (Collection, error) {
	c, ok := collections[name]
	if !ok {
		return nil, fmt.Errorf("unknown collection %q (this build manages: %s)", name, joinNames())
	}
	return c, nil
}

func joinNames() string {
	names := make([]string, 0, len(registry)+len(collections))
	for name := range registry {
		names = append(names, name)
	}
	for name := range collections {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
