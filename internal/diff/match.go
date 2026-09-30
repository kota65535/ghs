package diff

import (
	"fmt"
	"strings"
)

// Matches says which arrays hold elements identified by a key, and what the
// key is.
//
// It is keyed by where the array sits in the node being compared, as dotted
// field names with no element index or key in them: the required status checks
// of a ruleset are at rules.parameters.required_status_checks whichever rule
// they are reached through. Each value lists the fields that together identify
// an element; a field may itself be a dotted path into the element.
type Matches map[string][]string

// KeyOf renders the key of an element as it appears in a label: the values of
// the match fields, in order, as they are written in the settings file. A field
// the element does not have reads as null.
//
// The rendering is also the element's identity, since two keys render the same
// only when they hold the same values.
func KeyOf(element map[string]any, match []string) string {
	parts := make([]string, len(match))
	for i, field := range match {
		parts[i] = formatKey(lookup(element, field))
	}
	return strings.Join(parts, ", ")
}

// lookup follows a dotted path into an element.
func lookup(element map[string]any, path string) any {
	var value any = element
	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

// formatKey renders one value of a key. Numbers arrive from JSON as float64,
// and an integral one is written as the integer it is, as it is in the file.
func formatKey(v any) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", value)
	case float64:
		if value == float64(int64(value)) {
			return fmt.Sprintf("%d", int64(value))
		}
	}
	return fmt.Sprintf("%v", v)
}

// byKey indexes the elements of an array by key, reporting false when that is
// not possible: an element that is not an object has no key, and a key that
// repeats does not tell its elements apart.
func byKey(items []any, match []string) (map[string]any, bool) {
	out := make(map[string]any, len(items))
	for _, item := range items {
		element, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		key := KeyOf(element, match)
		if _, repeated := out[key]; repeated {
			return nil, false
		}
		out[key] = element
	}
	return out, true
}

// compareKeyed compares two arrays as sets of elements identified by match,
// reporting false and recording nothing when either side cannot be indexed by
// it. The caller then pairs them by position, which is all there is to go on.
//
// Paired elements are compared field by field like any other object, so that a
// default GitHub fills into an element is left alone when it is not declared.
// An element on one side only is reported whole.
func compareKeyed(path, shape string, currentArray, desiredArray []any, match []string, matches Matches, changes *[]Change) bool {
	current, ok := byKey(currentArray, match)
	if !ok {
		return false
	}
	desired, ok := byKey(desiredArray, match)
	if !ok {
		return false
	}

	for key, desiredElement := range desired {
		label := fmt.Sprintf("%s[%s]", path, key)
		currentElement, present := current[key]
		if !present {
			*changes = append(*changes, Change{Label: label, Desired: desiredElement, CurrentMissing: true})
			continue
		}
		compare(label, shape, currentElement, desiredElement, true, matches, changes)
	}
	for key, currentElement := range current {
		if _, declared := desired[key]; declared {
			continue
		}
		*changes = append(*changes, Change{Label: fmt.Sprintf("%s[%s]", path, key), Current: currentElement, DesiredMissing: true})
	}
	return true
}
