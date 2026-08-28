package config

import (
	"fmt"

	"github.com/kota65535/ghs/internal/diff"
	"github.com/kota65535/ghs/internal/schema"
)

// nameField identifies a collection element. Every element carries it, whether
// the create request sends it in the body or in the path.
const nameField = schema.NameField

// parseCollectionNode validates a declared collection: the elements themselves
// and whatever is declared under each of them.
func parseCollectionNode(path string, node schema.Node, declared any) (*Declaration, []string) {
	if declared == nil {
		// A field reads null as "clear this", but here the same half-written
		// line would declare that every element be deleted. Since there is an
		// explicit way to say that, null is left without a meaning.
		return nil, []string{fmt.Sprintf("%s: expected a sequence of elements (write [] to declare an empty set)", path)}
	}

	sequence, ok := declared.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("%s: expected a sequence of elements", path)}
	}

	elements := make(map[string]map[string]any, len(sequence))
	elementChildren := map[string]map[string]*Declaration{}
	var problems []string

	for i, raw := range sequence {
		// Until the name is known an element can only be located by position.
		elementPath := fmt.Sprintf("%s[%d]", path, i)

		fields, ok := raw.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expected a mapping of fields", elementPath))
			continue
		}

		name, ok := fields[nameField].(string)
		if !ok || name == "" {
			problems = append(problems, fmt.Sprintf("%s: %s is required and must be a non-empty string", elementPath, nameField))
			continue
		}

		elementPath = diff.ElementPath(path, name)
		if _, seen := elements[name]; seen {
			problems = append(problems, fmt.Sprintf("%s: declared twice", elementPath))
			continue
		}

		// An element holds its own fields and, under keys of their own, the
		// collections it owns -- the variables of an environment.
		own, children, childProblems := split(elementPath, node, fields)
		problems = append(problems, childProblems...)
		problems = append(problems, validateFields(elementPath, own, node.Fields)...)

		normalized, err := diff.NormalizeMap(own)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", elementPath, err))
			continue
		}

		elements[name] = normalized
		if len(children) > 0 {
			elementChildren[name] = children
		}
	}

	return &Declaration{Node: node, Elements: elements, ElementChildren: elementChildren}, problems
}
