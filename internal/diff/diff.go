// Package diff compares the settings declared in settings.yml against the
// values GitHub currently reports.
package diff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Action is what apply does about a change.
type Action string

const (
	// ActionUpdate changes fields of something that already exists. It is what
	// every change to a single-object resource amounts to.
	ActionUpdate Action = "update"

	// ActionCreate adds a collection element that GitHub does not have.
	ActionCreate Action = "create"

	// ActionDelete removes a collection element the settings file no longer
	// declares.
	ActionDelete Action = "delete"
)

// Change is a single field whose declared value differs from the current one.
type Change struct {
	// Label names the field within the node it belongs to, such as
	// "allow_auto_merge" or "rules[0].parameters.required_approving_review_count".
	//
	// Where in the settings file that node sits is the plan's business, not the
	// change's.
	Label string

	// Current is the value GitHub reports. It is nil when CurrentMissing is
	// true, which is not the same as GitHub reporting null. For a delete it is
	// the whole element, which is what addresses it in the API.
	Current any

	// Desired is the value declared in settings.yml. For a create it is the
	// whole element.
	Desired any

	// CurrentMissing reports that there is nothing at this path in the current
	// settings: the API response omits the field, or a list is about to grow
	// past where it ends today.
	//
	// A response omits a field when the token cannot see it or the feature it
	// belongs to is disabled. Such a field is reported as a change rather than
	// silently treated as equal, because applying it may well fail.
	CurrentMissing bool

	// DesiredMissing reports that there is nothing at this path in the
	// declared settings, which happens where a list is about to shrink past
	// where it ends today. It is not what an undeclared field looks like:
	// those are simply not managed and never become changes at all.
	DesiredMissing bool
}

// normalize converts a value decoded from YAML into the type shape
// encoding/json produces, so that declared and current values can be compared
// directly.
//
// Without this step every integer differs from itself: gopkg.in/yaml.v3
// decodes 1 as int while encoding/json decodes the same 1 as float64, which
// makes a plan report a change that apply cannot resolve — a diff that never
// goes away no matter how often it is applied.
func normalize(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}
	return out, nil
}

// NormalizeMap is Normalize for an object.
func NormalizeMap(v map[string]any) (map[string]any, error) {
	normalized, err := normalize(v)
	if err != nil {
		return nil, err
	}
	if normalized == nil {
		return nil, nil
	}
	out, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("normalize: expected an object, got %T", normalized)
	}
	return out, nil
}

// Compute returns the changes needed to bring current in line with desired.
//
// Only keys present in desired are examined; everything else about the node is
// left alone. Both arguments are expected to be normalized.
//
// matches says which arrays are sets of elements identified by a key rather
// than sequences compared position by position. It may be nil.
//
// The labels are relative to the node being compared. Where that node sits in
// the settings file is recorded by the plan the changes go into.
func Compute(current, desired map[string]any, matches Matches) []Change {
	var changes []Change
	walk("", "", current, desired, matches, &changes)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Label < changes[j].Label })
	return changes
}

// walk compares the declared keys of an object.
//
// prefix is the label of the object, and shape where it sits with the element
// indices and keys left out, which is what a match is looked up by.
func walk(prefix, shape string, current, desired map[string]any, matches Matches, changes *[]Change) {
	for key, desiredValue := range desired {
		currentValue, present := current[key]
		compare(join(prefix, key), join(shape, key), currentValue, desiredValue, present, matches, changes)
	}
}

// compare records how one declared value differs from the current one, going
// into objects and arrays so that the report names the leaf that actually
// differs.
func compare(path, shape string, currentValue, desiredValue any, present bool, matches Matches, changes *[]Change) {
	// A nested object declared where GitHub reports something else is handled
	// by the plain comparison at the end.
	if desiredChild, ok := desiredValue.(map[string]any); ok {
		if !present {
			// Report the leaves as missing rather than the parent, so the
			// output stays at the same granularity everywhere.
			walk(path, shape, nil, desiredChild, matches, changes)
			return
		}
		if currentChild, ok := currentValue.(map[string]any); ok {
			walk(path, shape, currentChild, desiredChild, matches, changes)
			return
		}
	}

	// An array whose elements are identified by a key is a set: elements are
	// paired by key, whatever order either side lists them in. GitHub does not
	// report the rules of a ruleset in the order they were sent, and pairing
	// those by position reports a difference that applying never removes.
	if desiredArray, ok := desiredValue.([]any); ok && present {
		if currentArray, ok := currentValue.([]any); ok {
			if match, keyed := matches[shape]; keyed && compareKeyed(path, shape, currentArray, desiredArray, match, matches, changes) {
				return
			}
		}
	}

	if desiredArray, ok := desiredValue.([]any); ok && present {
		if currentArray, ok := currentValue.([]any); ok {
			shared := len(currentArray)
			if len(desiredArray) < shared {
				shared = len(desiredArray)
			}

			for i := 0; i < shared; i++ {
				compare(fmt.Sprintf("%s[%d]", path, i), shape, currentArray[i], desiredArray[i], true, matches, changes)
			}
			for i := shared; i < len(desiredArray); i++ {
				*changes = append(*changes, Change{
					Label:          fmt.Sprintf("%s[%d]", path, i),
					Desired:        desiredArray[i],
					CurrentMissing: true,
				})
			}
			for i := shared; i < len(currentArray); i++ {
				*changes = append(*changes, Change{
					Label:          fmt.Sprintf("%s[%d]", path, i),
					Current:        currentArray[i],
					DesiredMissing: true,
				})
			}
			return
		}
	}

	if !present {
		*changes = append(*changes, Change{
			Label:          path,
			Desired:        desiredValue,
			CurrentMissing: true,
		})
		return
	}

	if reflect.DeepEqual(currentValue, desiredValue) {
		return
	}

	*changes = append(*changes, Change{
		Label:   path,
		Current: currentValue,
		Desired: desiredValue,
	})
}
