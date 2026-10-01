package cmd

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/diff"
)

// renamed plans and applies a declaration against what client reports,
// returning the text plan and the writes apply made.
func renamed(t *testing.T, client *fakeClient, yaml string) (string, []string) {
	t.Helper()

	p := planFor(t, client, yaml)
	if got := p.plan.Summarize(); got.Changed != 1 || got.Created != 0 || got.Deleted != 0 {
		t.Errorf("summary = %+v, want one change and nothing added or removed", got)
	}

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p, diff.FormatText); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return out.String(), client.calls()
}

func TestApplyRenamesALabelRatherThanReplacingIt(t *testing.T) {
	// Deleting the label would take it off the issues that carry it.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/labels": `[{"id": 1, "name": "bug", "color": "d73a4a", "description": "Something is not working"}]`,
	}}

	output, calls := renamed(t, client, `
labels:
  - name: defect
    color: d73a4a
`)

	if want := []string{"PATCH repos/kota65535/ghs/labels/bug"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("made %v, want %v", calls, want)
	}
	if body := client.writes[0].body; body["new_name"] != "defect" {
		t.Errorf("body = %+v, want the new name as new_name", body)
	}
	if !strings.Contains(output, `~ name: "bug" -> "defect"`) {
		t.Errorf("output does not show the rename:\n%s", output)
	}
}

func TestApplyRenamesARulesetRatherThanReplacingIt(t *testing.T) {
	// A ruleset created anew would get a new id and lose its history.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/rulesets":   `[{"id": 7, "name": "protect-main"}]`,
		"repos/kota65535/ghs/rulesets/7": `{"id": 7, "name": "protect-main", "enforcement": "active", "rules": [{"type": "deletion"}]}`,
	}}

	_, calls := renamed(t, client, `
rulesets:
  - name: protect-default
    enforcement: active
    rules:
      - type: deletion
`)

	if want := []string{"PUT repos/kota65535/ghs/rulesets/7"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("made %v, want %v", calls, want)
	}
	if body := client.writes[0].body; body["name"] != "protect-default" {
		t.Errorf("body = %+v, want the new name", body)
	}
}

func TestApplyRenamesAVariableRatherThanReplacingIt(t *testing.T) {
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/actions/variables": `{"variables": [{"name": "REGION", "value": "ap-northeast-1"}]}`,
	}}

	_, calls := renamed(t, client, `
actions:
  variables:
    - name: DEPLOY_REGION
      value: ap-northeast-1
`)

	if want := []string{"PATCH repos/kota65535/ghs/actions/variables/REGION"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("made %v, want %v", calls, want)
	}
	if body := client.writes[0].body; body["name"] != "DEPLOY_REGION" {
		t.Errorf("body = %+v, want the new name", body)
	}
}

func TestPlanReplacesAnElementThatChangedTooMuchToBeARename(t *testing.T) {
	// A colour the file also changes is not a claim that this is the old
	// label, so the literal reading stands.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/labels": `[{"id": 1, "name": "bug", "color": "d73a4a"}]`,
	}}

	p := planFor(t, client, `
labels:
  - name: defect
    color: 0e8a16
`)
	if got := p.plan.Summarize(); got.Created != 1 || got.Deleted != 1 {
		t.Errorf("summary = %+v, want one added and one removed", got)
	}
}

func TestPlanReplacesAnEnvironmentUnderANewName(t *testing.T) {
	// There is no renaming an environment: it is addressed by the name PUT
	// created it under.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/environments": `{"environments": [{"id": 1, "name": "staging"}]}`,
	}}

	p := planFor(t, client, `
environments:
  - name: preview
    wait_timer: 0
`)
	if got := p.plan.Summarize(); got.Created != 1 || got.Deleted != 1 {
		t.Errorf("summary = %+v, want one added and one removed", got)
	}
}

func TestApplyRenamesARulesetGitHubFilledIn(t *testing.T) {
	// What GitHub reports is not what was declared: it fills in the parameters
	// a rule leaves out, reports the rules in an order of its own and adds
	// fields the file never mentions. A rename is recognised the way a plan
	// compares anything else, by the declared fields alone.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/rulesets": `[{"id": 7, "name": "protect-main"}]`,
		"repos/kota65535/ghs/rulesets/7": `{
			"id": 7, "name": "protect-main", "target": "branch", "enforcement": "active",
			"source": "kota65535/ghs", "source_type": "Repository",
			"conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
			"bypass_actors": [],
			"rules": [
				{"type": "pull_request", "parameters": {
					"required_approving_review_count": 1,
					"dismiss_stale_reviews_on_push": false,
					"require_code_owner_review": false,
					"require_last_push_approval": false,
					"required_review_thread_resolution": false,
					"allowed_merge_methods": ["merge", "squash", "rebase"]
				}},
				{"type": "deletion"}
			]
		}`,
	}}

	_, calls := renamed(t, client, `
rulesets:
  - name: protect-default
    target: branch
    enforcement: active
    conditions:
      ref_name:
        include: ["~DEFAULT_BRANCH"]
    rules:
      - type: deletion
      - type: pull_request
        parameters:
          required_approving_review_count: 1
`)

	if want := []string{"PUT repos/kota65535/ghs/rulesets/7"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("made %v, want %v", calls, want)
	}
}
