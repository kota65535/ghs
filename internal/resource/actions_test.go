package resource

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

func TestActionsFetchGathersEveryEndpointIntoOneResource(t *testing.T) {
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/actions/permissions":                              `{"enabled": true, "allowed_actions": "selected", "sha_pinning_required": false}`,
		"GET /repos/kota65535/ghs/actions/permissions/workflow":                     `{"default_workflow_permissions": "read", "can_approve_pull_request_reviews": false}`,
		"GET /repos/kota65535/ghs/actions/permissions/fork-pr-contributor-approval": `{"approval_policy": "first_time_contributors"}`,
		"GET /repos/kota65535/ghs/actions/permissions/selected-actions":             `{"github_owned_allowed": true, "verified_allowed": false, "patterns_allowed": ["octo/*"]}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Actions{}.Fetch(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	for field, want := range map[string]any{
		"enabled":                          true,
		"allowed_actions":                  "selected",
		"default_workflow_permissions":     "read",
		"can_approve_pull_request_reviews": false,
		"approval_policy":                  "first_time_contributors",
		"github_owned_allowed":             true,
	} {
		if current[field] != want {
			t.Errorf("%s = %v, want %v", field, current[field], want)
		}
	}
}

func TestActionsFetchLeavesOutSettingsThatDoNotApply(t *testing.T) {
	// The allowed actions are listed only while the policy is to select them,
	// and GitHub answers 409 otherwise. That is not a failure: there is simply
	// nothing there, and a field declared against it reads as a change like
	// any field a response omits.
	for _, status := range []int{http.StatusConflict, http.StatusUnprocessableEntity} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/selected-actions") {
				w.WriteHeader(status)
				io.WriteString(w, `{"message": "does not apply"}`)
				return
			}
			io.WriteString(w, `{"enabled": true}`)
		}))

		current, err := Actions{}.Fetch(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
		srv.Close()

		if err != nil {
			t.Errorf("status %d: Fetch: %v", status, err)
			continue
		}
		if current["enabled"] != true {
			t.Errorf("status %d: enabled = %v, want the other endpoints still read", status, current["enabled"])
		}
		if _, present := current["github_owned_allowed"]; present {
			t.Errorf("status %d: got %+v, want the inapplicable fields left out", status, current)
		}
	}
}

func TestActionsFetchReportsRealFailures(t *testing.T) {
	// A conditional endpoint answering 403 means the token cannot see it,
	// which is a different thing from the setting not applying.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/selected-actions") {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"message": "Resource not accessible by integration"}`)
			return
		}
		io.WriteString(w, `{"enabled": true}`)
	}))
	defer srv.Close()

	if _, err := (Actions{}).Fetch(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}); err == nil {
		t.Fatal("Fetch succeeded, want the failure reported")
	}
}

func TestActionsApplyOnlyCallsEndpointsWithADeclaredField(t *testing.T) {
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	desired := map[string]any{
		"allowed_actions":              "selected",
		"github_owned_allowed":         true,
		"default_workflow_permissions": "read",
	}

	err := Actions{}.Apply(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}, desired)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Nothing was declared for the fork approval, so it is not written. The
	// general permissions go first: which actions may be selected only means
	// something once the policy says to select them.
	want := []string{
		"/repos/kota65535/ghs/actions/permissions",
		"/repos/kota65535/ghs/actions/permissions/workflow",
		"/repos/kota65535/ghs/actions/permissions/selected-actions",
	}
	if len(rec.requests) != len(want) {
		t.Fatalf("made %d requests, want %d: %+v", len(rec.requests), len(want), rec.requests)
	}
	for i, path := range want {
		if rec.requests[i].method != http.MethodPut || rec.requests[i].path != path {
			t.Errorf("request %d = %s %s, want PUT %s", i, rec.requests[i].method, rec.requests[i].path, path)
		}
	}

	// Each endpoint is sent its own fields and no others.
	if _, sent := rec.requests[0].body["default_workflow_permissions"]; sent {
		t.Errorf("body = %+v, want only the permissions fields", rec.requests[0].body)
	}
	if rec.requests[2].body["github_owned_allowed"] != true {
		t.Errorf("body = %+v, want the selected-actions fields", rec.requests[2].body)
	}
}

func TestActionsApplyWritesNothingWhenNothingIsDeclared(t *testing.T) {
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := Actions{}.Apply(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}, map[string]any{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(rec.requests) != 0 {
		t.Errorf("made %v, want no request", rec.requests)
	}
}

func TestEveryGeneratedActionsFieldIsWrittenSomewhere(t *testing.T) {
	// The field definitions are generated and the endpoints are written by
	// hand, so a field GitHub adds would otherwise be accepted in the settings
	// file and then quietly never sent.
	resource, ok := schema.Lookup("actions")
	if !ok {
		t.Fatal("actions is not registered; run go generate ./...")
	}

	written := map[string]bool{}
	for _, endpoint := range actionsEndpoints {
		for _, field := range endpoint.fields {
			if written[field] {
				t.Errorf("%s is written by more than one endpoint", field)
			}
			written[field] = true
		}
	}

	for field := range resource.Fields {
		if !written[field] {
			t.Errorf("%s is a generated field of actions but no endpoint writes it", field)
		}
	}
	for field := range written {
		if _, generated := resource.Fields[field]; !generated {
			t.Errorf("%s is written but is not a field of actions", field)
		}
	}
}
