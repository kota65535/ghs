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
//
// nested names the fields of an element that are collections themselves. Those
// are matched entry by entry rather than compared as plain values, because the
// API reaches them one entry at a time and their order carries no meaning.
func ComputeCollection(prefix string, current, desired map[string]map[string]any, nested ...string) []Change {
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

		fields := Compute(path, without(currentElement, nested), without(desired[name], nested))
		for _, field := range nested {
			declared, ok := desired[name][field]
			if !ok {
				// The field was not declared, so the entries it holds are not
				// managed, exactly as with any other field left out.
				continue
			}
			fields = append(fields, computeEntries(path, field, currentElement[field], declared)...)
		}

		for i := range fields {
			fields[i].Element = name
		}
		sort.SliceStable(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
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

// computeEntries compares a collection nested inside an element, matching its
// entries by name.
//
// Entries arriving and going are reported as a value present on one side only,
// not as the creates and deletes a top-level collection produces. Changing the
// variables of an environment is a change to that environment, and counting
// each variable as an object in its own right would say otherwise.
func computeEntries(elementPath, field string, current, desired any) []Change {
	currentEntries := entriesByName(current)
	desiredEntries := entriesByName(desired)

	prefix := elementPath + "." + field
	var changes []Change

	for _, name := range sortedElements(desiredEntries) {
		path := ElementPath(prefix, name)
		currentEntry, exists := currentEntries[name]
		if !exists {
			changes = append(changes, Change{
				Path:           path,
				Desired:        desiredEntries[name],
				CurrentMissing: true,
				Action:         ActionUpdate,
				Nested:         field,
				Entry:          name,
			})
			continue
		}

		entryChanges := Compute(path, currentEntry, desiredEntries[name])
		for i := range entryChanges {
			entryChanges[i].Nested = field
			entryChanges[i].Entry = name
		}
		changes = append(changes, entryChanges...)
	}

	for _, name := range sortedElements(currentEntries) {
		if _, declared := desiredEntries[name]; declared {
			continue
		}
		changes = append(changes, Change{
			Path:           ElementPath(prefix, name),
			Current:        currentEntries[name],
			DesiredMissing: true,
			Action:         ActionUpdate,
			Nested:         field,
			Entry:          name,
		})
	}

	return changes
}

// entriesByName keys a declared or reported sequence of entries by name,
// skipping anything that does not look like a named entry. Malformed input is
// rejected when the settings file is read, and what the API reports is taken
// as it comes.
func entriesByName(value any) map[string]map[string]any {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}

	out := make(map[string]map[string]any, len(entries))
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := entry[NameField].(string); ok && name != "" {
			out[name] = entry
		}
	}
	return out
}

// without returns the element with the named fields left out, so that they can
// be compared by their own rules instead.
func without(element map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return element
	}

	out := make(map[string]any, len(element))
	for key, value := range element {
		out[key] = value
	}
	for _, field := range fields {
		delete(out, field)
	}
	return out
}

func sortedElements(elements map[string]map[string]any) []string {
	names := make([]string, 0, len(elements))
	for name := range elements {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
