package diff

import (
	"fmt"
	"sort"
)

// NameField identifies a collection element. Every element carries it, whether
// the API takes it in the request body or in the path.
const NameField = "name"

// ElementPath is where a collection element appears in a change path.
func ElementPath(prefix, name string) string {
	return fmt.Sprintf("%s[%q]", prefix, name)
}

// MatchElements sorts the declared and reported elements of a collection into
// what has to happen to each.
//
// Declaring the collection's key declares the whole set: an element the API
// reports but the file does not is deleted. Within a matched element the rules
// are those of any other node, so a field the file leaves out -- an id, a
// timestamp -- is not managed.
//
// Both arguments are keyed by element name and expected to be normalized.
func MatchElements(prefix string, current, desired map[string]map[string]any) []ElementMatch {
	var matches []ElementMatch

	for _, name := range sortedElements(desired) {
		match := ElementMatch{
			Name:    name,
			Path:    ElementPath(prefix, name),
			Desired: desired[name],
		}
		if reported, exists := current[name]; exists {
			match.Action = ActionUpdate
			match.Current = reported
		} else {
			match.Action = ActionCreate
		}
		matches = append(matches, match)
	}

	for _, name := range sortedElements(current) {
		if _, declared := desired[name]; declared {
			continue
		}
		// The reported element is carried because it is what addresses the
		// element in the API: rulesets are deleted by a server-issued id
		// rather than by name.
		matches = append(matches, ElementMatch{
			Name:    name,
			Path:    ElementPath(prefix, name),
			Action:  ActionDelete,
			Current: current[name],
		})
	}

	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	return matches
}

// ElementMatch pairs one declared element with the reported one of the same
// name, or reports that only one of the two exists.
type ElementMatch struct {
	Name string

	// Path is where the element sits in the settings file, such as
	// `environments["production"]`.
	Path string

	// Action is what apply does about the element.
	Action Action

	// Current is the element as the API reports it, nil for a create.
	// Desired is the element as declared, nil for a delete.
	Current map[string]any
	Desired map[string]any
}

func sortedElements(elements map[string]map[string]any) []string {
	names := make([]string, 0, len(elements))
	for name := range elements {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
