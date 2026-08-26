package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/config"
	"github.com/kota65535/ghs/internal/resource"
)

// fakeClient stands in for the REST client, recording what was sent and
// replying with a canned repository.
type fakeClient struct {
	current  map[string]any
	patched  []map[string]any
	patchErr error
}

func (f *fakeClient) DoWithContext(_ context.Context, method, _ string, body io.Reader, response any) error {
	switch method {
	case http.MethodGet:
		b, err := json.Marshal(f.current)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, response)
	case http.MethodPatch:
		if f.patchErr != nil {
			return f.patchErr
		}
		var sent map[string]any
		if err := json.NewDecoder(body).Decode(&sent); err != nil {
			return err
		}
		f.patched = append(f.patched, sent)
		return nil
	default:
		return nil
	}
}

func mustConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml), config.Options{})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

var testRepo = resource.Repo{Owner: "kota65535", Name: "ghs"}

func TestComputePlansFindsDifferences(t *testing.T) {
	client := &fakeClient{current: map[string]any{"has_issues": true, "allow_auto_merge": false}}
	cfg := mustConfig(t, "repository:\n  has_issues: true\n  allow_auto_merge: true\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resource plans, want 1", len(resources))
	}
	if len(resources[0].changes) != 1 {
		t.Fatalf("got %d changes, want only the differing field: %+v", len(resources[0].changes), resources[0].changes)
	}
	if resources[0].changes[0].Path != "repository.allow_auto_merge" {
		t.Errorf("Path = %q, want repository.allow_auto_merge", resources[0].changes[0].Path)
	}
}

func TestApplySendsWholeDeclarationOnce(t *testing.T) {
	client := &fakeClient{current: map[string]any{"has_issues": true, "allow_auto_merge": false}}
	cfg := mustConfig(t, "repository:\n  has_issues: true\n  allow_auto_merge: true\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}

	var out bytes.Buffer
	if err := apply(context.Background(), &out, plans{repo: testRepo, client: client, resources: resources}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(client.patched) != 1 {
		t.Fatalf("sent %d requests, want 1", len(client.patched))
	}
	sent := client.patched[0]
	if len(sent) != 2 || sent["has_issues"] != true || sent["allow_auto_merge"] != true {
		t.Errorf("sent = %+v, want the whole declaration", sent)
	}
	if !strings.Contains(out.String(), "Apply complete. 1 field changed.") {
		t.Errorf("output = %q, want a completion summary", out.String())
	}
}

func TestApplyDoesNothingWithoutChanges(t *testing.T) {
	client := &fakeClient{current: map[string]any{"has_issues": true}}
	cfg := mustConfig(t, "repository:\n  has_issues: true\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}

	var out bytes.Buffer
	if err := apply(context.Background(), &out, plans{repo: testRepo, client: client, resources: resources}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(client.patched) != 0 {
		t.Errorf("sent %d requests, want none when nothing differs", len(client.patched))
	}
	if !strings.Contains(out.String(), "No changes") {
		t.Errorf("output = %q, want a no-changes message", out.String())
	}
}

func TestApplyReportsAPIFailure(t *testing.T) {
	client := &fakeClient{
		current:  map[string]any{"has_issues": false},
		patchErr: io.ErrUnexpectedEOF,
	}
	cfg := mustConfig(t, "repository:\n  has_issues: true\n")

	resources, err := computePlans(context.Background(), client, testRepo, cfg)
	if err != nil {
		t.Fatalf("computePlans: %v", err)
	}

	var out bytes.Buffer
	if err := apply(context.Background(), &out, plans{repo: testRepo, client: client, resources: resources}); err == nil {
		t.Fatal("apply succeeded, want the API failure reported")
	}
}

func TestResolveRepo(t *testing.T) {
	repo, err := resolveRepo("kota65535/ghs")
	if err != nil {
		t.Fatalf("resolveRepo: %v", err)
	}
	if repo != testRepo {
		t.Errorf("repo = %+v, want %+v", repo, testRepo)
	}

	for _, bad := range []string{"ghs", "/ghs", "kota65535/"} {
		if _, err := resolveRepo(bad); err == nil {
			t.Errorf("resolveRepo(%q) succeeded, want an error", bad)
		}
	}
}
