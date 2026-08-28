package diff

import (
	"bytes"
	"strings"
	"testing"
)

// environment builds a declared or reported environment with the given
// variables, which is the nested collection the tests here are about.
func environment(name string, variables ...map[string]any) map[string]any {
	entries := make([]any, 0, len(variables))
	for _, variable := range variables {
		entries = append(entries, variable)
	}
	return map[string]any{"name": name, "variables": entries}
}

func variable(name, value string) map[string]any {
	return map[string]any{"name": name, "value": value}
}

func TestComputeCollectionMatchesNestedEntriesByName(t *testing.T) {
	// Order carries no meaning here either: the entries are reached one at a
	// time through an API that addresses them by name.
	current := map[string]map[string]any{
		"production": environment("production", variable("B", "2"), variable("A", "1")),
	}
	desired := map[string]map[string]any{
		"production": environment("production", variable("A", "1"), variable("B", "2")),
	}

	if changes := ComputeCollection("environments", current, desired, "variables"); len(changes) != 0 {
		t.Fatalf("got %+v, want no change for the same entries in another order", changes)
	}
}

func TestComputeCollectionReportsNestedEntriesArrivingAndGoing(t *testing.T) {
	current := map[string]map[string]any{
		"production": environment("production", variable("KEEP", "same"), variable("GONE", "x")),
	}
	desired := map[string]map[string]any{
		"production": environment("production", variable("KEEP", "same"), variable("ADD", "new")),
	}

	changes := ComputeCollection("environments", current, desired, "variables")
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(changes), changes)
	}

	added, removed := changes[0], changes[1]
	if added.Path != `environments["production"].variables["ADD"]` {
		t.Errorf("Path = %q, want the entry being added", added.Path)
	}
	if !added.CurrentMissing {
		t.Error("CurrentMissing = false, want true for an entry that is arriving")
	}
	if removed.Path != `environments["production"].variables["GONE"]` {
		t.Errorf("Path = %q, want the entry being dropped", removed.Path)
	}
	if !removed.DesiredMissing {
		t.Error("DesiredMissing = false, want true for an entry that is going")
	}

	// Both belong to the environment, so both are changes to it rather than
	// objects of their own. Counting them separately would report creating a
	// variable as creating an environment.
	for _, c := range changes {
		if c.Element != "production" {
			t.Errorf("Element = %q, want the environment that owns the entry", c.Element)
		}
		if c.Action != ActionUpdate {
			t.Errorf("Action = %q, want %q", c.Action, ActionUpdate)
		}
	}
	if summary := Summarize(changes); summary.Created != 0 || summary.Changed != 1 || summary.Deleted != 0 {
		t.Errorf("summary = %+v, want one object changed", summary)
	}
}

func TestComputeCollectionReportsAChangedNestedValue(t *testing.T) {
	current := map[string]map[string]any{
		"production": environment("production", variable("REGION", "ap-northeast-1")),
	}
	desired := map[string]map[string]any{
		"production": environment("production", variable("REGION", "us-east-1")),
	}

	changes := ComputeCollection("environments", current, desired, "variables")
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Path != `environments["production"].variables["REGION"].value` {
		t.Errorf("Path = %q, want the field that differs", changes[0].Path)
	}
}

func TestComputeCollectionLeavesAnUndeclaredNestedCollectionAlone(t *testing.T) {
	// Not declaring the variables means not managing them, the same as any
	// other field left out. Reporting them as going would be wrong.
	current := map[string]map[string]any{
		"production": environment("production", variable("A", "1")),
	}
	desired := map[string]map[string]any{
		"production": {"name": "production", "wait_timer": float64(30)},
	}

	changes := ComputeCollection("environments", current, desired, "variables")
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want only wait_timer: %+v", len(changes), changes)
	}
	if changes[0].Path != `environments["production"].wait_timer` {
		t.Errorf("Path = %q, want only wait_timer reported", changes[0].Path)
	}
}

func TestComputeCollectionDeclaringNoNestedEntriesDropsThemAll(t *testing.T) {
	current := map[string]map[string]any{
		"production": environment("production", variable("A", "1"), variable("B", "2")),
	}
	desired := map[string]map[string]any{
		"production": environment("production"),
	}

	changes := ComputeCollection("environments", current, desired, "variables")
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want both entries dropped: %+v", len(changes), changes)
	}
	for _, c := range changes {
		if !c.DesiredMissing {
			t.Errorf("%s: DesiredMissing = false, want true", c.Path)
		}
	}
}

func TestComputeCollectionComparesNestedEntriesOnDeclaredFieldsOnly(t *testing.T) {
	// The API reports when a variable was created; the settings file does not
	// declare it, so it is not managed.
	current := map[string]map[string]any{
		"production": environment("production", map[string]any{
			"name":       "REGION",
			"value":      "us-east-1",
			"created_at": "2026-01-01T00:00:00Z",
		}),
	}
	desired := map[string]map[string]any{
		"production": environment("production", variable("REGION", "us-east-1")),
	}

	if changes := ComputeCollection("environments", current, desired, "variables"); len(changes) != 0 {
		t.Fatalf("got %+v, want no change over fields the file does not declare", changes)
	}
}

func TestRenderTextWritesNestedEntriesUnderTheirElement(t *testing.T) {
	current := map[string]map[string]any{
		"production": environment("production", variable("REGION", "ap-northeast-1"), variable("GONE", "x")),
	}
	desired := map[string]map[string]any{
		"production": environment("production", variable("REGION", "us-east-1"), variable("ADD", "new")),
	}

	var buf bytes.Buffer
	if err := Render(&buf, ComputeCollection("environments", current, desired, "variables"), FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// A collection an element owns is written as the sequence the settings
	// file writes it as, the same as the collection holding that element.
	// Writing it as keyed entries would show a shape the file does not have.
	want := strings.Join([]string{
		"~ environments: [",
		"    ~ {",
		`          name: "production"`,
		"        ~ variables: [",
		"            + {",
		`                + name:  "ADD"`,
		`                + value: "new"`,
		"              },",
		"            - {",
		`                - name:  "GONE"`,
		`                - value: "x"`,
		"              },",
		"            ~ {",
		`                  name:  "REGION"`,
		`                ~ value: "ap-northeast-1" -> "us-east-1"`,
		"              },",
		"          ]",
		"      },",
		"  ]",
		"",
		"Plan: 1 to change.",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", buf.String(), want)
	}
}
