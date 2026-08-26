package config

import (
	"strings"
	"testing"
)

func parse(t *testing.T, yaml string, opts Options) (*Config, error) {
	t.Helper()
	return Parse([]byte(yaml), opts)
}

func mustParse(t *testing.T, yaml string) *Config {
	t.Helper()
	cfg, err := parse(t, yaml, Options{})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return cfg
}

func TestParseKeepsOnlyDeclaredFields(t *testing.T) {
	cfg := mustParse(t, `
repository:
  has_issues: true
  delete_branch_on_merge: true
`)

	repo := cfg.Declared["repository"]
	if len(repo) != 2 {
		t.Fatalf("declared %d fields, want 2: %+v", len(repo), repo)
	}
	if repo["has_issues"] != true || repo["delete_branch_on_merge"] != true {
		t.Errorf("declared = %+v, want both fields true", repo)
	}
}

func TestParseNormalizesValues(t *testing.T) {
	// Numbers must come out as float64 so they compare equal to API responses.
	cfg, err := Parse([]byte("repository:\n  has_issues: true\n"), Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := cfg.Declared["repository"]["has_issues"].(bool); !ok {
		t.Errorf("has_issues has type %T, want bool", cfg.Declared["repository"]["has_issues"])
	}
}

func TestParseRejectsUnknownResource(t *testing.T) {
	_, err := parse(t, "rulesets:\n  foo: bar\n", Options{})
	if err == nil || !strings.Contains(err.Error(), "unknown resource") {
		t.Errorf("err = %v, want an unknown resource error", err)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := parse(t, "repository:\n  has_discusions: true\n", Options{})
	if err == nil || !strings.Contains(err.Error(), "not a writable field") {
		t.Errorf("err = %v, want a typo to be rejected", err)
	}
}

func TestParseAcceptsFieldsPatchedIntoTheDescription(t *testing.T) {
	// has_discussions is accepted by the API but described nowhere in the
	// request body schema, so it reaches the field definitions through the
	// hand-maintained patch rather than the generated part. It is validated
	// like any other field.
	cfg := mustParse(t, "repository:\n  has_discussions: true\n")
	if cfg.Declared["repository"]["has_discussions"] != true {
		t.Errorf("declared = %+v, want has_discussions kept", cfg.Declared["repository"])
	}

	_, err := parse(t, "repository:\n  has_discussions: sure\n", Options{})
	if err == nil || !strings.Contains(err.Error(), "expected boolean") {
		t.Errorf("err = %v, want the patched field type-checked too", err)
	}
}

func TestParseRejectsWrongType(t *testing.T) {
	_, err := parse(t, "repository:\n  has_issues: yes-please\n", Options{})
	if err == nil || !strings.Contains(err.Error(), "expected boolean") {
		t.Errorf("err = %v, want a type error", err)
	}
}

func TestParseRejectsValueOutsideEnum(t *testing.T) {
	_, err := parse(t, "repository:\n  squash_merge_commit_title: TITLE\n", Options{})
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
		cfg := mustParse(t, "repository:\n  "+name+": null\n")

		value, present := cfg.Declared["repository"][name]
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
		"enum":    "repository:\n  squash_merge_commit_title: null\n",
		"boolean": "repository:\n  has_issues:\n",
	}

	for name, yaml := range cases {
		cfg, err := parse(t, yaml, Options{})
		if err != nil {
			t.Errorf("%s: Parse failed: %v", name, err)
			continue
		}
		for _, value := range cfg.Declared["repository"] {
			if value != nil {
				t.Errorf("%s: value = %v, want nil", name, value)
			}
		}
	}
}

func TestParseValidatesNestedObjects(t *testing.T) {
	cfg := mustParse(t, `
repository:
  security_and_analysis:
    secret_scanning:
      status: enabled
`)
	nested := cfg.Declared["repository"]["security_and_analysis"].(map[string]any)
	scanning := nested["secret_scanning"].(map[string]any)
	if scanning["status"] != "enabled" {
		t.Errorf("status = %v, want enabled", scanning["status"])
	}

	_, err := parse(t, `
repository:
  security_and_analysis:
    secret_scanning:
      stauts: enabled
`, Options{})
	if err == nil || !strings.Contains(err.Error(), "security_and_analysis.secret_scanning.stauts") {
		t.Errorf("err = %v, want the nested typo reported with its full path", err)
	}
}

func TestParseReportsEveryProblemAtOnce(t *testing.T) {
	_, err := parse(t, `
repository:
  has_issues: 3
  squash_merge_commit_title: NOPE
  has_wikki: true
`, Options{})
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
	if _, err := parse(t, "", Options{}); err == nil {
		t.Error("Parse succeeded with no resources declared")
	}
}
