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
	if raw == nil {
		// A single-object resource reads null as "clear this field", but here
		// the same half-written line would declare that every element be
		// deleted. Since there is an explicit way to say that, null is left
		// without a meaning.
		return nil, []string{fmt.Sprintf("%s: expected a sequence of elements (write [] to declare an empty set)", name)}
	}

	declared, ok := raw.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("%s: expected a sequence of elements", name)}
	}

	elements := make(map[string]map[string]any, len(declared))
	var problems []string

	for i, rawElement := range declared {
		// Until the name is known an element can only be located by position.
		path := fmt.Sprintf("%s[%d]", name, i)

		element, ok := rawElement.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expected a mapping of fields", path))
			continue
		}

		elementName, ok := element[nameField].(string)
		if !ok || elementName == "" {
			problems = append(problems, fmt.Sprintf("%s: %s is required and must be a non-empty string", path, nameField))
			continue
		}

		path = diff.ElementPath(name, elementName)
		if _, seen := elements[elementName]; seen {
			problems = append(problems, fmt.Sprintf("%s: declared twice", path))
			continue
		}

		problems = append(problems, validateFields(path, element, resource.Fields, opts)...)

		normalized, err := diff.NormalizeMap(element)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		elements[elementName] = normalized
	}

	return elements, problems
}
