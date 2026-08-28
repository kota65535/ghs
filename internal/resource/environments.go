package resource

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Environments manages the repository's deployment environments.
type Environments struct{}

// Name implements Collection.
func (Environments) Name() string { return "environments" }

const environmentsPerPage = 100

// FetchAll implements Collection.
func (Environments) FetchAll(ctx context.Context, c Client, repo Repo) (map[string]map[string]any, error) {
	var all []map[string]any

	err := eachPage(environmentsPerPage, func(page int) (int, error) {
		var response struct {
			Environments []map[string]any `json:"environments"`
		}
		path := fmt.Sprintf("%s?per_page=%d&page=%d", environmentsPath(repo), environmentsPerPage, page)
		if err := c.DoWithContext(ctx, http.MethodGet, path, nil, &response); err != nil {
			return 0, fmt.Errorf("list environments of %s: %w", repo, err)
		}
		for _, environment := range response.Environments {
			all = append(all, asEnvironmentRequest(environment))
		}
		return len(response.Environments), nil
	})
	if err != nil {
		return nil, err
	}

	return byName(all, "environments")
}

// Create implements Collection.
//
// Creating and updating an environment are the same request: PUT takes the
// name in the path and settles the environment either way.
func (e Environments) Create(ctx context.Context, c Client, repo Repo, desired map[string]any) error {
	name, err := nameOf(desired)
	if err != nil {
		return err
	}
	if err := send(ctx, c, http.MethodPut, environmentPath(repo, name), withoutName(desired)); err != nil {
		return fmt.Errorf("create environment %s in %s: %w", name, repo, err)
	}
	return nil
}

// Update implements Collection.
func (e Environments) Update(ctx context.Context, c Client, repo Repo, current, desired map[string]any) error {
	name, err := nameOf(desired)
	if err != nil {
		return err
	}
	if err := send(ctx, c, http.MethodPut, environmentPath(repo, name), withoutName(desired)); err != nil {
		return fmt.Errorf("update environment %s in %s: %w", name, repo, err)
	}
	return nil
}

// Delete implements Collection.
func (Environments) Delete(ctx context.Context, c Client, repo Repo, current map[string]any) error {
	name, err := nameOf(current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, environmentPath(repo, name)); err != nil {
		return fmt.Errorf("delete environment %s from %s: %w", name, repo, err)
	}
	return nil
}

// withoutName returns the element without its name, which an environment
// carries in the path rather than in the body.
func withoutName(element map[string]any) map[string]any {
	body := make(map[string]any, len(element))
	for key, value := range element {
		if key == elementName {
			continue
		}
		body[key] = value
	}
	return body
}

// asEnvironmentRequest rewrites a reported environment into the shape that
// declares one.
//
// The two do not match: what a request sends as wait_timer, reviewers and
// prevent_self_review comes back folded into a protection_rules array, one
// entry per kind of rule. Undoing that here keeps the comparison in diff a
// plain field-by-field one, the same as every other resource gets.
func asEnvironmentRequest(environment map[string]any) map[string]any {
	// A rule that is off is not reported at all, so the fields it stands for
	// start at the value that means "off". Without this, declaring the value
	// GitHub is already at -- wait_timer: 0 on an environment with no delay --
	// reads as a change to a field the response does not have, and no amount
	// of applying it makes the difference go away.
	out := map[string]any{
		"wait_timer":          float64(0),
		"prevent_self_review": false,
		"reviewers":           []any{},
	}

	// deployment_branch_policy is reported the way it is declared, so it is
	// carried straight over, as is the name that identifies the element.
	for _, key := range []string{elementName, "deployment_branch_policy"} {
		if value, ok := environment[key]; ok {
			out[key] = value
		}
	}

	rules, _ := environment["protection_rules"].([]any)
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		switch rule["type"] {
		case "wait_timer":
			if value, ok := rule["wait_timer"]; ok {
				out["wait_timer"] = value
			}
		case "required_reviewers":
			if value, ok := rule["prevent_self_review"]; ok {
				out["prevent_self_review"] = value
			}
			if reviewers, ok := rule["reviewers"].([]any); ok {
				out["reviewers"] = asReviewerRequests(reviewers)
			}
		}
	}

	return out
}

// asReviewerRequests rewrites reported reviewers into the shape that declares
// them: a request names a reviewer by type and id, while the response nests
// the whole user or team under a reviewer key.
func asReviewerRequests(reviewers []any) []any {
	out := make([]any, 0, len(reviewers))

	for _, raw := range reviewers {
		reviewer, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		declared := map[string]any{}
		if value, ok := reviewer["type"]; ok {
			declared["type"] = value
		}
		if nested, ok := reviewer["reviewer"].(map[string]any); ok {
			if id, ok := nested[idField]; ok {
				declared[idField] = id
			}
		}
		out = append(out, declared)
	}

	return out
}

func environmentsPath(repo Repo) string {
	return fmt.Sprintf("repos/%s/%s/environments", repo.Owner, repo.Name)
}

func environmentPath(repo Repo, name string) string {
	return environmentsPath(repo) + "/" + url.PathEscape(name)
}
