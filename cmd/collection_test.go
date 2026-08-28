package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// collectionClient answers the list endpoint with canned variables and records
// every write, so that the order the operations go out in can be asserted.
type collectionClient struct {
	variables []map[string]any
	calls     []string
	listings  []string
	failOn    string
}

func (c *collectionClient) DoWithContext(_ context.Context, method, path string, body io.Reader, response any) error {
	call := method + " " + path
	if strings.HasPrefix(path, "repos/kota65535/ghs/actions/variables?") {
		// Reads are recorded apart from writes: whether a collection was
		// looked at is its own question from what was done about it.
		c.listings = append(c.listings, call)
		return json.Unmarshal(c.listing(), response)
	}

	if c.failOn != "" && strings.Contains(call, c.failOn) {
		return fmt.Errorf("api refused %s", call)
	}

	var sent map[string]any
	if body != nil {
		if err := json.NewDecoder(body).Decode(&sent); err != nil {
			return err
		}
	}
	c.calls = append(c.calls, fmt.Sprintf("%s %v", call, sent[elementNameForTest]))
	return nil
}

// elementNameForTest is the field the recorded calls are labelled by.
const elementNameForTest = "name"

func (c *collectionClient) listing() []byte {
	page := map[string]any{"total_count": len(c.variables), "variables": c.variables}
	b, _ := json.Marshal(page)
	return b
}

func TestPlanAndApplyOverACollection(t *testing.T) {
	client := &collectionClient{variables: []map[string]any{
		{"name": "KEEP", "value": "same"},
		{"name": "CHANGE", "value": "old"},
		{"name": "GONE", "value": "x"},
	}}
	cfg := mustConfig(t, `
variables:
  - name: KEEP
    value: same
  - name: CHANGE
    value: new
  - name: ADD
    value: fresh
`)

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resource plans, want 1", len(resources))
	}

	var out bytes.Buffer
	if err := apply(context.Background(), &out, plans{repo: testRepo, client: client, resources: resources}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Create first and delete last: a failure part way through then leaves a
	// spare element rather than a missing one.
	want := []string{
		"POST repos/kota65535/ghs/actions/variables ADD",
		"PATCH repos/kota65535/ghs/actions/variables/CHANGE CHANGE",
		"DELETE repos/kota65535/ghs/actions/variables/GONE <nil>",
	}
	if len(client.calls) != len(want) {
		t.Fatalf("made %d calls, want %d: %v", len(client.calls), len(want), client.calls)
	}
	for i := range want {
		if client.calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, client.calls[i], want[i])
		}
	}

	output := out.String()
	for _, want := range []string{
		"~ variables: [",
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

func TestApplyStopsAtTheFirstCollectionFailure(t *testing.T) {
	client := &collectionClient{
		variables: []map[string]any{{"name": "GONE", "value": "x"}},
		failOn:    "POST",
	}
	cfg := mustConfig(t, "variables:\n  - name: ADD\n    value: fresh\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}

	var out bytes.Buffer
	err = apply(context.Background(), &out, plans{repo: testRepo, client: client, resources: resources})
	if err == nil {
		t.Fatal("apply succeeded, want the API failure reported")
	}
	// The delete must not have gone out: a failed create leaves the settings
	// further from the declaration, not nearer to it.
	if len(client.calls) != 0 {
		t.Errorf("made %v, want nothing after the failure", client.calls)
	}
}

func TestEmptyCollectionDeletesEverything(t *testing.T) {
	client := &collectionClient{variables: []map[string]any{
		{"name": "A", "value": "1"},
		{"name": "B", "value": "2"},
	}}
	cfg := mustConfig(t, "variables: []\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}

	var out bytes.Buffer
	if err := apply(context.Background(), &out, plans{repo: testRepo, client: client, resources: resources}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(client.calls) != 2 {
		t.Fatalf("made %d calls, want both elements deleted: %v", len(client.calls), client.calls)
	}
	for _, call := range client.calls {
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
	// there, because nothing it learned could lead to an action it is allowed
	// to take.
	client := &collectionClient{variables: []map[string]any{{"name": "A", "value": "1"}}}
	cfg := mustConfig(t, "repository:\n  has_issues: true\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}

	for _, r := range resources {
		if r.collection != nil {
			t.Errorf("planned collection %q, want only the declared resource", r.name)
		}
	}
	if len(client.listings) != 0 {
		t.Errorf("listed %v, want no collection read at all", client.listings)
	}
}

func TestElementOperationsCountsAnElementOnce(t *testing.T) {
	// Several fields of one element differ, and it is still a single update.
	client := &collectionClient{variables: []map[string]any{{"name": "A", "value": "old"}}}
	cfg := mustConfig(t, "variables:\n  - name: A\n    value: new\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}

	created, updated, deleted := elementOperations(resources[0].changes)
	if len(created) != 0 || len(deleted) != 0 {
		t.Errorf("created/deleted = %v/%v, want neither", created, deleted)
	}
	if len(updated) != 1 || updated[0] != "A" {
		t.Errorf("updated = %v, want [A]", updated)
	}
}
