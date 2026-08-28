// Package config loads and validates settings.yml.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kota65535/ghs/internal/diff"
	"github.com/kota65535/ghs/internal/schema"
)

// DefaultPath is where ghs looks for the settings file.
const DefaultPath = ".github/settings.yml"

// Config is a validated settings file.
type Config struct {
	// Declared holds, per single-object resource, the fields declared for it.
	// Values are normalized so they can be compared with API responses
	// directly.
	//
	// A field that is absent here is not managed: ghs never touches it.
	Declared map[string]map[string]any

	// Collections holds, per collection resource, its declared elements keyed
	// by name.
	//
	// Where a field is the unit of management for the resources above, an
	// element is the unit here, and declaring the resource declares the whole
	// set: an entry present with no elements asks for every existing one to be
	// deleted, which is not what the resource being absent means.
	Collections map[string]map[string]map[string]any
}

// Options adjusts how a settings file is read. It carries nothing today and
// exists so that adding an option later does not change every call site.
type Options struct{}

// ResourceNames returns the declared resource names in sorted order.
func (c Config) ResourceNames() []string {
	names := make([]string, 0, len(c.Declared)+len(c.Collections))
	for name := range c.Declared {
		names = append(names, name)
	}
	for name := range c.Collections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsCollection reports whether the named resource was declared as a set of
// elements rather than as a single object.
func (c Config) IsCollection(name string) bool {
	_, ok := c.Collections[name]
	return ok
}

// Load reads and validates the settings file at path.
func Load(path string, opts Options) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := Parse(b, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse validates settings file contents.
func Parse(b []byte, opts Options) (*Config, error) {
	// Resources are decoded as raw YAML so that no field is coerced into a Go
	// type before validation.
	var resources map[string]any

	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&resources); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(resources) == 0 {
		return nil, errors.New("no resources declared")
	}

	cfg := &Config{
		Declared:    make(map[string]map[string]any, len(resources)),
		Collections: make(map[string]map[string]map[string]any, len(resources)),
	}
	var problems []string

	for _, name := range sortedKeys(resources) {
		resource, ok := schema.Lookup(name)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: unknown resource (this build manages: %s)",
				name, strings.Join(schema.Names(), ", ")))
			continue
		}

		if resource.IsCollection() {
			elements, elementProblems := parseCollection(name, resources[name], resource, opts)
			problems = append(problems, elementProblems...)
			if elements != nil {
				cfg.Collections[name] = elements
			}
			continue
		}

		declared, ok := resources[name].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expected a mapping of fields", name))
			continue
		}
		if len(declared) == 0 {
			problems = append(problems, fmt.Sprintf("%s: no fields declared", name))
			continue
		}

		problems = append(problems, validateFields(name, declared, resource.Fields, opts)...)

		normalized, err := diff.NormalizeMap(declared)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		cfg.Declared[name] = normalized
	}

	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return cfg, nil
}

// validateFields checks declared fields against the generated definitions and
// returns one message per problem, so a single run reports everything wrong
// with the file rather than only the first mistake.
func validateFields(path string, declared map[string]any, fields map[string]schema.Field, opts Options) []string {
	var problems []string

	for _, name := range sortedKeys(declared) {
		value := declared[name]
		fieldPath := path + "." + name

		field, known := fields[name]
		if !known {
			problems = append(problems, fmt.Sprintf("%s: not a writable field of this resource", fieldPath))
			continue
		}

		problems = append(problems, validateValue(fieldPath, value, field, opts)...)
	}
	return problems
}

func validateValue(path string, value any, field schema.Field, opts Options) []string {
	if value == nil {
		// Declaring null means "clear this field", and it is passed through
		// as-is. Whether a given field accepts null is not checked here: the
		// description records that inconsistently across resources, and a
		// wrongly rejected null would leave no way to clear a value at all.
		//
		// What is left unguarded is a line left half-written, since
		// "has_issues:" parses as null. That shows up in the plan as
		// "true => null" before anything is sent, and the API rejects it if
		// it gets that far.
		return nil
	}

	var problems []string

	if len(field.Enum) > 0 {
		s, ok := value.(string)
		if !ok || !contains(field.Enum, s) {
			problems = append(problems, fmt.Sprintf("%s: %v is not one of %s",
				path, formatScalar(value), strings.Join(field.Enum, ", ")))
			return problems
		}
	}

	if mismatch := typeMismatch(field.Type, value); mismatch != "" {
		return append(problems, fmt.Sprintf("%s: %s", path, mismatch))
	}

	// Nested objects are validated to the depth the description defines. An
	// object with no declared properties stays free-form.
	if nested, ok := value.(map[string]any); ok && len(field.Fields) > 0 {
		problems = append(problems, validateFields(path, nested, field.Fields, opts)...)
	}

	return problems
}

// typeMismatch returns a message when value does not match the declared JSON
// type, or an empty string when it does.
func typeMismatch(declaredType string, value any) string {
	if declaredType == "" {
		return ""
	}

	var actual string
	switch v := value.(type) {
	case bool:
		actual = "boolean"
	case string:
		actual = "string"
	case int, int64, float64:
		actual = "number"
		if declaredType == "integer" && isIntegral(v) {
			actual = "integer"
		}
	case map[string]any:
		actual = "object"
	case []any:
		actual = "array"
	default:
		return ""
	}

	switch {
	case actual == declaredType:
		return ""
	case declaredType == "number" && actual == "integer":
		return ""
	default:
		return fmt.Sprintf("expected %s, got %s (%s)", declaredType, actual, formatScalar(value))
	}
}

func isIntegral(v any) bool {
	switch n := v.(type) {
	case int, int64:
		return true
	case float64:
		return n == float64(int64(n))
	default:
		return false
	}
}

func formatScalar(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%v", v)
}

func contains(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
