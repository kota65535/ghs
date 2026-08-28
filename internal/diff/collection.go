package diff

import (
	"fmt"
	"sort"
)

// ComputeCollection returns the changes needed to bring a collection of named
// elements in line with the declared ones.
//
// Declaring the collection's key declares the whole set: an element GitHub
// reports but the file does not is deleted. Within a matched element the rules
// are those of a single-object resource, so a field the file leaves out -- an
// id, a timestamp -- is not managed.
//
// Both arguments are keyed by element name and expected to be normalized.
func ComputeCollection(prefix string, current, desired map[string]map[string]any) []Change {
	var changes []Change

	for _, name := range sortedElements(desired) {
		path := ElementPath(prefix, name)

		currentElement, exists := current[name]
		if !exists {
			changes = append(changes, Change{
				Path:    path,
				Desired: desired[name],
				Action:  ActionCreate,
				Element: name,
			})
			continue
		}

		fields := Compute(path, currentElement, desired[name])
		for i := range fields {
			fields[i].Element = name
		}
		changes = append(changes, fields...)
	}

	for _, name := range sortedElements(current) {
		if _, declared := desired[name]; declared {
			continue
		}
		// The current element is carried because it is what addresses the
		// element in the API: rulesets are deleted by a server-issued id
		// rather than by name.
		changes = append(changes, Change{
			Path:    ElementPath(prefix, name),
			Current: current[name],
			Action:  ActionDelete,
			Element: name,
		})
	}

	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// NameField identifies a collection element. Every element carries it, whether
// the API takes it in the request body or in the path.
const NameField = "name"

// ElementPath is where a collection element appears in a change path.
func ElementPath(prefix, name string) string {
	return fmt.Sprintf("%s[%q]", prefix, name)
}

func sortedElements(elements map[string]map[string]any) []string {
	names := make([]string, 0, len(elements))
	for name := range elements {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
