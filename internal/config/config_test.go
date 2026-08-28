package config

import (
	"strings"
	"testing"
)

func parse(t *testing.T, yaml string) (*Declaration, error) {
	t.Helper()
	return Parse([]byte(yaml))
}

func mustParse(t *testing.T, yaml string) *Declaration {
	t.Helper()
	cfg, err := parse(t, yaml)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return cfg
}

// child walks down to what was declared under a path of keys.
func child(t *testing.T, cfg *Declaration, keys ...string) *Declaration {
	t.Helper()

	declared := cfg
	for i, key := range keys {
		next, ok := declared.Child(key)
		if !ok {
			t.Fatalf("nothing declared under %v", keys[:i+1])
		}
		declared = next
	}
	return declared
}

func TestTheTopLevelIsTheRepository(t *testing.T) {
	// The file is the repository, so the fields of PATCH /repos/{owner}/{repo}
	// are written at the top level rather than under a key of their own.
	cfg := mustParse(t, `
has_issues: true
delete_branch_on_merge: true
`)

	if len(cfg.Fields) != 2 {
		t.Fatalf("declared %d fields, want 2: %+v", len(cfg.Fields), cfg.Fields)
	}
	if cfg.Fields["has_issues"] != true || cfg.Fields["delete_branch_on_merge"] != true {
		t.Errorf("declared = %+v, want both fields true", cfg.Fields)
	}
}

func TestKeysNamingPathsBecomeChildren(t *testing.T) {
	cfg := mustParse(t, `
has_issues: true

actions:
  permissions:
    enabled: true
    workflow:
      default_workflow_permissions: read
`)

	// The repository's own fields and the keys below it are told apart by the
	// description, not by where they sit.
	if _, isField := cfg.Fields["actions"]; isField {
		t.Error("actions was taken for a field of the repository")
	}

	permissions := child(t, cfg, "actions", "permissions")
	if permissions.Fields["enabled"] != true {
		t.Errorf("permissions = %+v, want enabled true", permissions.Fields)
	}

	workflow := child(t, cfg, "actions", "permissions", "workflow")
	if workflow.Fields["default_workflow_permissions"] != "read" {
		t.Errorf("workflow = %+v, want the declared value", workflow.Fields)
	}
}

func TestNamespacesHoldNoFields(t *testing.T) {
	// GitHub has nothing at /repos/{owner}/{repo}/actions, so a field written
	// directly under that key has nowhere to go.
	_, err := parse(t, "actions:\n  enabled: true\n")
	if err == nil || !strings.Contains(err.Error(), "holds no settings of its own") {
		t.Errorf("err = %v, want the namespace rejected", err)
	}
}

func TestParseNormalizesValues(t *testing.T) {
	// Numbers must come out as float64 so they compare equal to API responses.
	cfg := mustParse(t, "actions:\n  permissions:\n    artifact-and-log-retention:\n      days: 90\n")

	retention := child(t, cfg, "actions", "permissions", "artifact-and-log-retention")
	if _, ok := retention.Fields["days"].(float64); !ok {
		t.Errorf("days has type %T, want float64", retention.Fields["days"])
	}
}

