package resource

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var branchPoliciesNode = schema.Node{
	Kind:    schema.KindCollection,
	Segment: "deployment-branch-policies",
	Method:  http.MethodPost,
}

// branchPoliciesPath is where the policies of one environment sit, which is
// below the environment rather than below the repository.
func branchPoliciesPath() Path {
	return At(testRepo).Child("environments").Element("production").Child("deployment-branch-policies")
}

func TestDeploymentBranchPoliciesAreReadUnderTheirEnvironment(t *testing.T) {
	// The listing wraps the policies under branch_policies, which is not the
	// name of the endpoint they are read from.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/environments/production/deployment-branch-policies": `{
			"total_count": 2,
			"branch_policies": [
				{"id": 361471, "name": "release/*", "type": "branch"},
				{"id": 361472, "name": "v1.*", "type": "tag"}
			]
		}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := DeploymentBranchPolicies{}.FetchAll(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if got := rec.only().path; got != "/repos/kota65535/ghs/environments/production/deployment-branch-policies" {
		t.Errorf("path = %q, want the policies of the environment", got)
	}
	if !reflect.DeepEqual(sortedNames(current), []string{"release/*", "v1.*"}) {
		t.Errorf("policies = %+v, want both keyed by their pattern", current)
	}
	// The id is kept because it is what addresses the policy later.
	if current["release/*"][idField] != float64(361471) {
		t.Errorf("id = %v, want 361471", current["release/*"][idField])
	}
}

func TestDeploymentBranchPoliciesOfAnEnvironmentThatDisallowsThemAreEmpty(t *testing.T) {
	// An environment whose deployment_branch_policy is null or protected has no
	// such endpoint, and GitHub answers 404. Reporting that as a failure would
	// stop a plan that turns custom branch policies on and declares the policies
	// in the same file, which is the natural way to write one.
	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/environments/production/deployment-branch-policies", http.StatusNotFound)
	srv := rec.server()
	defer srv.Close()

	current, err := DeploymentBranchPolicies{}.FetchAll(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(current) != 0 {
		t.Errorf("policies = %+v, want none", current)
	}
}

func TestDeploymentBranchPoliciesReportRealFailures(t *testing.T) {
	// A 403 means the token cannot see the environment, which is a different
	// thing from the environment having no policies.
	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/environments/production/deployment-branch-policies", http.StatusForbidden)
	srv := rec.server()
	defer srv.Close()

	if _, err := (DeploymentBranchPolicies{}).FetchAll(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath()); err == nil {
		t.Fatal("FetchAll succeeded, want the failure reported")
	}
}

func TestDeploymentBranchPoliciesAreAddressedByTheIDGitHubIssued(t *testing.T) {
	desired := map[string]any{"name": "release/*", "type": "branch"}
	current := map[string]any{"name": "release/*", "type": "branch", idField: float64(361471)}

	t.Run("create posts to the collection", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (DeploymentBranchPolicies{}).Create(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath(), desired); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPost || got.path != "/repos/kota65535/ghs/environments/production/deployment-branch-policies" {
			t.Errorf("request = %s %s, want POST on the collection", got.method, got.path)
		}
		// The pattern travels in the body here, unlike an environment's name.
		if got.body["name"] != "release/*" || got.body["type"] != "branch" {
			t.Errorf("body = %+v, want the declared pattern and type", got.body)
		}
	})

	t.Run("delete removes the id", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (DeploymentBranchPolicies{}).Delete(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath(), current); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/environments/production/deployment-branch-policies/361471" {
			t.Errorf("request = %s %s, want DELETE on the policy id", got.method, got.path)
		}
	})

	t.Run("delete without an id fails before any request", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		err := DeploymentBranchPolicies{}.Delete(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath(),
			map[string]any{"name": "release/*"})
		if err == nil {
			t.Fatal("Delete succeeded, want an error")
		}
		if len(rec.requests) != 0 {
			t.Errorf("made %d requests, want none", len(rec.requests))
		}
	})
}

func TestDeploymentBranchPolicyTypeIsChangedByReplacement(t *testing.T) {
	// A policy is matched by its pattern, so an update means the type changed,
	// and the update endpoint takes the pattern alone. Replacing it is the only
	// way to leave the environment saying what the file says.
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := DeploymentBranchPolicies{}.Update(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath(),
		map[string]any{"name": "v1.*", "type": "branch", idField: float64(361472)},
		map[string]any{"name": "v1.*", "type": "tag"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	want := []string{
		"DELETE /repos/kota65535/ghs/environments/production/deployment-branch-policies/361472",
		"POST /repos/kota65535/ghs/environments/production/deployment-branch-policies",
	}
	if got := rec.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestDeploymentBranchPolicyRefusedByTheEnvironmentSaysWhy(t *testing.T) {
	// GitHub refuses a policy on an environment that does not allow custom
	// branch policies, and says only that there is nothing there. The setting
	// the write depends on is worth naming.
	rec := newRecorder(t, nil).fails("POST /repos/kota65535/ghs/environments/production/deployment-branch-policies", http.StatusNotFound)
	srv := rec.server()
	defer srv.Close()

	err := DeploymentBranchPolicies{}.Create(context.Background(), newTestClient(t, srv), branchPoliciesNode, branchPoliciesPath(),
		map[string]any{"name": "release/*", "type": "branch"})
	if err == nil {
		t.Fatal("Create succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "custom_branch_policies") {
		t.Errorf("error = %q, want the environment setting named", err)
	}
}

func sortedNames(elements map[string]map[string]any) []string {
	names := make([]string, 0, len(elements))
	for name := range elements {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
