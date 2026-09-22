package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kota65535/ghs/internal/schema"
)

// DeploymentBranchPolicies manages the branch and tag patterns an environment
// may be deployed from.
//
// It departs from the general treatment on three counts: the listing wraps the
// policies under a key of its own rather than under the name of the endpoint,
// GitHub addresses a policy by an id it issues, and the endpoint exists only
// while the environment says its deployment branch policy is a custom one.
type DeploymentBranchPolicies struct{}

// branchPoliciesKey is what the listing wraps its items in. The endpoint is
// deployment-branch-policies and the key is branch_policies, so the name of one
// cannot stand for the other.
const branchPoliciesKey = "branch_policies"

// FetchAll implements Collection.
//
// An environment that does not allow custom branch policies has no such
// endpoint, and GitHub answers 404. That is not a failure: it means there are
// no policies, which is exactly what an environment whose settings are about to
// be changed in the same apply should report. The declared policies then show
// up as creations, and they are sent after the environment that allows them.
func (DeploymentBranchPolicies) FetchAll(ctx context.Context, c Client, node schema.Node, path Path) (map[string]map[string]any, error) {
	listed, err := listElements(ctx, c, path, branchPoliciesKey)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return byName(listed, node.Segment)
}

// Create implements Collection.
func (DeploymentBranchPolicies) Create(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error {
	if err := send(ctx, c, http.MethodPost, path.String(), desired); err != nil {
		return fmt.Errorf("create deployment branch policy %v in %s: %w%s", desired[elementName], path, err, environmentHint(err))
	}
	return nil
}

// Update implements Collection.
//
// A policy is matched by name, so the only field that can differ is the type,
// and the update endpoint takes the name pattern alone: a policy created for
// branches cannot be turned into one for tags. The policy is therefore replaced
// rather than updated, which is the only way to make the declaration true.
func (d DeploymentBranchPolicies) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	if err := d.Delete(ctx, c, node, path, current); err != nil {
		return err
	}
	return d.Create(ctx, c, node, path, desired)
}

// Delete implements Collection.
func (d DeploymentBranchPolicies) Delete(ctx context.Context, c Client, node schema.Node, path Path, current map[string]any) error {
	target, err := d.ElementPath(path, current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, target.String()); err != nil {
		return fmt.Errorf("delete deployment branch policy %v: %w", current[elementName], err)
	}
	return nil
}

// ElementPath implements Collection, addressing a policy by the id GitHub
// issued for it.
func (DeploymentBranchPolicies) ElementPath(path Path, element map[string]any) (Path, error) {
	return elementByID(path, element, "deployment branch policy")
}

// environmentHint names the environment setting a refused write depends on.
//
// GitHub refuses a policy on an environment that has not been told to use
// custom branch policies, and says only that there is nothing there, which
// leaves the reason to be guessed at. Declaring the setting alongside the
// policies is enough -- the environment is written before what hangs off it --
// so the hint is worth more than the status is.
func environmentHint(err error) string {
	if !isNotFound(err) && !doesNotApply(err) {
		return ""
	}
	return " (the environment must declare deployment_branch_policy with custom_branch_policies: true)"
}
