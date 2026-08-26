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

// Version is the only settings.yml format version this build understands.
const Version = 1

// DefaultPath is where ghs looks for the settings file.
const DefaultPath = ".github/settings.yml"

// Config is a validated settings file.
type Config struct {
	// Declared holds, per resource name, the fields declared for it. Values
	// are normalized so they can be compared with API responses directly.
	//
	// A field that is absent here is not managed: ghs never touches it.
	Declared map[string]map[string]any
}

// Options adjusts how a settings file is read. It carries nothing today and
// exists so that adding an option later does not change every call site.
type Options struct{}

// ResourceNames returns the declared resource names in sorted order.
func (c Config) ResourceNames() []string {
	names := make([]string, 0, len(c.Declared))
	for name := range c.Declared {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// file mirrors the settings file layout. Resources are kept as raw YAML nodes
// so that no field is coerced into a Go type before validation.
type file struct {
	Version   *int                      `yaml:"version"`
	Resources map[string]map[string]any `yaml:",inline"`
}

// Parse validates settings file contents.
func Parse(b []byte, opts Options) (*Config, error) {
	var f file
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	if f.Version == nil {
		return nil, fmt.Errorf("version is required (expected %d)", Version)
	}
	if *f.Version != Version {
		return nil, fmt.Errorf("unsupported version %d (this build understands %d)", *f.Version, Version)
	}
	if len(f.Resources) == 0 {
		return nil, errors.New("no resources declared")
	}

	cfg := &Config{Declared: make(map[string]map[string]any, len(f.Resources))}
	var problems []string

	for _, name := range sortedKeys(f.Resources) {
		resource, ok := schema.Lookup(name)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: unknown resource (this build manages: %s)",
				name, strings.Join(schema.Names(), ", ")))
			continue
		}

		declared := f.Resources[name]
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
