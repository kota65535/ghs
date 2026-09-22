package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/diff"
)

func TestApplyACollection(t *testing.T) {
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/actions/variables": `{"variables": [
			{"name": "KEEP", "value": "same"},
			{"name": "CHANGE", "value": "old"},
			{"name": "GONE", "value": "x"}
		]}`,
	}}

	p := planFor(t, client, `
actions:
  variables:
    - name: KEEP
      value: same
    - name: CHANGE
      value: new
    - name: ADD
      value: fresh
`)

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p, diff.FormatText); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Create first and delete last: a failure part way through then leaves a
	// spare element rather than a missing one.
	want := []string{
		"POST repos/kota65535/ghs/actions/variables",
		"PATCH repos/kota65535/ghs/actions/variables/CHANGE",
		"DELETE repos/kota65535/ghs/actions/variables/GONE",
	}
	got := client.calls()
	if len(got) != len(want) {
		t.Fatalf("made %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}

	output := out.String()
	for _, want := range []string{
		"~ actions:",
		"    ~ variables: [",
		`        + name:  "ADD"`,
		`          name:  "CHANGE"`,
		`        ~ value: "old" -> "new"`,
		`        - name:  "GONE"`,
		"Apply complete. 1 created, 1 changed, 1 deleted.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	// An element that matches is left alone entirely.
	if strings.Contains(output, "KEEP") {
		t.Errorf("output mentions an unchanged element:\n%s", output)
	}
}

func TestApplyACollectionUnderAnElement(t *testing.T) {
	// The variables of an environment are reached through that environment's
	// own path, which the walk builds as it passes through the element.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/environments": `{"environments": [{"id": 1, "name": "production"}]}`,
		"repos/kota65535/ghs/environments/production/variables": `{"variables": [
			{"name": "REGION", "value": "ap-northeast-1"},
			{"name": "GONE", "value": "x"}
		]}`,
	}}

	p := planFor(t, client, `
environments:
  - name: production
    wait_timer: 0
    variables:
      - name: REGION
        value: us-east-1
      - name: ADD
        value: fresh
`)

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p, diff.FormatText); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The environment itself does not differ, so only its variables are
	// written -- each under the environment's path.
	want := []string{
		"POST repos/kota65535/ghs/environments/production/variables",
		"PATCH repos/kota65535/ghs/environments/production/variables/REGION",
		"DELETE repos/kota65535/ghs/environments/production/variables/GONE",
	}
	got := client.calls()
	if len(got) != len(want) {
		t.Fatalf("made %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Changing what an environment owns changes that environment: the
	// variables are not objects in their own right.
	if summary := p.plan.Summarize(); summary.Changed != 1 || summary.Created != 0 || summary.Deleted != 0 {
		t.Errorf("summary = %+v, want one object changed", summary)
	}
}

func TestApplyCreatesAnElementBeforeWhatItHolds(t *testing.T) {
	// Nothing can be put into an environment that is not there yet.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/environments": `{"environments": []}`,
	}}

	p := planFor(t, client, `
environments:
  - name: production
    wait_timer: 0
    variables:
      - name: REGION
        value: us-east-1
