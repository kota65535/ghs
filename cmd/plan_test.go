package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/config"
	"github.com/kota65535/ghs/internal/diff"
	"github.com/kota65535/ghs/internal/resource"
)

var testRepo = resource.Repo{Owner: "kota65535", Name: "ghs"}

// fakeClient answers reads from a table keyed by path and records every write,
// so that a test can assert on which endpoints a walk of the settings file
// touched and in what order.
type fakeClient struct {
	reads  map[string]string
	writes []write
	failOn string
}

type write struct {
	method string
	path   string
	body   map[string]any
}

func (f *fakeClient) DoWithContext(_ context.Context, method, path string, body io.Reader, out any) error {
	// Reads answer from the table; the query string is not part of the key.
	bare := strings.SplitN(path, "?", 2)[0]

	if method == http.MethodGet {
		reply, ok := f.reads[bare]
		if !ok {
			reply = "{}"
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal([]byte(reply), out)
	}

	if f.failOn != "" && strings.Contains(bare, f.failOn) {
		return fmt.Errorf("api refused %s %s", method, bare)
	}

	recorded := write{method: method, path: bare}
	if body != nil {
		if err := json.NewDecoder(body).Decode(&recorded.body); err != nil {
			return err
		}
	}
	f.writes = append(f.writes, recorded)
	return nil
}

// calls lists the writes as "METHOD path", for asserting on their order.
func (f *fakeClient) calls() []string {
	out := make([]string, 0, len(f.writes))
	for _, w := range f.writes {
		out = append(out, w.method+" "+w.path)
	}
	return out
}

func mustConfig(t *testing.T, yaml string) *config.Declaration {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

// planFor walks the settings file against the client and returns what apply
// would do.
func planFor(t *testing.T, client resource.Client, yaml string) plans {
	t.Helper()

	cfg := mustConfig(t, yaml)
	plan, err := planNode(context.Background(), client, "", cfg, resource.At(testRepo), true)
	if err != nil {
		t.Fatalf("planNode: %v", err)
	}
	return plans{repo: testRepo, client: client, declared: cfg, plan: plan}
}

func TestPlanReadsOnlyWhatIsDeclared(t *testing.T) {
	// A node nobody declared anything for is not read, so a settings file that
	// says nothing about Actions costs no requests about it.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs": `{"has_issues": false}`,
	}}

	p := planFor(t, client, "has_issues: true\n")

	if got := p.plan.Summarize(); got.Changed != 1 {
		t.Errorf("summary = %+v, want one object changed", got)
	}
	if len(p.plan.Children) != 0 {
		t.Errorf("planned %d children, want none declared", len(p.plan.Children))
	}
}

func TestPlanWalksDownToNestedNodes(t *testing.T) {
	// Each key names a path segment, so the walk reaches the endpoint the key
	// stands for.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs/actions/permissions":          `{"enabled": true, "allowed_actions": "all"}`,
		"repos/kota65535/ghs/actions/permissions/workflow": `{"default_workflow_permissions": "read"}`,
	}}

	p := planFor(t, client, `
actions:
  permissions:
    allowed_actions: local_only
    workflow:
      default_workflow_permissions: write
`)

	var buf bytes.Buffer
	if err := diff.Render(&buf, p.plan, diff.FormatText); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"~ actions:",
		"    ~ permissions:",
		`        ~ allowed_actions: "all" -> "local_only"`,
		"        ~ workflow:",
		`            ~ default_workflow_permissions: "read" -> "write"`,
		"Plan: 2 to change.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestApplyWritesEachNodeToItsOwnEndpoint(t *testing.T) {
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs":                              `{"has_issues": false}`,
		"repos/kota65535/ghs/actions/permissions":          `{"enabled": true}`,
		"repos/kota65535/ghs/actions/permissions/workflow": `{"default_workflow_permissions": "read"}`,
	}}

	p := planFor(t, client, `
has_issues: true

actions:
  permissions:
    enabled: false
    workflow:
      default_workflow_permissions: write
`)

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p); err != nil {
		t.Fatalf("apply: %v", err)
	}

	want := []string{
		"PATCH repos/kota65535/ghs",
		"PUT repos/kota65535/ghs/actions/permissions",
		"PUT repos/kota65535/ghs/actions/permissions/workflow",
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

	// Each endpoint is sent its own fields and no others.
	if _, sent := client.writes[0].body["enabled"]; sent {
		t.Errorf("repository body = %+v, want only its own fields", client.writes[0].body)
	}
	if !strings.Contains(out.String(), "Apply complete. 3 changed.") {
		t.Errorf("output = %q, want a completion summary", out.String())
	}
}

func TestApplySkipsNodesThatMatch(t *testing.T) {
	// Nothing differs, so nothing is written -- and the settings that are
	// already as declared are not rewritten on the way past.
	client := &fakeClient{reads: map[string]string{
		"repos/kota65535/ghs":                     `{"has_issues": true}`,
		"repos/kota65535/ghs/actions/permissions": `{"enabled": true}`,
	}}

	p := planFor(t, client, "has_issues: true\n\nactions:\n  permissions:\n    enabled: true\n")

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(client.writes) != 0 {
		t.Errorf("made %v, want nothing written", client.calls())
	}
	if !strings.Contains(out.String(), "No changes") {
		t.Errorf("output = %q, want a no-changes message", out.String())
	}
}

func TestApplyReportsAPIFailure(t *testing.T) {
	client := &fakeClient{
		reads:  map[string]string{"repos/kota65535/ghs": `{"has_issues": false}`},
		failOn: "repos/kota65535/ghs",
	}

	p := planFor(t, client, "has_issues: true\n")

	var out bytes.Buffer
	if err := apply(context.Background(), &out, p); err == nil {
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
