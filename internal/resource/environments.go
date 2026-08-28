package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kota65535/ghs/internal/schema"
)

// Environments manages the repository's deployment environments, whose
// reported shape differs from the one that declares them.
type Environments struct{}

// FetchAll implements Collection.
func (Environments) FetchAll(ctx context.Context, c Client, node schema.Node, path Path) (map[string]map[string]any, error) {
	listed, err := listElements(ctx, c, path, node.Segment)
	if err != nil {
		return nil, err
	}

	declared := make([]map[string]any, 0, len(listed))
	for _, environment := range listed {
		declared = append(declared, asRequest(environment))
	}
	return byName(declared, node.Segment)
}

// Create implements Collection.
//
// Creating and updating an environment are the same request: PUT takes the
// name in the path and settles the environment either way.
func (e Environments) Create(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error {
	return e.settle(ctx, c, path, desired, "create")
}

// Update implements Collection.
func (e Environments) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	return e.settle(ctx, c, path, desired, "update")
}

func (e Environments) settle(ctx context.Context, c Client, path Path, desired map[string]any, what string) error {
	target, err := e.ElementPath(path, desired)
	if err != nil {
		return err
	}
	// The name travels in the path, so sending it in the body would declare a
	// field the request has no such thing as.
	if err := send(ctx, c, http.MethodPut, target.String(), withoutName(desired)); err != nil {
		return fmt.Errorf("%s %s: %w", what, target, err)
	}
	return nil
}

// Delete implements Collection.
func (e Environments) Delete(ctx context.Context, c Client, node schema.Node, path Path, current map[string]any) error {
	target, err := e.ElementPath(path, current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, target.String()); err != nil {
		return fmt.Errorf("delete %s: %w", target, err)
	}
	return nil
}

// ElementPath implements Collection.
func (Environments) ElementPath(path Path, element map[string]any) (Path, error) {
	name, err := nameOf(element)
	if err != nil {
		return Path{}, err
	}
	return path.Element(name), nil
}

// withoutName returns the element without the name that identifies it.
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

// asRequest rewrites a reported environment into the shape that declares one.
//
// The two do not match: what a request sends as wait_timer, reviewers and
// prevent_self_review comes back folded into a protection_rules array, one
// entry per kind of rule. Undoing that here keeps the comparison in diff a
// plain field-by-field one, the same as every other node gets.
func asRequest(environment map[string]any) map[string]any {
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
