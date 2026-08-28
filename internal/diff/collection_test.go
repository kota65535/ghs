package diff

import "testing"

func element(fields map[string]any) map[string]any { return fields }

func TestMatchElementsPairsByName(t *testing.T) {
	current := map[string]map[string]any{
		"keep":   element(map[string]any{"name": "keep", "value": "same"}),
		"change": element(map[string]any{"name": "change", "value": "old"}),
		"gone":   element(map[string]any{"name": "gone", "value": "x"}),
	}
	desired := map[string]map[string]any{
		"keep":   element(map[string]any{"name": "keep", "value": "same"}),
		"change": element(map[string]any{"name": "change", "value": "new"}),
		"add":    element(map[string]any{"name": "add", "value": "fresh"}),
	}

	matches := MatchElements("variables", current, desired)
	if len(matches) != 4 {
		t.Fatalf("got %d matches, want 4: %+v", len(matches), matches)
	}

	// Sorted by name, so the report reads the same way however the elements
	// arrived.
	want := []struct {
		name   string
		action Action
	}{
		{"add", ActionCreate},
		{"change", ActionUpdate},
		{"gone", ActionDelete},
		{"keep", ActionUpdate},
	}
	for i, w := range want {
		if matches[i].Name != w.name || matches[i].Action != w.action {
			t.Errorf("match %d = %s/%s, want %s/%s", i, matches[i].Name, matches[i].Action, w.name, w.action)
		}
	}
}

func TestMatchElementsCarriesBothSides(t *testing.T) {
	current := map[string]map[string]any{
		"legacy": element(map[string]any{"name": "legacy", "id": float64(42)}),
	}
	desired := map[string]map[string]any{
		"release": element(map[string]any{"name": "release"}),
	}

	// Sorted by name, so legacy comes first whatever happens to it.
	matches := MatchElements("rulesets", current, desired)

	legacy, release := matches[0], matches[1]
	if release.Name != "release" || release.Action != ActionCreate {
		t.Fatalf("second match = %+v, want release being created", release)
	}
	if release.Current != nil {
		t.Errorf("Current = %+v, want nil for an element being created", release.Current)
	}

	if legacy.Action != ActionDelete {
		t.Fatalf("second match = %+v, want legacy being deleted", legacy)
	}
	// The reported element is what addresses it: rulesets are deleted by a
	// server-issued id rather than by name.
	if legacy.Current["id"] != float64(42) {
		t.Errorf("Current = %+v, want the reported element", legacy.Current)
	}
	if legacy.Desired != nil {
		t.Errorf("Desired = %+v, want nil for an element being deleted", legacy.Desired)
	}
}

func TestMatchElementsWithNothingDeclaredDeletesEverything(t *testing.T) {
	// "rulesets: []" declares a set with no members, which is not the same as
	// leaving the key out.
	current := map[string]map[string]any{
		"a": element(map[string]any{"name": "a"}),
		"b": element(map[string]any{"name": "b"}),
	}

	matches := MatchElements("rulesets", current, map[string]map[string]any{})
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2: %+v", len(matches), matches)
	}
	for _, match := range matches {
		if match.Action != ActionDelete {
			t.Errorf("%s: Action = %q, want %q", match.Name, match.Action, ActionDelete)
		}
	}
}

func TestMatchElementsPathsLocateTheElement(t *testing.T) {
	matches := MatchElements("environments", nil, map[string]map[string]any{
		"review/pr-1": element(map[string]any{"name": "review/pr-1"}),
	})
	if matches[0].Path != `environments["review/pr-1"]` {
		t.Errorf("Path = %q, want the element quoted in place", matches[0].Path)
	}
}

func TestComputeIgnoresFieldsTheFileDoesNotDeclare(t *testing.T) {
	// Within a matched element the rules are those of any other node: an id or
	// a timestamp the API reports is not managed.
	current := element(map[string]any{
		"name":        "protect-main",
		"enforcement": "active",
		"id":          float64(7),
		"created_at":  "2026-01-01T00:00:00Z",
	})
	desired := element(map[string]any{"name": "protect-main", "enforcement": "active"})

	if changes := Compute(current, desired); len(changes) != 0 {
		t.Fatalf("got %+v, want no changes", changes)
	}
}
