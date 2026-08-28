package diff

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalizeMakesYAMLIntegersComparableToAPIValues(t *testing.T) {
	// gopkg.in/yaml.v3 gives an int here while the API response gives a
	// float64. Without normalization these compare unequal and the resulting
	// change can never be applied away.
	desired := map[string]any{"required_approving_review_count": 1}
	current := map[string]any{"required_approving_review_count": float64(1)}

	// The premise: comparing the two type shapes directly reports a change.
	if got := Compute("rulesets", current, desired); len(got) != 1 {
		t.Fatalf("got %d changes without normalization, want 1", len(got))
	}

	normalized, err := NormalizeMap(desired)
	if err != nil {
		t.Fatalf("NormalizeMap: %v", err)
	}
	if changes := Compute("rulesets", current, normalized); len(changes) != 0 {
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
	if changes := Compute("x", current, normalized); len(changes) != 0 {
		t.Fatalf("got %d changes, want 0: %+v", len(changes), changes)
	}
}

func TestComputeIgnoresFieldsNotDeclared(t *testing.T) {
	current := map[string]any{"has_issues": true, "has_wiki": false}
	desired := map[string]any{"has_issues": true}

	if changes := Compute("repository", current, desired); len(changes) != 0 {
		t.Fatalf("got %+v, want no changes for undeclared fields", changes)
	}
}

func TestComputeReportsChangedField(t *testing.T) {
	current := map[string]any{"allow_auto_merge": false}
	desired := map[string]any{"allow_auto_merge": true}

	changes := Compute("repository", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	c := changes[0]
	if c.Path != "repository.allow_auto_merge" {
		t.Errorf("Path = %q, want repository.allow_auto_merge", c.Path)
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
	changes := Compute("repository", map[string]any{"homepage": "https://example.com"}, map[string]any{"homepage": nil})
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Desired != nil {
		t.Errorf("Desired = %v, want nil", changes[0].Desired)
	}

	if changes := Compute("repository", map[string]any{"homepage": nil}, map[string]any{"homepage": nil}); len(changes) != 0 {
		t.Fatalf("got %+v, want no change when both are null", changes)
	}
}

func TestComputeMarksFieldsAbsentFromResponse(t *testing.T) {
	changes := Compute("repository", map[string]any{}, map[string]any{"web_commit_signoff_required": true})
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

	changes := Compute("repository", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	want := "repository.security_and_analysis.secret_scanning.status"
	if changes[0].Path != want {
		t.Errorf("Path = %q, want %q", changes[0].Path, want)
	}
}

func TestComputeReportsLeavesWhenParentObjectIsAbsent(t *testing.T) {
	desired := map[string]any{
		"security_and_analysis": map[string]any{
			"secret_scanning": map[string]any{"status": "enabled"},
		},
	}

	changes := Compute("repository", map[string]any{}, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if !changes[0].CurrentMissing {
		t.Error("CurrentMissing = false, want true")
	}
	if changes[0].Path != "repository.security_and_analysis.secret_scanning.status" {
		t.Errorf("Path = %q, want the leaf path", changes[0].Path)
	}
}

func TestComputeComparesArraysIncludingOrder(t *testing.T) {
	current := map[string]any{"items": []any{"a", "b"}}

	if changes := Compute("x", current, map[string]any{"items": []any{"a", "b"}}); len(changes) != 0 {
		t.Fatalf("got %+v, want no change for identical arrays", changes)
	}
	// Reordering is a change: elements are paired by index, so both positions
	// are reported.
	changes := Compute("x", current, map[string]any{"items": []any{"b", "a"}})
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2 for a reordered array: %+v", len(changes), changes)
	}
	if changes[0].Path != "x.items[0]" || changes[1].Path != "x.items[1]" {
		t.Errorf("paths = %q, %q, want x.items[0], x.items[1]", changes[0].Path, changes[1].Path)
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

	if changes := Compute("rulesets", current, desired); len(changes) != 0 {
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

	changes := Compute("rulesets", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	want := "rulesets.rules[0].parameters.required_approving_review_count"
	if changes[0].Path != want {
		t.Errorf("Path = %q, want %q", changes[0].Path, want)
	}
}

func TestComputeReportsPositionsPastTheEndOfAnArray(t *testing.T) {
	current := map[string]any{"rules": []any{map[string]any{"type": "deletion"}}}
	desired := map[string]any{"rules": []any{
		map[string]any{"type": "deletion"},
		map[string]any{"type": "non_fast_forward"},
	}}

	changes := Compute("rulesets", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}

	c := changes[0]
	if c.Path != "rulesets.rules[1]" {
		t.Errorf("Path = %q, want the position being added", c.Path)
	}
	if !c.CurrentMissing {
		t.Error("CurrentMissing = false, want true for a position the array does not reach")
	}

	// The other way round, a position the declaration does not reach is going.
	changes = Compute("rulesets", desired, current)
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

	changes := Compute("rulesets", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want only the dropped entry: %+v", len(changes), changes)
	}
	if changes[0].Path != "rulesets.rules[1]" {
		t.Errorf("Path = %q, want only rulesets.rules[1] reported", changes[0].Path)
	}
}

func TestComputeComparesScalarArrayElementsWholly(t *testing.T) {
	current := map[string]any{"include": []any{"~DEFAULT_BRANCH", "refs/heads/release/*"}}
	desired := map[string]any{"include": []any{"~DEFAULT_BRANCH", "refs/heads/main"}}

	changes := Compute("rulesets", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Path != "rulesets.include[1]" {
		t.Errorf("Path = %q, want rulesets.include[1]", changes[0].Path)
	}
}

func TestComputeReportsArrayElementAgainstAMismatchedType(t *testing.T) {
	// An object declared where GitHub reports a scalar is a change, not a
	// reason to recurse.
	current := map[string]any{"rules": []any{"pull_request"}}
	desired := map[string]any{"rules": []any{map[string]any{"type": "pull_request"}}}

	changes := Compute("rulesets", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Path != "rulesets.rules[0]" {
		t.Errorf("Path = %q, want rulesets.rules[0]", changes[0].Path)
	}
}

func TestComputeSortsChangesByPath(t *testing.T) {
	current := map[string]any{"z": false, "a": false, "m": false}
	desired := map[string]any{"z": true, "a": true, "m": true}

	changes := Compute("repository", current, desired)
	var paths []string
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	want := []string{"repository.a", "repository.m", "repository.z"}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

func TestRenderText(t *testing.T) {
	changes := []Change{
		{Path: "repository.allow_auto_merge", Current: false, Desired: true},
		{Path: "repository.homepage", Current: "https://example.com", Desired: nil},
	}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Two fields of one resource are one object to change.
	want := strings.Join([]string{
		"~ repository:",
		"    ~ allow_auto_merge: false -> true",
		`    ~ homepage:         "https://example.com" -> null`,
		"",
		"Plan: 1 to change.",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestRenderTextNoChanges(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, nil, FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "No changes") {
		t.Errorf("output = %q, want a no-changes message", buf.String())
	}
}

func TestRenderMarkdown(t *testing.T) {
	changes := []Change{{Path: "repository.has_wiki", Current: true, Desired: false}}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatMarkdown); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"### ghs plan", "| Action | Path | Current | Desired |", "| update | `repository.has_wiki` | `true` | `false` |", "**Plan: 1 to change.**"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderJSON(t *testing.T) {
	changes := []Change{{Path: "repository.has_wiki", Desired: false, CurrentMissing: true}}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatJSON); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{`"path": "repository.has_wiki"`, `"current_missing": true`, `"changed": 1`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"text", "markdown", "json"} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("ParseFormat(%q) failed: %v", s, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("ParseFormat(\"yaml\") succeeded, want an error")
	}
}
