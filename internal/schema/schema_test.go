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
