package diff

import (
	"bytes"
	"strings"
	"testing"
)

func TestComputeCollectionReportsAnElementToCreate(t *testing.T) {
	desired := map[string]map[string]any{
		"release-protect": {"name": "release-protect", "enforcement": "active"},
	}

	changes := ComputeCollection("rulesets", nil, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}

	c := changes[0]
	if c.Action != ActionCreate {
		t.Errorf("Action = %q, want %q", c.Action, ActionCreate)
	}
	if c.Element != "release-protect" {
		t.Errorf("Element = %q, want release-protect", c.Element)
	}
	if c.Path != `rulesets["release-protect"]` {
		t.Errorf("Path = %q, want rulesets[\"release-protect\"]", c.Path)
	}
	// The whole element is carried so that apply sends what was reviewed.
	if got, ok := c.Desired.(map[string]any); !ok || got["enforcement"] != "active" {
		t.Errorf("Desired = %+v, want the declared element", c.Desired)
	}
}

func TestComputeCollectionReportsAnElementToDelete(t *testing.T) {
	// An element GitHub reports but the file no longer declares is removed:
	// declaring the key means declaring the whole set.
	current := map[string]map[string]any{
		"legacy": {"name": "legacy", "id": float64(42)},
	}

	changes := ComputeCollection("rulesets", current, map[string]map[string]any{})
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}

	c := changes[0]
	if c.Action != ActionDelete {
		t.Errorf("Action = %q, want %q", c.Action, ActionDelete)
	}
	if c.Element != "legacy" {
		t.Errorf("Element = %q, want legacy", c.Element)
	}
	// Delete needs the current element: some collections are addressed by a
	// server-issued id rather than by name.
	if got, ok := c.Current.(map[string]any); !ok || got["id"] != float64(42) {
		t.Errorf("Current = %+v, want the current element", c.Current)
	}
	if c.Desired != nil {
		t.Errorf("Desired = %v, want nil", c.Desired)
	}
}

func TestComputeCollectionReportsFieldsOfAnElementToUpdate(t *testing.T) {
	current := map[string]map[string]any{
		"protect-main": {"name": "protect-main", "enforcement": "evaluate", "id": float64(7)},
	}
	desired := map[string]map[string]any{
		"protect-main": {"name": "protect-main", "enforcement": "active"},
	}

	changes := ComputeCollection("rulesets", current, desired)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}

	c := changes[0]
	if c.Action != ActionUpdate {
		t.Errorf("Action = %q, want %q", c.Action, ActionUpdate)
	}
	if c.Element != "protect-main" {
		t.Errorf("Element = %q, want protect-main", c.Element)
	}
	if c.Path != `rulesets["protect-main"].enforcement` {
		t.Errorf("Path = %q, want the field path", c.Path)
	}
	if c.Current != "evaluate" || c.Desired != "active" {
		t.Errorf("Current/Desired = %v/%v, want evaluate/active", c.Current, c.Desired)
	}
}

func TestComputeCollectionIgnoresReadOnlyFieldsOfAMatchedElement(t *testing.T) {
	// Fields the API reports but the file does not declare -- id, timestamps --
	// are not managed, exactly as with a single-object resource.
	current := map[string]map[string]any{
		"protect-main": {
			"name":        "protect-main",
			"enforcement": "active",
			"id":          float64(7),
			"created_at":  "2026-01-01T00:00:00Z",
		},
	}
	desired := map[string]map[string]any{
		"protect-main": {"name": "protect-main", "enforcement": "active"},
	}

	if changes := ComputeCollection("rulesets", current, desired); len(changes) != 0 {
		t.Fatalf("got %+v, want no changes", changes)
	}
}

func TestComputeCollectionSortsElementsByName(t *testing.T) {
	current := map[string]map[string]any{
		"zeta": {"name": "zeta"},
		"beta": {"name": "beta", "value": "old"},
	}
	desired := map[string]map[string]any{
		"beta":  {"name": "beta", "value": "new"},
		"alpha": {"name": "alpha"},
	}

	changes := ComputeCollection("variables", current, desired)
	var paths []string
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	want := []string{`variables["alpha"]`, `variables["beta"].value`, `variables["zeta"]`}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

func TestComputeCollectionWithAnEmptyDeclarationDeletesEverything(t *testing.T) {
	// "rulesets: []" declares a set with no members, which is not the same as
	// leaving the key out.
	current := map[string]map[string]any{
		"a": {"name": "a"},
		"b": {"name": "b"},
	}

	changes := ComputeCollection("rulesets", current, map[string]map[string]any{})
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(changes), changes)
	}
	for _, c := range changes {
		if c.Action != ActionDelete {
			t.Errorf("Action = %q for %q, want %q", c.Action, c.Element, ActionDelete)
		}
	}
}

