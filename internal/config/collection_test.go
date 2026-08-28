package config

import (
	"strings"
	"testing"
)

func TestParseCollectionKeepsElementsByName(t *testing.T) {
	cfg := mustParse(t, `
variables:
  - name: DEPLOY_REGION
    value: ap-northeast-1
  - name: LOG_LEVEL
    value: info
`)

	elements := cfg.Collections["variables"]
	if len(elements) != 2 {
		t.Fatalf("declared %d elements, want 2: %+v", len(elements), elements)
	}
	if elements["DEPLOY_REGION"]["value"] != "ap-northeast-1" {
		t.Errorf("DEPLOY_REGION = %+v, want its declared value", elements["DEPLOY_REGION"])
	}
	// The name stays part of the element: it is what the create request sends.
	if elements["LOG_LEVEL"]["name"] != "LOG_LEVEL" {
		t.Errorf("LOG_LEVEL = %+v, want the name kept", elements["LOG_LEVEL"])
	}
}

func TestParseCollectionAcceptsAnEmptySequence(t *testing.T) {
	// "rulesets: []" declares a set with no members, which is what asks for
	// every existing one to be deleted. It is not the same as leaving the key
	// out, which declares nothing and touches nothing.
	cfg := mustParse(t, "rulesets: []\n")

	elements, declared := cfg.Collections["rulesets"]
	if !declared {
		t.Fatal("rulesets was dropped, want an empty set kept as a declaration")
	}
	if len(elements) != 0 {
		t.Errorf("elements = %+v, want none", elements)
	}
}

func TestParseCollectionRejectsNull(t *testing.T) {
	// A single-object resource reads null as "clear this", but for a
	// collection a half-written line would declare the deletion of every
	// element. There is an explicit way to say that, so null means nothing.
	for _, yaml := range []string{"rulesets:\n", "rulesets: null\n"} {
		_, err := parse(t, yaml, Options{})
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want null rejected", yaml)
			continue
		}
		if !strings.Contains(err.Error(), "[]") {
			t.Errorf("err = %v, want it to point at the empty sequence", err)
		}
	}
}

func TestParseCollectionRejectsAMapping(t *testing.T) {
	_, err := parse(t, "variables:\n  DEPLOY_REGION: ap-northeast-1\n", Options{})
	if err == nil || !strings.Contains(err.Error(), "sequence") {
		t.Errorf("err = %v, want a sequence to be asked for", err)
	}
}

func TestParseCollectionRequiresAName(t *testing.T) {
	for name, yaml := range map[string]string{
		"missing": "variables:\n  - value: 1\n",
		"null":    "variables:\n  - name:\n    value: 1\n",
		"empty":   "variables:\n  - name: \"\"\n    value: 1\n",
	} {
		_, err := parse(t, yaml, Options{})
		if err == nil {
			t.Errorf("%s: Parse succeeded, want a name required", name)
			continue
		}
		if !strings.Contains(err.Error(), "name") {
			t.Errorf("%s: err = %v, want it to mention the name", name, err)
		}
	}
}

func TestParseCollectionRejectsDuplicateNames(t *testing.T) {
	// Two elements of the same name would silently drop one, and which one
	// depends on the order they are read in.
	_, err := parse(t, `
variables:
  - name: A
    value: 1
  - name: A
    value: 2
`, Options{})
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("err = %v, want the duplicate reported", err)
	}
}

func TestParseCollectionRejectsANonMappingElement(t *testing.T) {
	_, err := parse(t, "variables:\n  - DEPLOY_REGION\n", Options{})
	if err == nil || !strings.Contains(err.Error(), "variables[0]") {
		t.Errorf("err = %v, want the offending element located by index", err)
	}
}

func TestParseCollectionValidatesElementFields(t *testing.T) {
	_, err := parse(t, `
rulesets:
  - name: protect-main
    enforcemnt: active
`, Options{})
	if err == nil || !strings.Contains(err.Error(), `rulesets["protect-main"].enforcemnt`) {
		t.Errorf("err = %v, want the typo reported under the element name", err)
	}
}

