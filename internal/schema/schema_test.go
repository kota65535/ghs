package schema

import "testing"

func TestRepositoryResourceIsGenerated(t *testing.T) {
	r, ok := Lookup("repository")
	if !ok {
		t.Fatal("repository is not registered; run go generate ./...")
	}

	// A sample of fields the request body schema declares. These are what the
	// settings file is expected to manage day to day.
	for _, field := range []string{"has_issues", "delete_branch_on_merge", "squash_merge_commit_title", "security_and_analysis"} {
		if _, ok := r.Get(field); !ok {
			t.Errorf("%s is missing from the generated fields", field)
		}
	}

	// Fields that only ever appear in responses must not be writable.
	for _, field := range []string{"id", "full_name", "owner", "node_id", "pushed_at"} {
		if _, ok := r.Get(field); ok {
			t.Errorf("%s is writable according to the generated fields, want it absent", field)
		}
	}
}

func TestCollectionResourcesAreGenerated(t *testing.T) {
	for _, tc := range []struct {
		resource string
		fields   []string
	}{
		{"variables", []string{"name", "value"}},
		{"rulesets", []string{"name", "target", "enforcement", "conditions", "rules"}},
		{"environments", []string{"name", "wait_timer", "reviewers", "deployment_branch_policy"}},
	} {
		r, ok := Lookup(tc.resource)
		if !ok {
			t.Errorf("%s is not registered; run go generate ./...", tc.resource)
			continue
		}
		if !r.IsCollection() {
			t.Errorf("%s has kind %q, want %q", tc.resource, r.Kind, KindCollection)
		}
		for _, field := range tc.fields {
			if _, ok := r.Get(field); !ok {
				t.Errorf("%s.%s is missing from the generated fields", tc.resource, field)
			}
		}
	}

	if r, _ := Lookup("repository"); r.IsCollection() {
		t.Errorf("repository has kind %q, want %q", r.Kind, KindObject)
	}
}

func TestEnvironmentNameIsAFieldEvenThoughTheBodyOmitsIt(t *testing.T) {
	// Creating an environment takes its name in the path, so the request body
	// the definitions come from has no name. Elements are matched by name, so
	// declaring one has to be allowed.
	r, _ := Lookup("environments")
	name, ok := r.Get("name")
	if !ok {
		t.Fatal("environments.name is missing")
	}
	if name.Type != "string" {
		t.Errorf("environments.name has type %q, want string", name.Type)
	}
}

func TestReferencedSchemasAreFollowed(t *testing.T) {
	// The ruleset request body shares schemas through $ref. Without following
	// them the enum and the nested fields are lost, and settings the API
	// rejects would pass validation.
	r, _ := Lookup("rulesets")

	enforcement, _ := r.Get("enforcement")
	if len(enforcement.Enum) == 0 {
		t.Error("rulesets.enforcement has no enum, want the accepted values")
	}

	conditions, _ := r.Get("conditions")
	refName, ok := conditions.Fields["ref_name"]
	if !ok {
		t.Fatal("rulesets.conditions.ref_name is missing")
	}
	if _, ok := refName.Fields["include"]; !ok {
		t.Error("rulesets.conditions.ref_name.include is missing")
	}

	// A schema built from oneOf declares no properties of its own, so a rule
	// stays free-form: which fields each variant takes is left to the API.
	rules, _ := r.Get("rules")
	if rules.Type != "array" {
		t.Errorf("rulesets.rules has type %q, want array", rules.Type)
	}
}

func TestEnumsAndNestedFieldsAreCaptured(t *testing.T) {
	r, _ := Lookup("repository")

	title, _ := r.Get("squash_merge_commit_title")
	if len(title.Enum) == 0 {
		t.Error("squash_merge_commit_title has no enum, want the accepted values")
	}

	analysis, _ := r.Get("security_and_analysis")
	if len(analysis.Fields) == 0 {
		t.Fatal("security_and_analysis has no nested fields, want them captured for validation")
	}
	if _, ok := analysis.Fields["secret_scanning"]; !ok {
		t.Error("security_and_analysis.secret_scanning is missing")
	}
}