func TestComputeMarksChangesAsUpdates(t *testing.T) {
	// A single-object resource only ever updates, and says so rather than
	// leaving the action blank for the JSON output to report.
	changes := Compute("repository", map[string]any{"has_wiki": true}, map[string]any{"has_wiki": false})
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Action != ActionUpdate {
		t.Errorf("Action = %q, want %q", changes[0].Action, ActionUpdate)
	}
}

func TestRenderTextGroupsChangesByElement(t *testing.T) {
	changes := []Change{
		{Path: `rulesets["legacy"]`, Element: "legacy", Action: ActionDelete, Current: map[string]any{"name": "legacy"}},
		{Path: `rulesets["protect-main"].enforcement`, Element: "protect-main", Action: ActionUpdate, Current: "evaluate", Desired: "active"},
		{Path: `rulesets["release"]`, Element: "release", Action: ActionCreate, Desired: map[string]any{"name": "release"}},
	}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	// A collection is written as the sequence the settings file writes it as,
	// with the sign on each entry. An entry being updated leads with the name
	// that says which one it is, unsigned because the name is not what
	// changes.
	want := strings.Join([]string{
		"~ rulesets: [",
		"    - {",
		`        - name: "legacy"`,
		"      },",
		"    ~ {",
		`          name:        "protect-main"`,
		`        ~ enforcement: "evaluate" -> "active"`,
		"      },",
		"    + {",
		`        + name: "release"`,
		"      },",
		"  ]",
		"",
		"Plan: 1 to create, 1 to change, 1 to delete.",
		"",
	}, "\n")
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestRenderTextOpensOutAnElementBeingCreated(t *testing.T) {
	// Naming the element is not enough to review its creation by: what the
	// element is made of is the part worth reading before it is sent, and a
	// nested value read as one long line is not read at all.
	changes := []Change{{
		Path:    `rulesets["release"]`,
		Element: "release",
		Action:  ActionCreate,
		Desired: map[string]any{
			"name":        "release",
			"target":      "branch",
			"enforcement": "active",
			"conditions":  map[string]any{"ref_name": map[string]any{"include": []any{"~DEFAULT_BRANCH"}, "exclude": []any{}}},
			"rules":       []any{map[string]any{"type": "deletion"}},
		},
	}}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := strings.Join([]string{
		"~ rulesets: [",
		"    + {",
		"        + conditions: {",
		"            + ref_name: {",
		"                + exclude: []",
		"                + include: [",
		`                    + "~DEFAULT_BRANCH",`,
		"                  ]",
		"              }",
		"          }",
		`        + enforcement: "active"`,
		`        + name:        "release"`,
		"        + rules: [",
		"            + {",
		`                + type: "deletion"`,
		"              },",
		"          ]",
		`        + target:      "branch"`,
		"      },",
		"  ]",
		"",
		"Plan: 1 to create.",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestRenderTextOpensOutAnElementBeingDeleted(t *testing.T) {
	// A deletion is the change worth reading most closely, so what is about to
	// go is spelled out rather than left to the element's name.
	changes := []Change{{
		Path:    `variables["GONE"]`,
		Element: "GONE",
		Action:  ActionDelete,
		Current: map[string]any{"name": "GONE", "value": "stale", "created_at": "2026-01-01T00:00:00Z"},
	}}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := strings.Join([]string{
		"~ variables: [",
		"    - {",
		`        - created_at: "2026-01-01T00:00:00Z"`,
		`        - name:       "GONE"`,
		`        - value:      "stale"`,
		"      },",
		"  ]",
		"",
		"Plan: 1 to delete.",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestRenderTextOpensOutAnEntryBeingDroppedFromAList(t *testing.T) {
	// A whole entry is going, so its fields are laid out rather than written
	// as one line of JSON, which at the size of a ruleset rule is the same as
	// not reporting it.
	current := map[string]map[string]any{
		"example": {
			"name": "example",
			"rules": []any{
				map[string]any{"type": "deletion"},
				map[string]any{
					"type":       "required_status_checks",
					"parameters": map[string]any{"strict_required_status_checks_policy": true},
				},
			},
		},
	}
	desired := map[string]map[string]any{
		"example": {
			"name":  "example",
			"rules": []any{map[string]any{"type": "deletion"}},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, ComputeCollection("rulesets", current, desired), FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := strings.Join([]string{
		"~ rulesets: [",
		"    ~ {",
		`          name:     "example"`,
		"        - rules[1]: {",
		"            - parameters: {",
		"                - strict_required_status_checks_policy: true",
		"              }",
		`            - type:       "required_status_checks"`,
		"          }",
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

func TestRenderTextWritesAScalarListEntryOnOneLine(t *testing.T) {
	current := map[string]any{"topics": []any{"go", "cli"}}
	desired := map[string]any{"topics": []any{"go"}}

	var buf bytes.Buffer
	if err := Render(&buf, Compute("repository", current, desired), FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := strings.Join([]string{
		"~ repository:",
		`    - topics[1]: "cli"`,
		"",
		"Plan: 1 to change.",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestRenderTextOpensOutAnEntryBeingAddedToAList(t *testing.T) {
	current := map[string]any{"rules": []any{}}
	desired := map[string]any{"rules": []any{map[string]any{"type": "deletion"}}}

	var buf bytes.Buffer
	if err := Render(&buf, Compute("rulesets", current, desired), FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := strings.Join([]string{
		"~ rulesets:",
		"    + rules[0]: {",
		`        + type: "deletion"`,
		"      }",
		"",
		"Plan: 1 to change.",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestRenderTextNamesEveryElementItTouches(t *testing.T) {
	// Entries of a sequence have no heading to be identified by, so the name
	// has to appear among the fields whatever is happening to the entry.
	for _, tc := range []struct {
		action Action
		change Change
	}{
		{ActionCreate, Change{Path: `variables["A"]`, Element: "A", Action: ActionCreate,
			Desired: map[string]any{"name": "A", "value": "1"}}},
		{ActionDelete, Change{Path: `variables["A"]`, Element: "A", Action: ActionDelete,
			Current: map[string]any{"name": "A", "value": "1"}}},
		{ActionUpdate, Change{Path: `variables["A"].value`, Element: "A", Action: ActionUpdate,
			Current: "1", Desired: "2"}},
	} {
		var buf bytes.Buffer
		if err := Render(&buf, []Change{tc.change}, FormatText); err != nil {
			t.Fatalf("%s: Render: %v", tc.action, err)
		}

		named := false
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.Contains(line, NameField+":") && strings.Contains(line, `"A"`) {
				named = true
				break
			}
		}
		if !named {
			t.Errorf("%s: output does not name the element:\n%s", tc.action, buf.String())
		}
	}
}

func TestRenderTextCountsAResourceOnceHoweverManyFieldsChange(t *testing.T) {
	changes := []Change{
		{Path: "repository.allow_auto_merge", Action: ActionUpdate, Current: false, Desired: true},
		{Path: "repository.has_wiki", Action: ActionUpdate, Current: true, Desired: false},
	}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "Plan: 1 to change.") {
		t.Errorf("output = %q, want one object counted as changed", buf.String())
	}
}

func TestRenderJSONReportsActionsAndCounts(t *testing.T) {
	changes := []Change{
		{Path: `variables["A"]`, Element: "A", Action: ActionCreate, Desired: map[string]any{"name": "A"}},
		{Path: `variables["B"].value`, Element: "B", Action: ActionUpdate, Current: "1", Desired: "2"},
	}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatJSON); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`"action": "create"`,
		`"element": "A"`,
		`"created": 1`,
		`"changed": 1`,
		`"deleted": 0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMarkdownReportsElementActions(t *testing.T) {
	changes := []Change{
		{Path: `variables["A"]`, Element: "A", Action: ActionCreate, Desired: map[string]any{"name": "A", "value": "1"}},
		{Path: `variables["B"].value`, Element: "B", Action: ActionUpdate, Current: "1", Desired: "2"},
	}

	var buf bytes.Buffer
	if err := Render(&buf, changes, FormatMarkdown); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"| Action | Path | Current | Desired |",
		"| create |",
		"| update |",
		"**Plan: 1 to create, 1 to change.**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
