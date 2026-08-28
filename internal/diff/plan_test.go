package diff

import (
	"bytes"
	"strings"
	"testing"
)

// changed is a field whose value differs.
func changed(label string, current, desired any) Change {
	return Change{Label: label, Current: current, Desired: desired}
}

// render is the text form of a plan, which is what the tests below assert on:
// the layout is the point, so they compare it whole rather than by substring.
func render(t *testing.T, plan *Plan) string {
	t.Helper()

	var buf bytes.Buffer
	if err := Render(&buf, plan, FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func lines(l ...string) string { return strings.Join(append(l, ""), "\n") }

func TestRenderNothing(t *testing.T) {
	if out := render(t, &Plan{}); !strings.Contains(out, "No changes") {
		t.Errorf("output = %q, want a no-changes message", out)
	}
}

func TestRenderRepositoryFields(t *testing.T) {
	// The repository is the top level of the file, so its fields are written
	// without a key above them.
	plan := &Plan{Fields: []Change{
		changed("allow_auto_merge", false, true),
		changed("homepage", "https://example.com", nil),
	}}

	want := lines(
		"~ allow_auto_merge: false -> true",
		`~ homepage:         "https://example.com" -> null`,
		"",
		// Two fields of one object are one object to change.
		"Plan: 1 to change.",
	)
	if got := render(t, plan); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderNestedNodes(t *testing.T) {
	// Each key names the path segment it stands for, so the output has the
	// shape of the file and of the API underneath it.
	plan := &Plan{Children: []*Plan{{
		Name: "actions",
		Children: []*Plan{{
			Name:   "permissions",
			Fields: []Change{changed("allowed_actions", "all", "local_only")},
			Children: []*Plan{{
				Name:   "workflow",
				Fields: []Change{changed("default_workflow_permissions", "read", "write")},
			}},
		}},
	}}}

	want := lines(
		"~ actions:",
		"    ~ permissions:",
		`        ~ allowed_actions: "all" -> "local_only"`,
		"        ~ workflow:",
		`            ~ default_workflow_permissions: "read" -> "write"`,
		"",
		"Plan: 2 to change.",
	)
	if got := render(t, plan); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderCollection(t *testing.T) {
	// A collection is written as the sequence the settings file writes it as.
	// An element being updated leads with the name that says which one it is,
	// unsigned because the name is not what changes.
	plan := &Plan{Children: []*Plan{{
		Name:       "rulesets",
		Collection: true,
		Elements: []ElementDiff{
			{Name: "legacy", Path: `rulesets["legacy"]`, Action: ActionDelete,
				Values: map[string]any{"name": "legacy", "id": float64(7)}},
			{Name: "protect-main", Path: `rulesets["protect-main"]`, Action: ActionUpdate,
				Fields: []Change{changed("enforcement", "evaluate", "active")}},
			{Name: "release", Path: `rulesets["release"]`, Action: ActionCreate,
				Values: map[string]any{"name": "release", "target": "branch"}},
		},
	}}}

	want := lines(
		"~ rulesets: [",
		"    - {",
		"        - id:   7",
		`        - name: "legacy"`,
		"      },",
		"    ~ {",
		`          name:        "protect-main"`,
		`        ~ enforcement: "evaluate" -> "active"`,
		"      },",
		"    + {",
		`        + name:   "release"`,
		`        + target: "branch"`,
		"      },",
		"  ]",
		"",
		"Plan: 1 to create, 1 to change, 1 to delete.",
	)
	if got := render(t, plan); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderACollectionUnderAnElement(t *testing.T) {
	// The variables of an environment are written inside it, as the sequence
	// they are. Writing them as keyed lookups would show a shape the file does
	// not have.
	plan := &Plan{Children: []*Plan{{
		Name:       "environments",
		Collection: true,
		Elements: []ElementDiff{{
			Name:   "production",
			Path:   `environments["production"]`,
			Action: ActionUpdate,
			Children: []*Plan{{
				Name:       "variables",
				Collection: true,
				Elements: []ElementDiff{
					{Name: "GONE", Action: ActionDelete, Values: map[string]any{"name": "GONE", "value": "x"}},
					{Name: "REGION", Action: ActionUpdate, Fields: []Change{changed("value", "ap-northeast-1", "us-east-1")}},
				},
			}},
		}},
	}}}

	want := lines(
		"~ environments: [",
		"    ~ {",
		`          name: "production"`,
		"        ~ variables: [",
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
		// The environment counts once: adding a variable to it changes the
		// environment rather than creating an object of its own.
		"Plan: 1 to change.",
	)
	if got := render(t, plan); got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderLeavesOutWhatDoesNotDiffer(t *testing.T) {
	// Reporting a node that matches would bury the changes among the settings
	// that are already as declared.
	plan := &Plan{Children: []*Plan{
		{Name: "actions", Children: []*Plan{{Name: "permissions"}}},
		{Name: "rulesets", Collection: true, Elements: []ElementDiff{
			{Name: "untouched", Action: ActionUpdate},
			{Name: "changed", Action: ActionUpdate, Fields: []Change{changed("enforcement", "evaluate", "active")}},
		}},
	}}

	out := render(t, plan)
	if strings.Contains(out, "actions") {
		t.Errorf("output mentions a node that does not differ:\n%s", out)
	}
	if strings.Contains(out, "untouched") {
		t.Errorf("output mentions an element that does not differ:\n%s", out)
	}
	if !strings.Contains(out, "changed") {
		t.Errorf("output leaves out the element that does differ:\n%s", out)
	}
}

func TestSummarizeCountsObjectsNotFields(t *testing.T) {
	plan := &Plan{
		Fields: []Change{changed("a", 1, 2), changed("b", 1, 2)},
		Children: []*Plan{{
			Name:       "variables",
			Collection: true,
			Elements: []ElementDiff{
				{Name: "add", Action: ActionCreate, Values: map[string]any{"name": "add"}},
				{Name: "gone", Action: ActionDelete, Values: map[string]any{"name": "gone"}},
				{Name: "same", Action: ActionUpdate},
			},
		}},
	}

	got := plan.Summarize()
	want := Summary{Created: 1, Changed: 1, Deleted: 1}
	if got != want {
		t.Errorf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestRenderMarkdownSpellsOutFullPaths(t *testing.T) {
	// A table has no nesting to put a change in, so each row carries the whole
	// path to where it sits.
	plan := &Plan{Children: []*Plan{{
		Name:   "actions",
		Path:   "actions",
		Fields: nil,
		Children: []*Plan{{
			Name:   "permissions",
			Path:   "actions.permissions",
			Fields: []Change{changed("enabled", false, true)},
		}},
	}}}

	var buf bytes.Buffer
	if err := Render(&buf, plan, FormatMarkdown); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"| Action | Path | Current | Desired |",
		"| update | `actions.permissions.enabled` | `false` | `true` |",
		"**Plan: 1 to change.**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderJSONReportsActionsAndCounts(t *testing.T) {
	plan := &Plan{Children: []*Plan{{
		Name:       "variables",
		Path:       "variables",
		Collection: true,
		Elements: []ElementDiff{
			{Name: "A", Path: `variables["A"]`, Action: ActionCreate, Values: map[string]any{"name": "A"}},
			{Name: "B", Path: `variables["B"]`, Action: ActionUpdate, Fields: []Change{changed("value", "1", "2")}},
		},
	}}}

	var buf bytes.Buffer
	if err := Render(&buf, plan, FormatJSON); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`"action": "create"`,
		`"path": "variables[\"A\"]"`,
		`"path": "variables[\"B\"].value"`,
		`"created": 1`,
		`"changed": 1`,
		`"deleted": 0`,
	} {
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
