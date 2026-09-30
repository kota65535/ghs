package diff

import (
	"reflect"
	"testing"
)

// rulesMatch is how the rules of a ruleset are identified: a ruleset holds at
// most one rule of each type.
var rulesMatch = Matches{"rules": {"type"}}

func labels(changes []Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Label)
	}
	return out
}

func TestComputeIgnoresTheOrderOfAKeyedArray(t *testing.T) {
	// The case that made index pairing untenable: GitHub reports the rules in
	// an order of its own, and pairing by position turns that into a
	// difference that applying never removes.
	current := map[string]any{"rules": []any{
		map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": float64(1)}},
		map[string]any{"type": "deletion"},
	}}
	desired := map[string]any{"rules": []any{
		map[string]any{"type": "deletion"},
		map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": float64(1)}},
	}}

	if changes := Compute(current, desired, rulesMatch); len(changes) != 0 {
		t.Fatalf("got %+v, want no changes for a reordering", changes)
	}
}

func TestComputePairsKeyedElementsForPartialComparison(t *testing.T) {
	// GitHub fills in the parameters a rule leaves out. Paired by key, only
	// the declared leaves are compared, as index pairing did before.
	current := map[string]any{"rules": []any{
		map[string]any{"type": "deletion"},
		map[string]any{"type": "pull_request", "parameters": map[string]any{
			"required_approving_review_count": float64(1),
			"dismiss_stale_reviews_on_push":   false,
		}},
	}}
	desired := map[string]any{"rules": []any{
		map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": float64(2)}},
		map[string]any{"type": "deletion"},
	}}

	changes := Compute(current, desired, rulesMatch)
	want := []string{`rules["pull_request"].parameters.required_approving_review_count`}
	if got := labels(changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if changes[0].Current != float64(1) || changes[0].Desired != float64(2) {
		t.Errorf("Current/Desired = %v/%v, want 1/2", changes[0].Current, changes[0].Desired)
	}
}

func TestComputeReportsKeyedElementsOnOneSideOnly(t *testing.T) {
	current := map[string]any{"rules": []any{
		map[string]any{"type": "deletion"},
		map[string]any{"type": "non_fast_forward"},
	}}
	desired := map[string]any{"rules": []any{
		map[string]any{"type": "creation"},
		map[string]any{"type": "deletion"},
	}}

	changes := Compute(current, desired, rulesMatch)
	want := []string{`rules["creation"]`, `rules["non_fast_forward"]`}
	if got := labels(changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if !changes[0].CurrentMissing || changes[0].DesiredMissing {
		t.Errorf("creation: want an addition, got %+v", changes[0])
	}
	if !changes[1].DesiredMissing || changes[1].CurrentMissing {
		t.Errorf("non_fast_forward: want a removal, got %+v", changes[1])
	}
}

func TestComputeMatchesOnCompositeAndNestedKeys(t *testing.T) {
	matches := Matches{
		"bypass_actors":      {"actor_type", "actor_id"},
		"required_reviewers": {"reviewer.type", "reviewer.id"},
	}
	current := map[string]any{
		"bypass_actors": []any{
			map[string]any{"actor_type": "Team", "actor_id": float64(2), "bypass_mode": "always"},
			map[string]any{"actor_type": "OrganizationAdmin", "actor_id": nil, "bypass_mode": "always"},
		},
		"required_reviewers": []any{
			map[string]any{"reviewer": map[string]any{"type": "Team", "id": float64(7)}, "minimum_approvals": float64(1)},
		},
	}
	desired := map[string]any{
		"bypass_actors": []any{
			map[string]any{"actor_type": "OrganizationAdmin", "actor_id": nil, "bypass_mode": "always"},
			map[string]any{"actor_type": "Team", "actor_id": float64(2), "bypass_mode": "pull_request"},
		},
		"required_reviewers": []any{
			map[string]any{"reviewer": map[string]any{"type": "Team", "id": float64(7)}, "minimum_approvals": float64(2)},
		},
	}

	want := []string{
		`bypass_actors["Team", 2].bypass_mode`,
		`required_reviewers["Team", 7].minimum_approvals`,
	}
	if got := labels(Compute(current, desired, matches)); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}

func TestComputeFindsKeyedArraysInsideKeyedElements(t *testing.T) {
	// A match is looked up by where the array sits in the shape of the node,
	// which does not include the key of the element it is reached through.
	matches := Matches{
		"rules": {"type"},
		"rules.parameters.required_status_checks": {"context", "integration_id"},
	}
	check := func(context string) map[string]any {
		return map[string]any{"context": context, "integration_id": nil}
	}
	rule := func(checks ...any) map[string]any {
		return map[string]any{"type": "required_status_checks", "parameters": map[string]any{"required_status_checks": checks}}
	}

	current := map[string]any{"rules": []any{rule(check("lint"), check("test"))}}
	desired := map[string]any{"rules": []any{rule(check("test"), check("lint"))}}
	if changes := Compute(current, desired, matches); len(changes) != 0 {
		t.Fatalf("got %+v, want no changes for a reordering one level down", changes)
	}
}

func TestComputeFallsBackToIndexPairingWhenKeysRepeat(t *testing.T) {
	// A key that does not tell the elements apart says nothing about which of
	// them to pair, so the comparison goes back to what it did without one.
	current := map[string]any{"rules": []any{map[string]any{"type": "a", "n": float64(1)}, map[string]any{"type": "a", "n": float64(2)}}}
	desired := map[string]any{"rules": []any{map[string]any{"type": "a", "n": float64(1)}, map[string]any{"type": "a", "n": float64(3)}}}

	want := []string{"rules[1].n"}
	if got := labels(Compute(current, desired, rulesMatch)); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}

func TestComputeKeepsIndexPairingForArraysWithoutAMatch(t *testing.T) {
	current := map[string]any{"include": []any{"a", "b"}}
	desired := map[string]any{"include": []any{"b", "a"}}

	if changes := Compute(current, desired, rulesMatch); len(changes) != 2 {
		t.Fatalf("got %+v, want both positions reported", changes)
	}
}

func TestKeyOf(t *testing.T) {
	element := map[string]any{"actor_type": "Team", "actor_id": float64(2), "reviewer": map[string]any{"id": float64(7)}}

	for _, tc := range []struct {
		match []string
		want  string
	}{
		{[]string{"actor_type"}, `"Team"`},
		{[]string{"actor_type", "actor_id"}, `"Team", 2`},
		{[]string{"reviewer.id"}, `7`},
		{[]string{"missing"}, `null`},
	} {
		if got := KeyOf(element, tc.match); got != tc.want {
			t.Errorf("KeyOf(%v) = %s, want %s", tc.match, got, tc.want)
		}
	}
}
