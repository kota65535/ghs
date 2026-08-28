package diff

import "testing"

func TestNormalizeMakesYAMLIntegersComparableToAPIValues(t *testing.T) {
	// gopkg.in/yaml.v3 gives an int here while the API response gives a
	// float64. Without normalization these compare unequal and the resulting
	// change can never be applied away.
	desired := map[string]any{"required_approving_review_count": 1}
	current := map[string]any{"required_approving_review_count": float64(1)}

	// The premise: comparing the two type shapes directly reports a change.
	if got := Compute(current, desired); len(got) != 1 {
		t.Fatalf("got %d changes without normalization, want 1", len(got))
	}

	normalized, err := NormalizeMap(desired)
	if err != nil {
		t.Fatalf("NormalizeMap: %v", err)
	}
	if changes := Compute(current, normalized); len(changes) != 0 {
		t.Fatalf("got %d changes after normalization, want 0: %+v", len(changes), changes)
	}
}

func TestNormalizeKeepsNestedIntegers(t *testing.T) {
	desired := map[string]any{"rules": map[string]any{"count": 2, "names": []any{"a"}}}
	current := map[string]any{"rules": map[string]any{"count": float64(2), "names": []any{"a"}}}

	normalized, err := NormalizeMap(desired)
	if err != nil {
		t.Fatalf("NormalizeMap: %v", err)
	}
	if changes := Compute(current, normalized); len(changes) != 0 {
		t.Fatalf("got %d changes, want 0: %+v", len(changes), changes)
	}
}

func TestComputeIgnoresFieldsNotDeclared(t *testing.T) {
	current := map[string]any{"has_issues": true, "has_wiki": false}
	desired := map[string]any{"has_issues": true}

	if changes := Compute(current, desired); len(changes) != 0 {
		t.Fatalf("got %+v, want no changes for undeclared fields", changes)
	}
}