`)

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p, diff.FormatText); err != nil {
		t.Fatalf("apply: %v", err)
	}

	want := []string{
		"PUT repos/kota65535/ghs/environments/production",
		"POST repos/kota65535/ghs/environments/production/variables",
	}
	got := client.calls()
	if len(got) != len(want) {
		t.Fatalf("made %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeclaringAnEmptyCollectionDeletesEverything(t *testing.T) {
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/actions/variables": `{"variables": [
			{"name": "A", "value": "1"},
			{"name": "B", "value": "2"}
		]}`,
	}}

	p := planFor(t, client, "actions:\n  variables: []\n")

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p, diff.FormatText); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(client.writes) != 2 {
		t.Fatalf("made %v, want both elements deleted", client.calls())
	}
	for _, call := range client.calls() {
		if !strings.HasPrefix(call, "DELETE ") {
			t.Errorf("call = %q, want a delete", call)
		}
	}
	if !strings.Contains(out.String(), "Apply complete. 2 deleted.") {
		t.Errorf("output = %q, want both deletions reported", out.String())
	}
}

func TestAnUndeclaredCollectionIsNotEvenLookedAt(t *testing.T) {
	// Declaring a collection puts its whole set under management, so leaving
	// the key out has to mean more than "no changes": ghs must not ask what is
	// there, because nothing it learned could lead to an action it may take.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs":                   `{"has_issues": false}`,
		"repos/kota65535/ghs/actions/variables": `{"variables": [{"name": "A", "value": "1"}]}`,
	}}

	p := planFor(t, client, "has_issues: true\n")

	if len(p.plan.Children) != 0 {
		t.Errorf("planned children %+v, want only the repository", p.plan.Children)
	}
	if summary := p.plan.Summarize(); summary.Deleted != 0 {
		t.Errorf("summary = %+v, want nothing deleted", summary)
	}
}

func TestApplyStopsAtTheFirstFailure(t *testing.T) {
	client := &fakeClient{
		reads:  map[string]string{"repos/kota65535/ghs/actions/variables": `{"variables": [{"name": "GONE", "value": "x"}]}`},
		failOn: "actions/variables",
	}

	p := planFor(t, client, "actions:\n  variables:\n    - name: ADD\n      value: fresh\n")

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p, diff.FormatText); err == nil {
		t.Fatal("apply succeeded, want the API failure reported")
	}
	// The delete must not have gone out: a failed create leaves the settings
	// further from the declaration, not nearer to it.
	if len(client.writes) != 0 {
		t.Errorf("made %v, want nothing after the failure", client.calls())
	}
}

func TestAutolinksAreAChangeAgainstNothingWhereThePlanLacksThem(t *testing.T) {
	// Autolinks belong to the paid plans, and a repository on GitHub Free
	// answers 403 rather than with an empty list. A file that declares them is
	// still read: the plan reports each as arriving, which is what a
	// conditional path that is not there produces everywhere else, and the rest
	// of the file is not held up by it.
	client := &fakeClient{forbids: "autolinks"}

	p := planFor(t, client, "autolinks:\n  - key_prefix: JIRA-\n    url_template: https://jira.example.com/browse/<num>\n")

	if summary := p.plan.Summarize(); summary.Created != 1 || summary.Deleted != 0 {
		t.Errorf("summary = %+v, want the declared autolink reported as arriving", summary)
	}
}

func TestInitLeavesOutAutolinksThePlanDoesNotHave(t *testing.T) {
	// The file says what the repository has. A repository that cannot have
	// autolinks has none to write, and `autolinks: []` would declare a set
	// nobody can hold rather than say nothing.
	client := &fakeClient{forbids: "autolinks", reads: map[string]string{
		"repos/kota65535/ghs": `{"has_issues": true}`,
	}}

	settings, err := generate(context.Background(), client, testRepo, []string{repositoryKey, "autolinks"}, false)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(string(settings), "autolinks") {
		t.Errorf("settings mention autolinks, want the key left out:\n%s", settings)
	}
}

func TestApplyAnAutolinkReplacesIt(t *testing.T) {
	// GitHub has no endpoint that changes an autolink, so changing one means
	// deleting it and creating it again. The plan still reports it as a change,
	// which is what it is: the autolink the prefix stands for goes on existing,
	// and what the file does not declare is carried over rather than reset.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/autolinks": `[
			{"id": 7, "key_prefix": "JIRA-", "url_template": "https://old.example.com/<num>", "is_alphanumeric": false},
			{"id": 8, "key_prefix": "GONE-", "url_template": "https://gone.example.com/<num>", "is_alphanumeric": true}
		]`,
	}}

	p := planFor(t, client, `
autolinks:
  - key_prefix: JIRA-
    url_template: https://jira.example.com/browse/<num>
  - key_prefix: ADD-
    url_template: https://add.example.com/<num>
`)

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p, diff.FormatText); err != nil {
		t.Fatalf("apply: %v", err)
	}

	want := []string{
		"POST repos/kota65535/ghs/autolinks",     // ADD-, which is new
		"DELETE repos/kota65535/ghs/autolinks/7", // JIRA-, on its way back
		"POST repos/kota65535/ghs/autolinks",     //
		"DELETE repos/kota65535/ghs/autolinks/8", // GONE-, which the file dropped
	}
	got := client.calls()
	if len(got) != len(want) {
		t.Fatalf("made %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The autolink is put back whole: is_alphanumeric is not declared, so what
	// it was is what it stays.
	replaced := client.writes[2].body
	if replaced["is_alphanumeric"] != false || replaced["url_template"] != "https://jira.example.com/browse/<num>" {
		t.Errorf("body = %+v, want the declaration over what was reported", replaced)
	}

	output := out.String()
	for _, want := range []string{
		"~ autolinks: [",
		`          key_prefix:   "JIRA-"`,
		`        ~ url_template: "https://old.example.com/<num>" -> "https://jira.example.com/browse/<num>"`,
		`        + key_prefix:   "ADD-"`,
		`        - key_prefix:      "GONE-"`,
		"Apply complete. 1 created, 1 changed, 1 deleted.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}
