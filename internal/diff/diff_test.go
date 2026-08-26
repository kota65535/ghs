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
	if changes := Compute("x", current, map[string]any{"items": []any{"b", "a"}}); len(changes) != 1 {
		t.Fatalf("got %d changes, want 1 for a reordered array", len(changes))
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
	out := buf.String()

	for _, want := range []string{
		"repository\n",
		"~ allow_auto_merge",
		"false => true",
		`"https://example.com" => null`,
		"Plan: 2 fields to change.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
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

	for _, want := range []string{"### ghs plan", "| Field | Current | Desired |", "| `repository.has_wiki` | `true` | `false` |", "**Plan: 1 field to change.**"} {
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
