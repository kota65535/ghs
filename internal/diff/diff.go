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

// Change is a single difference between the declared settings and the current
// ones: either a field whose value differs, or a collection element to create
// or delete.
type Change struct {
	// Path is the location of the change, such as
	// "repository.allow_auto_merge" or `rulesets["protect-main"].enforcement`.
	Path string

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

	// Action is what apply does about this change.
	Action Action

	// Element names the collection element this change belongs to. It is empty
	// for a single-object resource, which has no elements.
	Element string
}

// action is Action with the zero value read as ActionUpdate, so that a change
// built without naming one still reports what it does.
func (c Change) action() Action {
	if c.Action == "" {
		return ActionUpdate
	}
	return c.Action
}

// Normalize converts a value decoded from YAML into the type shape
// encoding/json produces, so that declared and current values can be compared
// directly.
//
// Without this step every integer differs from itself: gopkg.in/yaml.v3
// decodes 1 as int while encoding/json decodes the same 1 as float64, which
// makes a plan report a change that apply cannot resolve — a diff that never
// goes away no matter how often it is applied.
func Normalize(v any) (any, error) {
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
	normalized, err := Normalize(v)
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
// Only keys present in desired are examined; everything else about the
// resource is left alone. Both arguments are expected to be normalized.
func Compute(prefix string, current, desired map[string]any) []Change {
	var changes []Change
	walk(prefix, current, desired, &changes)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func walk(prefix string, current, desired map[string]any, changes *[]Change) {
	for key, desiredValue := range desired {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		currentValue, present := current[key]
		compare(path, currentValue, desiredValue, present, changes)
	}
}

// compare records how one declared value differs from the current one, going
// into objects and arrays so that the report names the leaf that actually
// differs.
func compare(path string, currentValue, desiredValue any, present bool, changes *[]Change) {
	// A nested object declared where GitHub reports something else is handled
	// by the plain comparison at the end.
	if desiredChild, ok := desiredValue.(map[string]any); ok {
		if !present {
			// Report the leaves as missing rather than the parent, so the
			// output stays at the same granularity everywhere.
			walk(path, nil, desiredChild, changes)
			return
		}
		if currentChild, ok := currentValue.(map[string]any); ok {
			walk(path, currentChild, desiredChild, changes)
			return
		}
	}

	// Arrays are compared including their order, pairing elements by index.
	// Pairing them is what carries partial management into an array: a
	// ruleset rule declares one parameter and GitHub returns it alongside the
	// defaults it filled in, which a comparison of the arrays as wholes would
	// report as a change that apply can never resolve.
	//
	// A difference in length does not change that for the positions the two
	// arrays share. Reporting the whole array instead would drag every
	// undeclared default GitHub filled in into the report, as though removing
	// one rule also removed settings that are not managed at all. Positions
	// past the end of one side are reported on their own.
	//
	// An array absent from the response is still reported whole: there is
	// nothing to pair its elements with.
	if desiredArray, ok := desiredValue.([]any); ok && present {
		if currentArray, ok := currentValue.([]any); ok {
			shared := len(currentArray)
			if len(desiredArray) < shared {
				shared = len(desiredArray)
			}

			for i := 0; i < shared; i++ {
				compare(fmt.Sprintf("%s[%d]", path, i), currentArray[i], desiredArray[i], true, changes)
			}
			for i := shared; i < len(desiredArray); i++ {
				*changes = append(*changes, Change{
					Path:           fmt.Sprintf("%s[%d]", path, i),
					Desired:        desiredArray[i],
					CurrentMissing: true,
					Action:         ActionUpdate,
				})
			}
			for i := shared; i < len(currentArray); i++ {
				*changes = append(*changes, Change{
					Path:           fmt.Sprintf("%s[%d]", path, i),
					Current:        currentArray[i],
					DesiredMissing: true,
					Action:         ActionUpdate,
				})
			}
			return
		}
	}

	if !present {
		*changes = append(*changes, Change{
			Path:           path,
			Desired:        desiredValue,
			CurrentMissing: true,
			Action:         ActionUpdate,
		})
		return
	}

	if reflect.DeepEqual(currentValue, desiredValue) {
		return
	}

	*changes = append(*changes, Change{
		Path:    path,
		Current: currentValue,
		Desired: desiredValue,
		Action:  ActionUpdate,
	})
}
