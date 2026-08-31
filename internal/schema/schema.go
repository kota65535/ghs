// Package schema describes the shape of settings.yml: which keys it accepts,
// what may go under each of them, and which API operation writes what.
//
// The description in nodes_gen.go is generated from the official GitHub
// OpenAPI description (github/rest-api-description) by `go generate ./gen`.
// Nothing in this package is hand-maintained except the patches in extra.go.
//
// The file is a tree that follows the API's own paths. Its root is the
// repository -- `PATCH /repos/{owner}/{repo}` -- so the fields of that request
// are written at the top level, and everything reached through a longer path
// goes under a key named after it:
//
//	has_issues: true              # a field of the repository itself
//	actions:                      # /repos/{owner}/{repo}/actions
//	  permissions:                # .../actions/permissions
//	    enabled: true
//	    workflow:                 # .../actions/permissions/workflow
//	      default_workflow_permissions: read
package schema

import "sort"

// Field describes a single writable field.
type Field struct {
	// Type is the JSON type declared by the OpenAPI description
	// ("string", "boolean", "integer", "number", "object", "array").
	// It is empty when the description declares no type.
	Type string

	// Enum lists the accepted values. Nil when the field is not an enum.
	//
	// An explicit null is not checked against it: null means "clear this
	// field" and is passed through to the API untouched.
	Enum []string

	// Description is what the API description says the field is for, as it
	// says it: Markdown, several sentences of it where the API has that much
	// to say. It is empty where the description says nothing.
	//
	// Nothing is validated against it. It is here so that `ghs init` can write
	// each field with its own documentation above it, which is the difference
	// between a generated file you read and one you look up field by field.
	Description string

	// Fields holds the nested field definitions of an object field. It is nil
	// for scalars and for objects whose properties the description leaves
	// unspecified (a free-form object), in which case the nested content is
	// passed through without validation.
	Fields map[string]Field
}

// Kind is the shape a node takes in settings.yml.
type Kind string

const (
	// KindObject is a single API object, declared as a mapping of fields.
	KindObject Kind = "object"

	// KindCollection is a set of named elements, declared as a sequence of
	// them. Its fields describe one element rather than the whole set.
	KindCollection Kind = "collection"

	// KindNamespace is a key that stands for a path segment and holds no
	// fields of its own. GitHub has nothing at /repos/{owner}/{repo}/actions,
	// but the settings below it are reached through that segment.
	KindNamespace Kind = "namespace"
)

// Node is one key of settings.yml, and everything that may be written under
// it.
type Node struct {
	// Kind is how the node is written.
	Kind Kind

	// Segment is both the key this node is written under and what it adds to
	// its parent's API path. Those are the same thing: a key names the path
	// segment it stands for, which is what makes the endpoint that changes a
	// setting tell you where to write it.
	//
	// It is empty for the root, whose path is the repository itself and whose
	// fields are the top level of the file.
	//
	// A path is built by walking down from the root, which is how the element
	// names of a collection get into it: the variables of an environment are
	// at environments/{name}/variables, and the {name} comes from the element
	// the walk is passing through.
	Segment string

	// Method writes this node's fields. It is empty for a namespace, which has
	// no operation of its own.
	Method string

	// Summary is the one-line title the API description gives that operation,
	// such as "Set GitHub Actions permissions for a repository". It is what the
	// key this node is written under stands for, and `ghs init` writes it above
	// the key. Empty for a namespace, and for an operation the description
	// gives no title.
	Summary string

	// Conditional marks a node whose API path exists only in some
	// repositories: the allowed actions are there only while the policy is to
	// select them, and some settings are for private repositories alone.
	//
	// Where such a path does not apply, the current state simply lacks those
	// fields, and a declaration of one reads as a change against nothing.
	Conditional bool

	// Fields are the writable fields of this node, taken from the request body
	// of its operation. For a collection they describe one element.
	Fields map[string]Field

	// Nodes are the keys that may appear under this one, named after the path
	// segment each stands for.
	Nodes map[string]Node
}

// IsCollection reports whether the node is a set of named elements.
func (n Node) IsCollection() bool { return n.Kind == KindCollection }

// IsNamespace reports whether the node only groups the nodes beneath it.
func (n Node) IsNamespace() bool { return n.Kind == KindNamespace }

// Writable reports whether the node has an operation that writes its fields.
func (n Node) Writable() bool { return n.Method != "" }

// Field returns the definition of one of the node's own fields.
func (n Node) Field(name string) (Field, bool) {
	f, ok := n.Fields[name]
	return f, ok
}

// Child returns the node written under name.
func (n Node) Child(name string) (Node, bool) {
	child, ok := n.Nodes[name]
	return child, ok
}

// ChildNames returns the keys that may appear under this node, sorted.
func (n Node) ChildNames() []string {
	names := make([]string, 0, len(n.Nodes))
	for name := range n.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Root is the repository, which is what a settings file describes.
func Root() Node { return root }
