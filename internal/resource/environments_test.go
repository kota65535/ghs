package resource

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestEnvironmentsFetchAllUnfoldsProtectionRules(t *testing.T) {
	// A reported environment does not look like the request that declares one:
	// wait_timer, reviewers and prevent_self_review come back folded into a
	// protection_rules array. Undoing that keeps the comparison in diff plain.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/environments": `{"total_count": 1, "environments": [{
			"id": 161088068,
			"name": "production",
			"protection_rules": [
				{"id": 3515, "node_id": "MDQ6R2F0ZTM1MTU=", "type": "wait_timer", "wait_timer": 30},
				{"id": 3755, "node_id": "MDQ6R2F0ZTM3NTU=", "type": "required_reviewers",
				 "prevent_self_review": true,
				 "reviewers": [{"type": "User", "reviewer": {"id": 1, "login": "octocat"}}]},
				{"id": 3756, "node_id": "MDQ6R2F0ZTM3NTY=", "type": "branch_policy"}
			],
			"deployment_branch_policy": {"protected_branches": true, "custom_branch_policies": false}
		}]}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Environments{}.FetchAll(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	production, ok := current["production"]
	if !ok {
		t.Fatalf("production is missing: %+v", current)
	}

	if production["wait_timer"] != float64(30) {
		t.Errorf("wait_timer = %v, want 30", production["wait_timer"])
	}
	if production["prevent_self_review"] != true {
		t.Errorf("prevent_self_review = %v, want true", production["prevent_self_review"])
	}

	// A reviewer is declared by type and id, while the response nests the
	// whole user under a reviewer key.
	want := []any{map[string]any{"type": "User", "id": float64(1)}}
	if !reflect.DeepEqual(production["reviewers"], want) {
		t.Errorf("reviewers = %+v, want %+v", production["reviewers"], want)
	}

	// deployment_branch_policy is reported the way it is declared.
	policy, ok := production["deployment_branch_policy"].(map[string]any)
	if !ok || policy["protected_branches"] != true {
		t.Errorf("deployment_branch_policy = %+v, want it carried over", production["deployment_branch_policy"])
	}

	// The protection rules themselves are not part of the declared shape, so
	// they must not survive the rewrite and show up as an undeclared field.
	if _, present := production["protection_rules"]; present {
		t.Error("protection_rules survived, want the request shape only")
	}
}

func TestEnvironmentsFetchAllReportsRulesThatAreOffAsTheirOffValue(t *testing.T) {
	// A protection rule that is off is not reported at all. Left as absent,
	// declaring the value GitHub is already at -- no delay, written as
	// wait_timer: 0 -- would read as a change to a field the response does not
	// have, and applying it would never make the difference go away.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/environments": `{"total_count": 1, "environments": [{"id": 1, "name": "staging"}]}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Environments{}.FetchAll(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	staging, ok := current["staging"]
	if !ok {
		t.Fatalf("staging is missing: %+v", current)
	}
	if staging["wait_timer"] != float64(0) {
		t.Errorf("wait_timer = %v, want 0", staging["wait_timer"])
	}
	if staging["prevent_self_review"] != false {
		t.Errorf("prevent_self_review = %v, want false", staging["prevent_self_review"])
	}
	if reviewers, ok := staging["reviewers"].([]any); !ok || len(reviewers) != 0 {
		t.Errorf("reviewers = %+v, want an empty list", staging["reviewers"])
	}
}

func TestEnvironmentsCreateAndUpdateSendTheSameRequest(t *testing.T) {
	// Creating and settling an environment are one operation: PUT takes the
	// name in the path and does either.
	repo := Repo{Owner: "kota65535", Name: "ghs"}
	desired := map[string]any{"name": "production", "wait_timer": float64(30)}

	for _, tc := range []struct {
		what string
		call func(client Client) error
	}{
		{"create", func(client Client) error {
			return Environments{}.Create(context.Background(), client, repo, desired)
		}},
		{"update", func(client Client) error {
			return Environments{}.Update(context.Background(), client, repo, map[string]any{"name": "production"}, desired)
		}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			rec := newRecorder(t, nil)
			srv := rec.server()
			defer srv.Close()

			if err := tc.call(newTestClient(t, srv)); err != nil {
				t.Fatalf("%s: %v", tc.what, err)
			}

			got := rec.only()
			if got.method != http.MethodPut || got.path != "/repos/kota65535/ghs/environments/production" {
				t.Errorf("request = %s %s, want PUT on the environment", got.method, got.path)
			}
			if got.body["wait_timer"] != float64(30) {
				t.Errorf("body = %+v, want the declared fields", got.body)
			}
			// The name travels in the path, so sending it in the body would
			// declare a field the request body has no such thing as.
			if _, sent := got.body[elementName]; sent {
				t.Errorf("body = %+v, want the name left out", got.body)
			}
		})
	}
}

