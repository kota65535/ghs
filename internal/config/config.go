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

// Declaration is what was declared at one node of the settings file.
//
// The file is a tree that follows the API's paths, and so is this: Fields holds
// the node's own settings, and Children what was declared under each key
// beneath it. Load returns the root, which is the repository.
type Declaration struct {
	// Node is what the schema says may be written here.
	Node schema.Node

	// Fields holds this node's own declared fields, normalized so they compare
	// with API responses directly. A field absent here is not managed.
	//
	// It is nil for a collection, whose declaration is Elements.
	Fields map[string]any

	// Elements holds a collection's declared elements, keyed by name.
	//
	// Declaring the key declares the whole set: an element the API reports and
	// this map does not hold is deleted. An empty non-nil map asks for every
	// existing element to go, which is not what the key being absent means.
	Elements map[string]map[string]any

	// Children holds what was declared under the keys beneath this node.
	// Elements of a collection share it: an element's own children are held by
	// ElementChildren.
	Children map[string]*Declaration

	// ElementChildren holds, per element name, what was declared under the
	// keys beneath that element.
	ElementChildren map[string]map[string]*Declaration
}

// ChildNames returns the declared child keys in sorted order.
func (d *Declaration) ChildNames() []string {
	names := make([]string, 0, len(d.Children))
	for name := range d.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Child returns what was declared under name.
func (d *Declaration) Child(name string) (*Declaration, bool) {
	child, ok := d.Children[name]
	return child, ok
}

// ElementChild returns what was declared under name within one element.
func (d *Declaration) ElementChild(element, name string) (*Declaration, bool) {
	child, ok := d.ElementChildren[element][name]
	return child, ok
}

// ElementChildNames returns the declared child keys of one element, sorted.
func (d *Declaration) ElementChildNames(element string) []string {
	names := make([]string, 0, len(d.ElementChildren[element]))
	for name := range d.ElementChildren[element] {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Load reads and validates the settings file at path.
func Load(path string) (*Declaration, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse validates settings file contents.
//
// The top level of the file is the repository itself, so its fields are the
// fields of PATCH /repos/{owner}/{repo}, and every other key names a path
// reached from there.
func Parse(b []byte) (*Declaration, error) {
	// Decoded as raw YAML so that no field is coerced into a Go type before
	// validation.
	var declared map[string]any

	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&declared); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(declared) == 0 {
		return nil, errors.New("nothing declared")
	}

	root, problems := parseNode("", schema.Root(), declared)
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return root, nil
}

// parseNode validates what was declared at one node, and everything below it.
func parseNode(path string, node schema.Node, declared any) (*Declaration, []string) {
	if node.IsCollection() {
		return parseCollectionNode(path, node, declared)
	}

	fields, ok := declared.(map[string]any)
	if !ok {
		return nil, []string{fmt.Sprintf("%s: expected a mapping of fields", or(path, "the settings file"))}
	}
	if len(fields) == 0 {
		return nil, []string{fmt.Sprintf("%s: nothing declared", or(path, "the settings file"))}
	}

	// The keys naming nodes are taken out first, so that what remains is this
	// node's own fields and an unknown key is reported as such.
	own, children, problems := split(path, node, fields)

	// A namespace stands for a path segment and has no operation of its own,
	// so a field written directly under it has nowhere to go.
	if node.IsNamespace() {
		for _, name := range sortedKeys(own) {
			problems = append(problems, fmt.Sprintf("%s.%s: %s holds no settings of its own", path, name, path))
		}
		own = nil
	}

	problems = append(problems, validateFields(path, own, node.Fields)...)

	normalized, err := diff.NormalizeMap(own)
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", or(path, "the settings file"), err))
	}

	return &Declaration{Node: node, Fields: normalized, Children: children}, problems
}

// split separates the keys that name nodes from the fields of the node itself.
func split(path string, node schema.Node, declared map[string]any) (map[string]any, map[string]*Declaration, []string) {
	own := map[string]any{}
	children := map[string]*Declaration{}
	var problems []string

	for _, name := range sortedKeys(declared) {
		child, isNode := node.Child(name)
		if !isNode {
			own[name] = declared[name]
			continue
		}

		parsed, childProblems := parseNode(join(path, name), child, declared[name])
		problems = append(problems, childProblems...)
		if parsed != nil {
			children[name] = parsed
		}
	}

	return own, children, problems
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func or(path, fallback string) string {
	if path == "" {
		return fallback
	}
	return path
}

// validateFields checks declared fields against the description and returns
// one message per problem, so a single run reports everything wrong with the
// file rather than only the first mistake.
func validateFields(path string, declared map[string]any, fields map[string]schema.Field) []string {
	var problems []string

	for _, name := range sortedKeys(declared) {
		value := declared[name]
		fieldPath := join(path, name)

		field, known := fields[name]
		if !known {
			problems = append(problems, fmt.Sprintf("%s: not a writable field here", fieldPath))
			continue
		}

		problems = append(problems, validateValue(fieldPath, value, field)...)
	}
	return problems
}

func validateValue(path string, value any, field schema.Field) []string {
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
		problems = append(problems, validateFields(path, nested, field.Fields)...)
	}

	if items, ok := value.([]any); ok && len(field.Variants) > 0 {
		problems = append(problems, validateElements(path, items, field)...)
	}

	return problems
}

// validateElements checks the elements of an array against what the
// description says an element may be, which is a question the element answers
// for itself: a ruleset rule declares a type, and which parameters it accepts
// follows from that.
//
// A type the description does not have is rejected like any other value that is
// not one of the accepted ones. GitHub adds rule types between releases, so this
// is a rule type ghs does not know yet rather than one that does not exist, and
// the answer is the same either way: the pinned API description is followed
// daily, so what ghs does not know it does not know for long.
func validateElements(path string, items []any, field schema.Field) []string {
	var problems []string

	for i, item := range items {
		elementPath := fmt.Sprintf("%s[%d]", path, i)

		element, ok := item.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expected a mapping", elementPath))
			continue
		}

		variant, known := field.Variant(element)
		if !known {
			problems = append(problems, fmt.Sprintf("%s.type: %s is not one of %s",
				elementPath, formatScalar(element["type"]), strings.Join(sortedKeys(field.Variants), ", ")))
			continue
		}
		problems = append(problems, validateFields(elementPath, element, variant.Fields)...)
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
