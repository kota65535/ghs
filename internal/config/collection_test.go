package config

import (
	"strings"
	"testing"
)

func TestParseCollectionKeepsElementsByName(t *testing.T) {
	cfg := mustParse(t, `
actions:
  variables:
    - name: DEPLOY_REGION
      value: ap-northeast-1
    - name: LOG_LEVEL
      value: info
`)

	variables := child(t, cfg, "actions", "variables")
	if len(variables.Elements) != 2 {
		t.Fatalf("declared %d elements, want 2: %+v", len(variables.Elements), variables.Elements)
	}
	if variables.Elements["DEPLOY_REGION"]["value"] != "ap-northeast-1" {
		t.Errorf("DEPLOY_REGION = %+v, want its declared value", variables.Elements["DEPLOY_REGION"])
	}
	// The name stays part of the element: it is what the create request sends.
	if variables.Elements["LOG_LEVEL"]["name"] != "LOG_LEVEL" {
		t.Errorf("LOG_LEVEL = %+v, want the name kept", variables.Elements["LOG_LEVEL"])
	}
}

func TestParseCollectionAcceptsAnEmptySequence(t *testing.T) {
	// "rulesets: []" declares a set with no members, which is what asks for
	// every existing one to be deleted. It is not the same as leaving the key
	// out, which declares nothing and touches nothing.
	cfg := mustParse(t, "rulesets: []\n")

	rulesets := child(t, cfg, "rulesets")
	if rulesets.Elements == nil {
		t.Fatal("rulesets was dropped, want an empty set kept as a declaration")
	}
	if len(rulesets.Elements) != 0 {
		t.Errorf("elements = %+v, want none", rulesets.Elements)
	}
}

func TestParseCollectionRejectsNull(t *testing.T) {
	// A field reads null as "clear this", but for a collection a half-written
	// line would declare the deletion of every element. There is an explicit
	// way to say that, so null means nothing.
	for _, yaml := range []string{"rulesets:\n", "rulesets: null\n"} {
		_, err := parse(t, yaml)
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
	_, err := parse(t, "rulesets:\n  protect-main: {}\n")
	if err == nil || !strings.Contains(err.Error(), "sequence") {
		t.Errorf("err = %v, want a sequence to be asked for", err)
	}
}

func TestParseCollectionRequiresAName(t *testing.T) {
	for name, yaml := range map[string]string{
		"missing": "actions:\n  variables:\n    - value: 1\n",
		"null":    "actions:\n  variables:\n    - name:\n      value: 1\n",
		"empty":   "actions:\n  variables:\n    - name: \"\"\n      value: 1\n",
	} {
		_, err := parse(t, yaml)
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
actions:
  variables:
    - name: A
      value: 1
    - name: A
      value: 2
`)
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("err = %v, want the duplicate reported", err)
	}
}

func TestParseCollectionLocatesABadElementByPosition(t *testing.T) {
	_, err := parse(t, "actions:\n  variables:\n    - DEPLOY_REGION\n")
	if err == nil || !strings.Contains(err.Error(), "actions.variables[0]") {
		t.Errorf("err = %v, want the offending element located by index", err)
	}
}

func TestParseCollectionValidatesElementFields(t *testing.T) {
	_, err := parse(t, `
rulesets:
  - name: protect-main
    enforcemnt: active
`)
	if err == nil || !strings.Contains(err.Error(), `rulesets["protect-main"].enforcemnt`) {
		t.Errorf("err = %v, want the typo reported under the element name", err)
	}
}

func TestParseCollectionValidatesEnums(t *testing.T) {
	_, err := parse(t, `
rulesets:
  - name: protect-main
    enforcement: sometimes
`)
	if err == nil || !strings.Contains(err.Error(), "is not one of") {
		t.Errorf("err = %v, want an enum error", err)
	}
	if !strings.Contains(err.Error(), "evaluate") {
		t.Errorf("err = %v, want the accepted values listed", err)
	}
}

func TestParseCollectionNormalizesNestedNumbers(t *testing.T) {
	cfg := mustParse(t, `
rulesets:
  - name: protect-main
    target: branch
    enforcement: active
    rules:
      - type: pull_request
        parameters:
          required_approving_review_count: 1
`)

	element := child(t, cfg, "rulesets").Elements["protect-main"]
	rules := element["rules"].([]any)
	parameters := rules[0].(map[string]any)["parameters"].(map[string]any)
	if _, ok := parameters["required_approving_review_count"].(float64); !ok {
		t.Errorf("required_approving_review_count has type %T, want float64",
			parameters["required_approving_review_count"])
	}
}

func TestParseCollectionLeavesFreeFormContentAlone(t *testing.T) {
	// A ruleset rule is described as a choice between two dozen shapes, so the
	// description declares no fields for one. Its content goes to the API to
	// judge rather than being rejected here.
	cfg := mustParse(t, `
rulesets:
  - name: protect-main
    rules:
      - type: pull_request
        parameters:
          allowed_merge_methods: [squash]
`)
	rules := child(t, cfg, "rulesets").Elements["protect-main"]["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want one rule", rules)
	}
}

func TestParseKeepsWhatIsDeclaredUnderAnElement(t *testing.T) {
	// The variables of an environment are reached through an endpoint of their
	// own, which does not stop them being written inside the environment.
	cfg := mustParse(t, `
environments:
  - name: production
    wait_timer: 30
    variables:
      - name: DEPLOY_REGION
        value: ap-northeast-1
`)

	environments := child(t, cfg, "environments")
	if environments.Elements["production"]["wait_timer"] != float64(30) {
		t.Errorf("production = %+v, want wait_timer 30", environments.Elements["production"])
	}
	// The variables are not one of the environment's own fields.
	if _, isField := environments.Elements["production"]["variables"]; isField {
		t.Error("variables was taken for a field of the environment")
	}

	variables, ok := environments.ElementChild("production", "variables")
	if !ok {
		t.Fatal("nothing declared under production.variables")
	}
	if variables.Elements["DEPLOY_REGION"]["value"] != "ap-northeast-1" {
		t.Errorf("variables = %+v, want the declared value", variables.Elements)
	}
}

func TestParseValidatesWhatIsDeclaredUnderAnElement(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"unknown field": {
			yaml: "environments:\n  - name: production\n    variables:\n      - name: A\n        valeu: 1\n",
			want: `environments["production"].variables["A"].valeu`,
		},
		"missing name": {
			yaml: "environments:\n  - name: production\n    variables:\n      - value: 1\n",
			want: "name is required",
		},
		"duplicate name": {
			yaml: "environments:\n  - name: production\n    variables:\n      - name: A\n        value: 1\n      - name: A\n        value: 2\n",
			want: "declared twice",
		},
		"not a sequence": {
			yaml: "environments:\n  - name: production\n    variables:\n      A: 1\n",
			want: "expected a sequence",
		},
		"null": {
			yaml: "environments:\n  - name: production\n    variables:\n",
			want: "[]",
		},
	} {
		_, err := parse(t, tc.yaml)
		if err == nil {
			t.Errorf("%s: Parse succeeded, want an error", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestParseAcceptsAnEmptySequenceUnderAnElement(t *testing.T) {
	// As with a collection at the top level, declaring no entries asks for the
	// ones that exist to go.
	cfg := mustParse(t, "environments:\n  - name: production\n    variables: []\n")

	variables, ok := child(t, cfg, "environments").ElementChild("production", "variables")
	if !ok {
		t.Fatal("nothing declared under production.variables")
	}
	if variables.Elements == nil || len(variables.Elements) != 0 {
		t.Errorf("elements = %+v, want an empty set kept", variables.Elements)
	}
}