func TestEnvironmentsFetchAllReadsTheVariablesOfEachEnvironment(t *testing.T) {
	// The variables are listed separately from the environment but declared
	// inside it, so they are put where the settings file expects them.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/environments": `{"total_count": 1, "environments": [{"id": 1, "name": "production"}]}`,
		"GET /repos/kota65535/ghs/environments/production/variables": `{"total_count": 1, "variables": [
			{"name": "DEPLOY_REGION", "value": "ap-northeast-1", "created_at": "2026-01-01T00:00:00Z"}
		]}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Environments{}.FetchAll(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	variables, ok := current["production"]["variables"].([]any)
	if !ok {
		t.Fatalf("variables = %+v, want a sequence", current["production"]["variables"])
	}
	if len(variables) != 1 {
		t.Fatalf("got %d variables, want 1", len(variables))
	}
	if variables[0].(map[string]any)["value"] != "ap-northeast-1" {
		t.Errorf("variables[0] = %+v, want the reported value", variables[0])
	}
}

func TestEnvironmentsFetchAllReportsNoVariablesAsAnEmptySequence(t *testing.T) {
	// An environment with no variables must not read as one whose variables
	// are unknown: declaring none has to compare equal to having none.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/environments":                   `{"total_count": 1, "environments": [{"id": 1, "name": "staging"}]}`,
		"GET /repos/kota65535/ghs/environments/staging/variables": `{"total_count": 0, "variables": []}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Environments{}.FetchAll(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	variables, ok := current["staging"]["variables"].([]any)
	if !ok || len(variables) != 0 {
		t.Errorf("variables = %+v, want an empty sequence", current["staging"]["variables"])
	}
}

func TestEnvironmentsSettleVariablesInOrder(t *testing.T) {
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	current := map[string]any{
		"name": "production",
		"variables": []any{
			map[string]any{"name": "KEEP", "value": "same"},
			map[string]any{"name": "CHANGE", "value": "old"},
			map[string]any{"name": "GONE", "value": "x"},
		},
	}
	desired := map[string]any{
		"name":       "production",
		"wait_timer": float64(30),
		"variables": []any{
			map[string]any{"name": "KEEP", "value": "same"},
			map[string]any{"name": "CHANGE", "value": "new"},
			map[string]any{"name": "ADD", "value": "fresh"},
		},
	}

	err := Environments{}.Update(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}, current, desired)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The environment is settled first -- a variable cannot go into one that
	// does not exist -- and then its variables, created before deleted so that
	// a failure part way leaves a spare rather than a gap.
	want := []string{
		"PUT /repos/kota65535/ghs/environments/production",
		"POST /repos/kota65535/ghs/environments/production/variables",
		"PATCH /repos/kota65535/ghs/environments/production/variables/CHANGE",
		"DELETE /repos/kota65535/ghs/environments/production/variables/GONE",
	}
	if len(rec.requests) != len(want) {
		t.Fatalf("made %d requests, want %d: %+v", len(rec.requests), len(want), rec.requests)
	}
	for i, w := range want {
		got := rec.requests[i].method + " " + rec.requests[i].path
		if got != w {
			t.Errorf("request %d = %q, want %q", i, got, w)
		}
	}

	// The variables are not a field of the environment's own request body.
	if _, sent := rec.requests[0].body["variables"]; sent {
		t.Errorf("body = %+v, want the variables left out", rec.requests[0].body)
	}
	if rec.requests[0].body["wait_timer"] != float64(30) {
		t.Errorf("body = %+v, want the declared fields", rec.requests[0].body)
	}
}

func TestEnvironmentsLeaveUndeclaredVariablesAlone(t *testing.T) {
	// Not declaring the variables means not managing them, so nothing about
	// them is sent -- least of all a delete.
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	current := map[string]any{
		"name":      "production",
		"variables": []any{map[string]any{"name": "A", "value": "1"}},
	}
	desired := map[string]any{"name": "production", "wait_timer": float64(0)}

	err := Environments{}.Update(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}, current, desired)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := rec.only()
	if got.method != http.MethodPut || got.path != "/repos/kota65535/ghs/environments/production" {
		t.Errorf("request = %s %s, want only the environment settled", got.method, got.path)
	}
}

func TestEnvironmentsCreateSendsItsVariablesToo(t *testing.T) {
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	desired := map[string]any{
		"name":      "production",
		"variables": []any{map[string]any{"name": "A", "value": "1"}},
	}

	err := Environments{}.Create(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}, desired)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(rec.requests) != 2 {
		t.Fatalf("made %d requests, want the environment and its variable: %+v", len(rec.requests), rec.requests)
	}
	if rec.requests[1].path != "/repos/kota65535/ghs/environments/production/variables" {
		t.Errorf("second request = %s, want the variable created", rec.requests[1].path)
	}
	if rec.requests[1].body["name"] != "A" {
		t.Errorf("body = %+v, want the variable's name sent, since it is what the API keys on", rec.requests[1].body)
	}
}

func TestEnvironmentsDelete(t *testing.T) {
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := Environments{}.Delete(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"},
		map[string]any{"name": "legacy"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got := rec.only()
	if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/environments/legacy" {
		t.Errorf("request = %s %s, want DELETE on the environment", got.method, got.path)
	}
}

func TestEnvironmentNamesAreEscapedInPaths(t *testing.T) {
	// Environment names allow characters that mean something in a URL.
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := Environments{}.Delete(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"},
		map[string]any{"name": "review/pr-1"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got := rec.only()
	if got.path != "/repos/kota65535/ghs/environments/review%2Fpr-1" {
		t.Errorf("path = %q, want the name escaped", got.path)
	}
}
