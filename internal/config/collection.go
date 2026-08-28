package config

import (
	"fmt"

	"github.com/kota65535/ghs/internal/diff"
	"github.com/kota65535/ghs/internal/schema"
)

// nameField identifies a collection element. Every element carries it, whether
// the create request sends it in the body or in the path.
const nameField = diff.NameField

// parseCollection validates a declared collection and returns its elements
// keyed by name, along with one message per problem.
//
// A nil result means the declaration was unusable; an empty non-nil one is a
// set declared with no members, which is a valid thing to ask for.
func parseCollection(name string, raw any, resource schema.Resource, opts Options) (map[string]map[string]any, []string) {
	declared, problems := elementsOf(name, raw, resource.Fields, opts)
	if declared == nil {
		return nil, problems
	}

	elements := make(map[string]map[string]any, len(declared))
	for elementName, element := range declared {
		normalized, err := diff.NormalizeMap(element)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", diff.ElementPath(name, elementName), err))
			continue
		}
		elements[elementName] = normalized
	}

	return elements, problems
}

// elementsOf validates a declared sequence of named elements and returns them
// keyed by name.
//
// It is used for a collection written as a top-level key and for one written
// as a field of another collection's element: the two are declared the same
// way, and only differ in how the API is asked to change them.
func elementsOf(path string, raw any, fields map[string]schema.Field, opts Options) (map[string]map[string]any, []string) {
	if raw == nil {
		// A single-object resource reads null as "clear this field", but here
		// the same half-written line would declare that every element be
		// deleted. Since there is an explicit way to say that, null is left
		// without a meaning.
		return nil, []string{fmt.Sprintf("%s: expected a sequence of elements (write [] to declare an empty set)", path)}
	}

	declared, ok := raw.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("%s: expected a sequence of elements", path)}
	}

	elements := make(map[string]map[string]any, len(declared))
	var problems []string

	for i, rawElement := range declared {
		// Until the name is known an element can only be located by position.
		elementPath := fmt.Sprintf("%s[%d]", path, i)

		element, ok := rawElement.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expected a mapping of fields", elementPath))
			continue
		}

		name, ok := element[nameField].(string)
		if !ok || name == "" {
			problems = append(problems, fmt.Sprintf("%s: %s is required and must be a non-empty string", elementPath, nameField))
			continue
		}

		elementPath = diff.ElementPath(path, name)
		if _, seen := elements[name]; seen {
			problems = append(problems, fmt.Sprintf("%s: declared twice", elementPath))
			continue
		}

		problems = append(problems, validateFields(elementPath, element, fields, opts)...)
		elements[name] = element
	}

	return elements, problems
}
