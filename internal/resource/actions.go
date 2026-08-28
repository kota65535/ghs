package resource

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Actions manages how GitHub Actions runs in the repository: whether it runs
// at all, which actions it may run, and what a workflow is allowed to do.
//
// These are the settings that change quietly. Someone widens the token's
// default permissions in the web interface to unblock a workflow, and nothing
// afterwards says so.
type Actions struct{}

// Name implements Resource.
func (Actions) Name() string { return "actions" }

// actionsEndpoint is one of the paths the Actions settings are spread over,
// and the fields it accepts.
//
// GitHub splits these across several endpoints; the settings file describes
// the repository rather than the requests, so they are read and written as one
// resource. The field lists are what maps between the two.
type actionsEndpoint struct {
	path   string
	fields []string

	// conditional marks an endpoint that only exists in some repositories:
	// the allowed actions are listed only while the policy is to select them,
	// and some settings are for private repositories alone. Where the endpoint
	// does not apply, its fields are left out of what Fetch reports, and a
	// declaration of one shows up as a change against nothing.
	conditional bool
}

var actionsEndpoints = []actionsEndpoint{
	{
		path:   "actions/permissions",
		fields: []string{"enabled", "allowed_actions", "sha_pinning_required"},
	},
	{
		path:   "actions/permissions/workflow",
		fields: []string{"default_workflow_permissions", "can_approve_pull_request_reviews"},
	},
	{
		path:   "actions/permissions/fork-pr-contributor-approval",
		fields: []string{"approval_policy"},
	},
	{
		path:        "actions/permissions/selected-actions",
		fields:      []string{"github_owned_allowed", "verified_allowed", "patterns_allowed"},
		conditional: true,
	},
}

// Fetch implements Resource.
func (Actions) Fetch(ctx context.Context, c Client, repo Repo) (map[string]any, error) {
	current := map[string]any{}

	for _, endpoint := range actionsEndpoints {
		var reported map[string]any
		err := c.DoWithContext(ctx, http.MethodGet, actionsPath(repo, endpoint.path), nil, &reported)
		if err != nil {
			if endpoint.conditional && doesNotApply(err) {
				continue
			}
			return nil, fmt.Errorf("get %s of %s: %w", endpoint.path, repo, err)
		}

		for _, name := range endpoint.fields {
			if value, ok := reported[name]; ok {
				current[name] = value
			}
		}
	}

	return current, nil
}

// Apply implements Resource.
//
// Only the endpoints with a declared field are called, and the general
// permissions go first: which actions may be selected is a question that only
// makes sense once the policy says to select them.
func (Actions) Apply(ctx context.Context, c Client, repo Repo, desired map[string]any) error {
	for _, endpoint := range actionsEndpoints {
		body := map[string]any{}
		for _, name := range endpoint.fields {
			if value, declared := desired[name]; declared {
				body[name] = value
			}
		}
		if len(body) == 0 {
			continue
		}

		if err := send(ctx, c, http.MethodPut, actionsPath(repo, endpoint.path), body); err != nil {
			return fmt.Errorf("put %s of %s: %w", endpoint.path, repo, err)
		}
	}
	return nil
}

// doesNotApply reports an error that means the setting has no place in this
// repository, rather than one that means the request failed.
//
// GitHub answers 409 where the allowed actions are not being selected, and 422
// where a setting is for private repositories only. Both say the same thing:
// there is nothing here to read.
func doesNotApply(err error) bool {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusConflict || httpErr.StatusCode == http.StatusUnprocessableEntity
}

func actionsPath(repo Repo, endpoint string) string {
	return fmt.Sprintf("repos/%s/%s/%s", repo.Owner, repo.Name, endpoint)
}
