package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/config"
)

// TestGenerateWritesTheCurrentSettings checks the three things a generated
// file has to get right: only the selected resources appear, only writable
// fields do, and what comes out parses as a settings file.
func TestGenerateWritesTheCurrentSettings(t *testing.T) {
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs": `{
			"name":        "ghs",
			"has_issues":  true,
			"has_wiki":    false,
			"homepage":    null,
			"pushed_at":   "2026-08-31T00:00:00Z",
			"security_and_analysis": {"advanced_security": {"status": "disabled"}}
		}`,
		"repos/kota65535/ghs/actions/permissions":               `{"enabled": true, "allowed_actions": "all", "selected_actions_url": "https://example.invalid"}`,
		"repos/kota65535/ghs/actions/permissions/workflow":      `{"default_workflow_permissions": "read", "can_approve_pull_request_reviews": false}`,
		"repos/kota65535/ghs/actions/variables":                 `{"total_count": 1, "variables": [{"name": "REGION", "value": "us-east-1", "created_at": "2026-01-01T00:00:00Z"}]}`,
		"repos/kota65535/ghs/environments":                      `{"total_count": 1, "environments": [{"id": 1, "name": "production", "protection_rules": [{"type": "wait_timer", "wait_timer": 30}]}]}`,
		"repos/kota65535/ghs/environments/production/variables": `{"total_count": 0, "variables": []}`,
	}}

	settings, err := generate(context.Background(), client, testRepo, []string{"actions", "environments", repositoryKey})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(settings)

	for _, want := range []string{
		"has_issues: true",
		"has_wiki: false",
		"default_workflow_permissions: read",
		"name: REGION",
		"wait_timer: 30",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q:\n%s", want, got)
		}
	}

	// Read-only fields, fields the API reported as null, the fields init
	// leaves out on purpose, and the resource that was not selected.
	for _, unwanted := range []string{
		"pushed_at",
		"selected_actions_url",
		"created_at",
		"homepage",
		"name: ghs",
		"security_and_analysis",
		"rulesets",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("generated file should not hold %q:\n%s", unwanted, got)
		}
	}

	// The repository's own fields come before the keys written under it, and
	// what comes out is a settings file ghs accepts.
	if repository, actions := strings.Index(got, "has_issues"), strings.Index(got, "actions:"); repository > actions {
		t.Errorf("the repository's fields should come first:\n%s", got)
	}
	if _, err := config.Parse(settings); err != nil {
		t.Errorf("generated file does not parse: %v\n%s", err, got)
	}
}

// TestGenerateWritesWhatEachSettingIsFor checks that the field descriptions and
// the operation titles from the API description come out as comments, and that
// they stay within a width a file is read at.
func TestGenerateWritesWhatEachSettingIsFor(t *testing.T) {
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs":                              `{"has_issues": true}`,
		"repos/kota65535/ghs/actions/permissions/workflow": `{"default_workflow_permissions": "read"}`,
		"repos/kota65535/ghs/actions/variables": `{"total_count": 2, "variables": [
			{"name": "A", "value": "1"}, {"name": "B", "value": "2"}]}`,
	}}

	settings, err := generate(context.Background(), client, testRepo, []string{repositoryKey, "actions"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(settings)

	for _, want := range []string{
		// A field, from the request body schema.
		"# Either `true` to enable issues for this repository or `false` to disable\n# them.\nhas_issues: true",
		// A key, from the title of the operation that writes it.
		"# Set default workflow permissions for a repository",
		// The first element of a collection carries the commentary.
		"- # The name of the variable.\n      name: A",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing:\n%s\ngot:\n%s", want, got)
		}
	}

	// The second element does not repeat it: what the fields of an element are
	// is a property of the collection, not of the element.
	if strings.Contains(got, "- # The name of the variable.\n      name: B") {
		t.Errorf("the second element should not repeat the commentary:\n%s", got)
	}

	for _, line := range strings.Split(got, "\n") {
		// Long words are left alone -- the descriptions hold URLs that cannot
		// be broken -- so the check is that wrapping happened at all.
		if len(line) > 80 && !strings.Contains(line, "http") {
			t.Errorf("line is %d characters wide: %q", len(line), line)
		}
	}
}

// TestGenerateDeclaresAnEmptyCollection checks that a selected collection with
// no elements is written as the empty set, which is what says "there should be
// none" rather than "not managed".
func TestGenerateDeclaresAnEmptyCollection(t *testing.T) {
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/rulesets": `[]`,
	}}

	settings, err := generate(context.Background(), client, testRepo, []string{"rulesets"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := string(settings); !strings.Contains(got, "rulesets: []") {
		t.Errorf("want an empty rulesets declaration, got:\n%s", got)
	}
}