func TestComputeReportsChangedField(t *testing.T) {
	current := map[string]any{"allow_auto_merge": false}
	desired := map[string]any{"allow_auto_merge": true}

	changes := Compute(current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	c := changes[0]
	if c.Label != "allow_auto_merge" {
		t.Errorf("Path = %q, want repository.allow_auto_merge", c.Label)
	}
	if c.Current != false || c.Desired != true {
		t.Errorf("Current/Desired = %v/%v, want false/true", c.Current, c.Desired)
	}
	if c.CurrentMissing {
		t.Error("CurrentMissing = true, want false")
	}
}

func TestComputeTreatsNullAsAValue(t *testing.T) {
	// Declaring null means "clear this field", which is a change when GitHub
	// reports a value and no change when it already reports null.
	changes := Compute(map[string]any{"homepage": "https://example.com"}, map[string]any{"homepage": nil})
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Desired != nil {
		t.Errorf("Desired = %v, want nil", changes[0].Desired)
	}

	if changes := Compute(map[string]any{"homepage": nil}, map[string]any{"homepage": nil}); len(changes) != 0 {
		t.Fatalf("got %+v, want no change when both are null", changes)
	}
}

func TestComputeMarksFieldsAbsentFromResponse(t *testing.T) {
	changes := Compute(map[string]any{}, map[string]any{"web_commit_signoff_required": true})
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if !changes[0].CurrentMissing {
		t.Error("CurrentMissing = false, want true for a field the response omits")
	}
	// A missing field must be distinguishable from one reported as null.
	if got := formatValue(changes[0].Current, changes[0].CurrentMissing); got != missingLabel {
		t.Errorf("formatted current = %q, want %q", got, missingLabel)
	}
}

func TestComputeRecursesIntoObjects(t *testing.T) {
	current := map[string]any{
		"security_and_analysis": map[string]any{
			"secret_scanning": map[string]any{"status": "disabled"},
		},
	}
	desired := map[string]any{
		"security_and_analysis": map[string]any{
			"secret_scanning": map[string]any{"status": "enabled"},
		},
	}

	changes := Compute(current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	want := "security_and_analysis.secret_scanning.status"
	if changes[0].Label != want {
		t.Errorf("Path = %q, want %q", changes[0].Label, want)
	}
}

func TestComputeReportsLeavesWhenParentObjectIsAbsent(t *testing.T) {
	desired := map[string]any{
		"security_and_analysis": map[string]any{
			"secret_scanning": map[string]any{"status": "enabled"},
		},
	}

	changes := Compute(map[string]any{}, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if !changes[0].CurrentMissing {
		t.Error("CurrentMissing = false, want true")
	}
	if changes[0].Label != "security_and_analysis.secret_scanning.status" {
		t.Errorf("Path = %q, want the leaf path", changes[0].Label)
	}
}

func TestComputeComparesArraysIncludingOrder(t *testing.T) {
	current := map[string]any{"items": []any{"a", "b"}}

	if changes := Compute(current, map[string]any{"items": []any{"a", "b"}}); len(changes) != 0 {
		t.Fatalf("got %+v, want no change for identical arrays", changes)
	}
	// Reordering is a change: elements are paired by index, so both positions
	// are reported.
	changes := Compute(current, map[string]any{"items": []any{"b", "a"}})
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2 for a reordered array: %+v", len(changes), changes)
	}
	if changes[0].Label != "items[0]" || changes[1].Label != "items[1]" {
		t.Errorf("paths = %q, %q, want x.items[0], x.items[1]", changes[0].Label, changes[1].Label)
	}
}

func TestComputeIgnoresServerDefaultsInsideArrayElements(t *testing.T) {
	// A ruleset rule declares one parameter and GitHub returns it alongside the
	// defaults it filled in. Comparing the arrays as wholes would report a
	// change that apply can never resolve.
	current := map[string]any{
		"rules": []any{
			map[string]any{
				"type": "pull_request",
				"parameters": map[string]any{
					"required_approving_review_count": float64(1),
					"dismiss_stale_reviews_on_push":   false,
					"require_code_owner_review":       false,
				},
			},
		},
	}
	desired := map[string]any{
		"rules": []any{
			map[string]any{
				"type":       "pull_request",
				"parameters": map[string]any{"required_approving_review_count": float64(1)},
			},
		},
	}

	if changes := Compute(current, desired); len(changes) != 0 {
		t.Fatalf("got %+v, want no change when only undeclared defaults differ", changes)
	}
}

func TestComputeReportsTheDifferingFieldInsideAnArrayElement(t *testing.T) {
	current := map[string]any{
		"rules": []any{
			map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": float64(1)}},
		},
	}
	desired := map[string]any{
		"rules": []any{
			map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": float64(2)}},
		},
	}

	changes := Compute(current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	want := "rules[0].parameters.required_approving_review_count"
	if changes[0].Label != want {
		t.Errorf("Path = %q, want %q", changes[0].Label, want)
	}
}

func TestComputeReportsPositionsPastTheEndOfAnArray(t *testing.T) {
	current := map[string]any{"rules": []any{map[string]any{"type": "deletion"}}}
	desired := map[string]any{"rules": []any{
		map[string]any{"type": "deletion"},
		map[string]any{"type": "non_fast_forward"},
	}}

	changes := Compute(current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}

	c := changes[0]
	if c.Label != "rules[1]" {
		t.Errorf("Path = %q, want the position being added", c.Label)
	}
	if !c.CurrentMissing {
		t.Error("CurrentMissing = false, want true for a position the array does not reach")
	}

	// The other way round, a position the declaration does not reach is going.
	changes = Compute(desired, current)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if !changes[0].DesiredMissing {
		t.Error("DesiredMissing = false, want true for a position being dropped")
	}
	if changes[0].Current == nil {
		t.Error("Current is nil, want the entry that is going")
	}
}

func TestComputeIgnoresServerDefaultsEvenWhenAnArrayChangesLength(t *testing.T) {
	// Dropping the last rule must not drag the defaults GitHub filled in on
	// the rules before it into the report. Those fields are not declared, so
	// they are not managed, and saying they are about to go would be false.
	current := map[string]any{
		"rules": []any{
			map[string]any{
				"type": "pull_request",
				"parameters": map[string]any{
					"required_approving_review_count": float64(1),
					"require_code_owner_review":       false,
				},
			},
			map[string]any{"type": "required_status_checks"},
		},
	}
	desired := map[string]any{
		"rules": []any{
			map[string]any{
				"type":       "pull_request",
				"parameters": map[string]any{"required_approving_review_count": float64(1)},
			},
		},
	}

	changes := Compute(current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want only the dropped entry: %+v", len(changes), changes)
	}
	if changes[0].Label != "rules[1]" {
		t.Errorf("Path = %q, want only rulesets.rules[1] reported", changes[0].Label)
	}
}

func TestComputeComparesScalarArrayElementsWholly(t *testing.T) {
	current := map[string]any{"include": []any{"~DEFAULT_BRANCH", "refs/heads/release/*"}}
	desired := map[string]any{"include": []any{"~DEFAULT_BRANCH", "refs/heads/main"}}

	changes := Compute(current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Label != "include[1]" {
		t.Errorf("Path = %q, want rulesets.include[1]", changes[0].Label)
	}
}

func TestComputeReportsArrayElementAgainstAMismatchedType(t *testing.T) {
	// An object declared where GitHub reports a scalar is a change, not a
	// reason to recurse.
	current := map[string]any{"rules": []any{"pull_request"}}
	desired := map[string]any{"rules": []any{map[string]any{"type": "pull_request"}}}

	changes := Compute(current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Label != "rules[0]" {
		t.Errorf("Path = %q, want rulesets.rules[0]", changes[0].Label)
	}
}

func TestComputeSortsChangesByPath(t *testing.T) {
	current := map[string]any{"z": false, "a": false, "m": false}
	desired := map[string]any{"z": true, "a": true, "m": true}

	changes := Compute(current, desired)
	var paths []string
	for _, c := range changes {
		paths = append(paths, c.Label)
	}
	want := []string{"a", "m", "z"}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}
