package resource

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var environmentsNode = schema.Node{
	Kind:    schema.KindCollection,
	Segment: "environments",
	Method:  http.MethodPut,
}

func environmentsPath() Path { return At(testRepo).Child("environments") }

func TestEnvironmentsFetchAllUnfoldsProtectionRules(t *testing.T) {
	// A reported environment does not look like the request that declares one:
	// wait_timer, reviewers and prevent_self_review come back folded into a
	// protection_rules array. Undoing that keeps the comparison in diff plain.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/environments": `{"total_count": 1, "environments": [{
			"id": 161088068,
			"name": "production",
			"protection_rules": [
				{"id": 3515, "type": "wait_timer", "wait_timer": 30},
				{"id": 3755, "type": "required_reviewers",
				 "prevent_self_review": true,
				 "reviewers": [{"type": "User", "reviewer": {"id": 1, "login": "octocat"}}]},
				{"id": 3756, "type": "branch_policy"}
			],
			"deployment_branch_policy": {"protected_branches": true, "custom_branch_policies": false}
		}]}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Environments{}.FetchAll(context.Background(), newTestClient(t, srv), environmentsNode, environmentsPath())
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

	current, err := Environments{}.FetchAll(context.Background(), newTestClient(t, srv), environmentsNode, environmentsPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	staging := current["staging"]
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
	desired := map[string]any{"name": "production", "wait_timer": float64(30)}

	for _, tc := range []struct {
		what string
		call func(client Client) error
	}{
		{"create", func(client Client) error {
			return Environments{}.Create(context.Background(), client, environmentsNode, environmentsPath(), desired)
		}},
		{"update", func(client Client) error {
			return Environments{}.Update(context.Background(), client, environmentsNode, environmentsPath(),
				map[string]any{"name": "production"}, desired)
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
			// declare a field the request has no such thing as.
			if _, sent := got.body[elementName]; sent {
				t.Errorf("body = %+v, want the name left out", got.body)
			}
		})
	}
}

func TestEnvironmentsDelete(t *testing.T) {
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := Environments{}.Delete(context.Background(), newTestClient(t, srv), environmentsNode, environmentsPath(),
		map[string]any{"name": "legacy"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got := rec.only()
	if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/environments/legacy" {
		t.Errorf("request = %s %s, want DELETE on the environment", got.method, got.path)
	}
}

func TestEnvironmentPathIsWhereItsVariablesHang(t *testing.T) {
	// What an element owns is addressed under that element, which is how the
	// variables of an environment are reached.
	path, err := Environments{}.ElementPath(environmentsPath(), map[string]any{"name": "review/pr-1"})
	if err != nil {
		t.Fatalf("ElementPath: %v", err)
	}

	got := path.Child("variables").String()
	if got != "repos/kota65535/ghs/environments/review%2Fpr-1/variables" {
		t.Errorf("path = %q, want the name escaped within it", got)
	}
}
