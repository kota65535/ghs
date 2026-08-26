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
}

// Resource describes one manageable API resource, such as "repository".
type Resource struct {
	// Name is the top-level key used in settings.yml.
	Name string

	// Fields are the writable fields taken from the API request body schema.
	Fields map[string]Field
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