func TestParseRejectsUnknownKey(t *testing.T) {
	_, err := parse(t, "branch_protection:\n  foo: bar\n")
	if err == nil || !strings.Contains(err.Error(), "not a writable field here") {
		t.Errorf("err = %v, want the unknown key rejected", err)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := parse(t, "has_discusions: true\n")
	if err == nil || !strings.Contains(err.Error(), "not a writable field") {
		t.Errorf("err = %v, want a typo to be rejected", err)
	}
}

func TestParseReportsTheFullKeyOfAProblem(t *testing.T) {
	_, err := parse(t, `
actions:
  permissions:
    workflow:
      default_workflow_permisions: read
`)
	if err == nil || !strings.Contains(err.Error(), "actions.permissions.workflow.default_workflow_permisions") {
		t.Errorf("err = %v, want the key spelled out", err)
	}
}

func TestParseAcceptsFieldsPatchedIntoTheDescription(t *testing.T) {
	// has_discussions is accepted by the API but described nowhere in the
	// request body schema, so it reaches the description through the
	// hand-maintained patch rather than the generated part. It is validated
	// like any other field.
	cfg := mustParse(t, "has_discussions: true\n")
	if cfg.Fields["has_discussions"] != true {
		t.Errorf("declared = %+v, want has_discussions kept", cfg.Fields)
	}

	_, err := parse(t, "has_discussions: sure\n")
	if err == nil || !strings.Contains(err.Error(), "expected boolean") {
		t.Errorf("err = %v, want the patched field type-checked too", err)
	}
}

func TestParseRejectsWrongType(t *testing.T) {
	_, err := parse(t, "has_issues: yes-please\n")
	if err == nil || !strings.Contains(err.Error(), "expected boolean") {
		t.Errorf("err = %v, want a type error", err)
	}
}

func TestParseRejectsValueOutsideEnum(t *testing.T) {
	_, err := parse(t, "squash_merge_commit_title: TITLE\n")
	if err == nil || !strings.Contains(err.Error(), "is not one of") {
		t.Errorf("err = %v, want an enum error", err)
	}
	if !strings.Contains(err.Error(), "PR_TITLE") {
		t.Errorf("err = %v, want the accepted values listed", err)
	}
}

func TestParseAcceptsNullToClearAField(t *testing.T) {
	// homepage and description are the repository fields where clearing the
	// value is a thing anyone actually wants to do.
	for _, name := range []string{"homepage", "description"} {
		cfg := mustParse(t, name+": null\n")

		value, present := cfg.Fields[name]
		if !present {
			t.Fatalf("%s was dropped, want it kept so apply clears the field", name)
		}
		if value != nil {
			t.Errorf("%s = %v, want nil", name, value)
		}
	}
}

func TestParsePassesNullThroughWhateverTheFieldIs(t *testing.T) {
	// Which fields accept null is the API's business. The description records
	// it inconsistently, and refusing a null wrongly would leave no way to
	// clear the field at all, so it is passed through and the API decides.
	cases := map[string]string{
		"enum":    "squash_merge_commit_title: null\n",
		"boolean": "has_issues:\n",
	}

	for name, yaml := range cases {
		cfg, err := parse(t, yaml)
		if err != nil {
			t.Errorf("%s: Parse failed: %v", name, err)
			continue
		}
		for _, value := range cfg.Fields {
			if value != nil {
				t.Errorf("%s: value = %v, want nil", name, value)
			}
		}
	}
}

func TestParseValidatesNestedObjects(t *testing.T) {
	cfg := mustParse(t, `
security_and_analysis:
  secret_scanning:
    status: enabled
`)
	nested := cfg.Fields["security_and_analysis"].(map[string]any)
	scanning := nested["secret_scanning"].(map[string]any)
	if scanning["status"] != "enabled" {
		t.Errorf("status = %v, want enabled", scanning["status"])
	}

	_, err := parse(t, `
security_and_analysis:
  secret_scanning:
    stauts: enabled
`)
	if err == nil || !strings.Contains(err.Error(), "security_and_analysis.secret_scanning.stauts") {
		t.Errorf("err = %v, want the nested typo reported with its full path", err)
	}
}

func TestParseReportsEveryProblemAtOnce(t *testing.T) {
	_, err := parse(t, `
has_issues: 3
squash_merge_commit_title: NOPE
has_wikki: true
`)
	if err == nil {
		t.Fatal("Parse succeeded, want errors")
	}
	for _, want := range []string{"has_issues", "squash_merge_commit_title", "has_wikki"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %s", err, want)
		}
	}
}

func TestParseRejectsEmptyFile(t *testing.T) {
	if _, err := parse(t, ""); err == nil {
		t.Error("Parse succeeded with nothing declared")
	}
}
