package resource

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
)

// Environments manages the repository's deployment environments.
type Environments struct{}

// Name implements Collection.
func (Environments) Name() string { return "environments" }

const environmentsPerPage = 100

// FetchAll implements Collection.
func (e Environments) FetchAll(ctx context.Context, c Client, repo Repo) (map[string]map[string]any, error) {
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

	// The variables belong to the environment but are listed separately, so
	// they are read here and put where the settings file declares them.
	for _, environment := range all {
		name, err := nameOf(environment)
		if err != nil {
			return nil, err
		}
		variables, err := e.fetchVariables(ctx, c, repo, name)
		if err != nil {
			return nil, err
		}
		environment[variablesField] = variables
	}

	return byName(all, "environments")
}

// variablesField is where an environment's variables are declared and where
// FetchAll reports them.
const variablesField = "variables"

// environmentVariablesPerPage matches what the repository-level endpoint
// accepts, which is lower than the usual hundred.
const environmentVariablesPerPage = 30

// fetchVariables returns the variables of one environment as a sequence, the
// shape they are declared in.
func (Environments) fetchVariables(ctx context.Context, c Client, repo Repo, environment string) ([]any, error) {
	variables := []any{}

	err := eachPage(environmentVariablesPerPage, func(page int) (int, error) {
		var response struct {
			Variables []map[string]any `json:"variables"`
		}
		path := fmt.Sprintf("%s?per_page=%d&page=%d",
			environmentVariablesPath(repo, environment), environmentVariablesPerPage, page)
		if err := c.DoWithContext(ctx, http.MethodGet, path, nil, &response); err != nil {
			return 0, fmt.Errorf("list variables of environment %s in %s: %w", environment, repo, err)
		}
		for _, variable := range response.Variables {
			variables = append(variables, variable)
		}
		return len(response.Variables), nil
	})
	if err != nil {
		return nil, err
	}

	return variables, nil
}

// Create implements Collection.
//
// Creating and updating an environment are the same request: PUT takes the
// name in the path and settles the environment either way.
func (e Environments) Create(ctx context.Context, c Client, repo Repo, desired map[string]any) error {
	return e.settle(ctx, c, repo, nil, desired, "create")
}

// Update implements Collection.
func (e Environments) Update(ctx context.Context, c Client, repo Repo, current, desired map[string]any) error {
	return e.settle(ctx, c, repo, current, desired, "update")
}

// settle brings one environment in line with what was declared: the
// environment itself, then the variables it owns.
//
// The variables go out one at a time because that is the only way the API
// offers, but they are part of the same declaration and are settled together
// with it. The environment goes first, since a variable cannot be put into an
// environment that does not exist yet.
func (e Environments) settle(ctx context.Context, c Client, repo Repo, current, desired map[string]any, what string) error {
	name, err := nameOf(desired)
	if err != nil {
		return err
	}

	body := withoutName(desired)
	delete(body, variablesField)
	if err := send(ctx, c, http.MethodPut, environmentPath(repo, name), body); err != nil {
		return fmt.Errorf("%s environment %s in %s: %w", what, name, repo, err)
	}

	declared, ok := desired[variablesField]
	if !ok {
		// Not declared, so the variables are not managed and are left alone.
		return nil
	}
	var reported any
	if current != nil {
		reported = current[variablesField]
	}
	return e.settleVariables(ctx, c, repo, name, reported, declared)
}

// settleVariables creates, updates and deletes the variables of one
// environment, in that order and for the same reason apply uses it on
// collections: a failure part way through leaves a spare variable rather than
// a missing one.
func (Environments) settleVariables(ctx context.Context, c Client, repo Repo, environment string, current, desired any) error {
	reported := variablesByName(current)
	declared := variablesByName(desired)

	for _, name := range sortedNames(declared) {
		variable := declared[name]
		existing, exists := reported[name]
		if !exists {
			if err := send(ctx, c, http.MethodPost, environmentVariablesPath(repo, environment), variable); err != nil {
				return fmt.Errorf("create variable %s of environment %s in %s: %w", name, environment, repo, err)
			}
			continue
		}
		if reflect.DeepEqual(existing[valueField], variable[valueField]) {
			continue
		}
		path := environmentVariablePath(repo, environment, name)
		if err := send(ctx, c, http.MethodPatch, path, variable); err != nil {
			return fmt.Errorf("update variable %s of environment %s in %s: %w", name, environment, repo, err)
		}
	}

	for _, name := range sortedNames(reported) {
		if _, declared := declared[name]; declared {
			continue
		}
		if err := deleteAt(ctx, c, environmentVariablePath(repo, environment, name)); err != nil {
			return fmt.Errorf("delete variable %s of environment %s in %s: %w", name, environment, repo, err)
		}
	}

	return nil
}

// valueField is the only field of a variable that can be changed; its name is
// what addresses it.
const valueField = "value"

// variablesByName keys a sequence of variables by name.
func variablesByName(value any) map[string]map[string]any {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}

	out := make(map[string]map[string]any, len(entries))
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := entry[elementName].(string); ok && name != "" {
			out[name] = entry
		}
	}
	return out
}

func sortedNames(entries map[string]map[string]any) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

func environmentVariablesPath(repo Repo, environment string) string {
	return environmentPath(repo, environment) + "/variables"
}

func environmentVariablePath(repo Repo, environment, name string) string {
	return environmentVariablesPath(repo, environment) + "/" + url.PathEscape(name)
}
