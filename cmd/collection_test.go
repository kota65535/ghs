package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
	if err := apply(context.Background(), &out, p); err != nil {
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
		"Plan: 1 to create, 1 to change, 1 to delete.",
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
	if err := apply(context.Background(), &out, p); err != nil {
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
	if err := apply(context.Background(), &out, p); err != nil {
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
	if err := apply(context.Background(), &out, p); err != nil {
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
	if err := apply(context.Background(), &out, p); err == nil {
		t.Fatal("apply succeeded, want the API failure reported")
	}
	// The delete must not have gone out: a failed create leaves the settings
	// further from the declaration, not nearer to it.
	if len(client.writes) != 0 {
		t.Errorf("made %v, want nothing after the failure", client.calls())
	}
}
