// Package resource reads and writes the settings a node of settings.yml
// stands for.
//
// Most nodes need nothing said about them: the schema records the path and the
// method, and Object or Collection does the rest. What is written here by hand
// is the handful of places where GitHub does not follow its own pattern -- a
// ruleset addressed by a server-issued id, an environment whose reported shape
// differs from the one that declares it.
package resource

import (
	"context"
	"fmt"
	"io"

	"github.com/kota65535/ghs/internal/schema"
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

// Path is where a node sits in the API, built by walking down from the
// repository and picking up the element names on the way.
type Path struct {
	repo     Repo
	segments []string
}

// At returns the path of the repository itself.
func At(repo Repo) Path { return Path{repo: repo} }

// Child returns the path of a node below this one.
func (p Path) Child(segment string) Path {
	if segment == "" {
		return p
	}
	return Path{repo: p.repo, segments: append(append([]string(nil), p.segments...), segment)}
}

// Element returns the path of one element of a collection.
func (p Path) Element(name string) Path {
	return Path{repo: p.repo, segments: append(append([]string(nil), p.segments...), escape(name))}
}

// String is the path as the API takes it.
func (p Path) String() string {
	path := fmt.Sprintf("repos/%s/%s", p.repo.Owner, p.repo.Name)
	for _, segment := range p.segments {
		path += "/" + segment
	}
	return path
}

// Object reads and writes the fields of one node directly.
type Object interface {
	// Fetch returns the current state as the API reports it.
	Fetch(ctx context.Context, c Client, node schema.Node, path Path) (map[string]any, error)

	// Apply sends the declared fields. The whole declaration is sent rather
	// than only the differing fields: the underlying requests are idempotent,
	// so there is nothing to gain from the more complicated option.
	Apply(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error
}

// Collection reads and writes a set of named elements.
//
// Where an Object has one thing to write, a collection has elements that come
// and go, so it needs create and delete alongside update.
type Collection interface {
	// FetchAll returns the current elements keyed by name.
	//
	// Elements are shaped like the body that creates one, so that comparing
	// them with the declared elements is the same field-by-field comparison a
	// plain node gets. Read-only fields the API reports are kept: Update and
	// Delete may need them to address the element.
	FetchAll(ctx context.Context, c Client, node schema.Node, path Path) (map[string]map[string]any, error)

	// Create adds an element that GitHub does not have.
	Create(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error

	// Update sends the declared element in full.
	//
	// It receives the current element as well because a collection may address
	// its elements by something GitHub issued rather than by name.
	Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error

	// Delete removes an element the settings file no longer declares. It takes
	// the current element for the same reason Update does.
	Delete(ctx context.Context, c Client, node schema.Node, path Path, current map[string]any) error

	// ElementPath returns the path of one element, which is what the nodes
	// below it hang off.
	ElementPath(path Path, element map[string]any) (Path, error)
}

// objects and collections hold the nodes that need something other than the
// general behaviour, keyed by the path of keys leading to them in the settings
// file.
var (
	objects = map[string]Object{}

	collections = map[string]Collection{
		"rulesets":     Rulesets{},
		"environments": Environments{},
	}
)

// ObjectFor returns how to read and write the fields of a node.
func ObjectFor(key string) Object {
	if special, ok := objects[key]; ok {
		return special
	}
	return GenericObject{}
}

// CollectionFor returns how to read and write the elements of a collection.
func CollectionFor(key string) Collection {
	if special, ok := collections[key]; ok {
		return special
	}
	return GenericCollection{}
}