func TestParseCollectionValidatesEnums(t *testing.T) {
	_, err := parse(t, `
rulesets:
  - name: protect-main
    enforcement: sometimes
`, Options{})
	if err == nil || !strings.Contains(err.Error(), "is not one of") {
		t.Errorf("err = %v, want an enum error", err)
	}
	if !strings.Contains(err.Error(), "evaluate") {
		t.Errorf("err = %v, want the accepted values listed", err)
	}
}

func TestParseCollectionValidatesNestedFields(t *testing.T) {
	cfg := mustParse(t, `
rulesets:
  - name: protect-main
    target: branch
    enforcement: active
    conditions:
      ref_name:
        include: ["~DEFAULT_BRANCH"]
        exclude: []
    rules:
      - type: pull_request
        parameters:
          required_approving_review_count: 1
`)

	element := cfg.Collections["rulesets"]["protect-main"]
	conditions := element["conditions"].(map[string]any)
	refName := conditions["ref_name"].(map[string]any)
	if len(refName["include"].([]any)) != 1 {
		t.Errorf("include = %+v, want one pattern", refName["include"])
	}

	// Numbers inside a rule must normalize like anything else, or the plan
	// reports a change apply cannot resolve.
	rules := element["rules"].([]any)
	parameters := rules[0].(map[string]any)["parameters"].(map[string]any)
	if _, ok := parameters["required_approving_review_count"].(float64); !ok {
		t.Errorf("required_approving_review_count has type %T, want float64",
			parameters["required_approving_review_count"])
	}

	_, err := parse(t, `
rulesets:
  - name: protect-main
    conditions:
      ref_nmae:
        include: []
`, Options{})
	if err == nil || !strings.Contains(err.Error(), "conditions.ref_nmae") {
		t.Errorf("err = %v, want the nested typo reported", err)
	}
}

func TestParseCollectionLeavesFreeFormContentAlone(t *testing.T) {
	// A ruleset rule is described as a choice between two dozen shapes, so the
	// definitions declare no fields for one. Its content goes to the API to
	// judge rather than being rejected here.
	cfg := mustParse(t, `
rulesets:
  - name: protect-main
    rules:
      - type: pull_request
        parameters:
          allowed_merge_methods: [squash]
`)
	rules := cfg.Collections["rulesets"]["protect-main"]["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want one rule", rules)
	}
}

func TestParseReportsEveryProblemInACollectionAtOnce(t *testing.T) {
	_, err := parse(t, `
variables:
  - name: A
    valeu: 1
  - name: B
    value: 2
    extra: 3
`, Options{})
	if err == nil {
		t.Fatal("Parse succeeded, want errors")
	}
	for _, want := range []string{"valeu", "extra"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %s", err, want)
		}
	}
}

func TestParseMixesObjectAndCollectionResources(t *testing.T) {
	cfg := mustParse(t, `
repository:
  has_issues: true

variables:
  - name: A
    value: "1"
`)

	if cfg.Declared["repository"]["has_issues"] != true {
		t.Errorf("repository = %+v, want has_issues true", cfg.Declared["repository"])
	}
	if cfg.Collections["variables"]["A"]["value"] != "1" {
		t.Errorf("variables = %+v, want A declared", cfg.Collections["variables"])
	}

	names := cfg.ResourceNames()
	want := []string{"repository", "variables"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("ResourceNames = %v, want %v", names, want)
	}
	if !cfg.IsCollection("variables") || cfg.IsCollection("repository") {
		t.Error("IsCollection does not tell the two kinds apart")
	}
}

func TestParseRejectsASequenceForASingleObjectResource(t *testing.T) {
	_, err := parse(t, "repository:\n  - has_issues: true\n", Options{})
	if err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Errorf("err = %v, want a mapping to be asked for", err)
	}
}
