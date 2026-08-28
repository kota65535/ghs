// Package schema holds the field definitions that describe which fields of a
// GitHub REST API resource ghs is allowed to manage.
//
// The definitions in fields_gen.go are generated from the official GitHub
// OpenAPI description (github/rest-api-description) by `go generate ./gen`.
// Nothing in this package is hand-maintained except the deny lists below.
package schema

import "sort"

// Field describes a single writable field of a resource.
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

	// Fields holds the nested field definitions of an object field. It is nil
	// for scalars and for objects whose properties the description leaves
	// unspecified (a free-form object), in which case the nested content is
	// passed through without validation.
	Fields map[string]Field

	// Elements describes one entry of a collection nested inside this field.
	//
	// A list belonging to a resource is sometimes reached through an API of
	// its own -- the variables of an environment are written one at a time --
	// without thereby ceasing to be a field of that resource. Such a field is
	// declared like any other list and the entries are matched by name, the
	// same as a top-level collection. It is nil for every other field.
	Elements map[string]Field
}

// IsCollection reports whether the field is a collection of named entries
// rather than a plain value.
func (f Field) IsCollection() bool { return f.Elements != nil }

// Kind is the shape a resource takes in settings.yml.
type Kind string

const (
	// KindObject is a single API object, declared as a mapping of fields.
	KindObject Kind = "object"

	// KindCollection is a set of named elements, declared as a sequence of
	// them. Its fields describe one element rather than the whole set.
	KindCollection Kind = "collection"
)

// Resource describes one manageable API resource, such as "repository".
type Resource struct {
	// Name is the top-level key used in settings.yml.
	Name string

	// Kind is how the resource is written in the settings file.
	Kind Kind

	// Fields are the writable fields taken from the API request body schema.
	// For a collection they are the fields of one element, taken from the
	// request body that creates it.
	Fields map[string]Field
}

// IsCollection reports whether the resource is a set of named elements.
func (r Resource) IsCollection() bool { return r.Kind == KindCollection }

// NestedCollections names the fields of this resource that are collections of
// their own, in sorted order.
func (r Resource) NestedCollections() []string {
	var names []string
	for name, field := range r.Fields {
		if field.IsCollection() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Get returns the definition of a top-level field.
func (r Resource) Get(name string) (Field, bool) {
	f, ok := r.Fields[name]
	return f, ok
}

// Lookup returns the resource registered under name.
func Lookup(name string) (Resource, bool) {
	r, ok := resources[name]
	return r, ok
}

// Names returns the registered resource names in sorted order.
func Names() []string {
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
