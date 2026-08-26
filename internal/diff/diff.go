// Package diff compares the settings declared in settings.yml against the
// values GitHub currently reports.
package diff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Change is a single field whose current value differs from the declared one.
type Change struct {
	// Path is the dotted location of the field, such as
	// "repository.allow_auto_merge".
	Path string

	// Current is the value GitHub reports. It is nil when CurrentMissing is
	// true, which is not the same as GitHub reporting null.
	Current any

	// Desired is the value declared in settings.yml.
	Desired any

	// CurrentMissing reports that the field is absent from the API response.
	// This happens when the token cannot see the field or when the feature it
	// belongs to is disabled. Such a field is reported as a change rather than
	// silently treated as equal, because applying it may well fail.
	CurrentMissing bool
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

		// Recurse into objects so that the report names the leaf that actually
		// differs. A nested object declared where GitHub reports something
		// else is handled by the plain comparison below.
		desiredChild, desiredIsObject := desiredValue.(map[string]any)
		if desiredIsObject {
			currentChild, currentIsObject := currentValue.(map[string]any)
			if !present {
				// Report the leaves as missing rather than the parent, so the
				// output stays at the same granularity everywhere.
				walk(path, nil, desiredChild, changes)
				continue
			}
			if currentIsObject {
				walk(path, currentChild, desiredChild, changes)
				continue
			}
		}

		if !present {
			*changes = append(*changes, Change{
				Path:           path,
				Desired:        desiredValue,
				CurrentMissing: true,
			})
			continue
		}

		// Arrays are compared including their order. Fields whose order
		// carries no meaning need a per-field rule, which is added when such a
		// field is actually managed.
		if reflect.DeepEqual(currentValue, desiredValue) {
			continue
		}

		*changes = append(*changes, Change{
			Path:    path,
			Current: currentValue,
			Desired: desiredValue,
		})
	}
}
