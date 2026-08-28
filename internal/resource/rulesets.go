package resource

import (
	"context"
	"fmt"
	"net/http"
)

// Rulesets manages the repository's rulesets.
type Rulesets struct{}

// Name implements Collection.
func (Rulesets) Name() string { return "rulesets" }

const rulesetsPerPage = 100

// idField is what GitHub issues for a ruleset and what addresses it
// afterwards. It is not declared in the settings file, so it is only ever read
// from the current state.
const idField = "id"

// FetchAll implements Collection.
//
// Each ruleset is read individually after the listing. The listing describes a
// ruleset as the full object, but whether it fills in the rules and conditions
// is not something the description settles, and a plan built from a listing
// that leaves them out would report every rule as missing.
func (Rulesets) FetchAll(ctx context.Context, c Client, repo Repo) (map[string]map[string]any, error) {
	var listed []map[string]any

	err := eachPage(rulesetsPerPage, func(page int) (int, error) {
		var response []map[string]any
		// includes_parents is on by default and brings in the rulesets an
		// organization applies to this repository. Those cannot be managed
		// here, and leaving them in would have every plan offer to delete
		// them.
		path := fmt.Sprintf("%s?includes_parents=false&per_page=%d&page=%d", rulesetsPath(repo), rulesetsPerPage, page)
		if err := c.DoWithContext(ctx, http.MethodGet, path, nil, &response); err != nil {
			return 0, fmt.Errorf("list rulesets of %s: %w", repo, err)
		}
		listed = append(listed, response...)
		return len(response), nil
	})
	if err != nil {
		return nil, err
	}

	full := make([]map[string]any, 0, len(listed))
	for _, summary := range listed {
		id, err := rulesetID(summary)
		if err != nil {
			return nil, err
		}

		var ruleset map[string]any
		if err := c.DoWithContext(ctx, http.MethodGet, rulesetPath(repo, id), nil, &ruleset); err != nil {
			return nil, fmt.Errorf("get ruleset %s of %s: %w", id, repo, err)
		}
		full = append(full, ruleset)
	}

	return byName(full, "rulesets")
}

// Create implements Collection.
func (Rulesets) Create(ctx context.Context, c Client, repo Repo, desired map[string]any) error {
	if err := send(ctx, c, http.MethodPost, rulesetsPath(repo), desired); err != nil {
		return fmt.Errorf("create ruleset %v in %s: %w", desired[elementName], repo, err)
	}
	return nil
}

// Update implements Collection.
func (Rulesets) Update(ctx context.Context, c Client, repo Repo, current, desired map[string]any) error {
	id, err := rulesetID(current)
	if err != nil {
		return err
	}
	if err := send(ctx, c, http.MethodPut, rulesetPath(repo, id), desired); err != nil {
		return fmt.Errorf("update ruleset %v in %s: %w", current[elementName], repo, err)
	}
	return nil
}

// Delete implements Collection.
func (Rulesets) Delete(ctx context.Context, c Client, repo Repo, current map[string]any) error {
	id, err := rulesetID(current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, rulesetPath(repo, id)); err != nil {
		return fmt.Errorf("delete ruleset %v from %s: %w", current[elementName], repo, err)
	}
	return nil
}

// rulesetID returns the id GitHub issued for a ruleset, rendered as it appears
// in a path.
//
// The id arrives as a JSON number, so it is a float64 here; rendering it with
// %v would spell a large one in exponent notation and produce a path the API
// does not recognize.
func rulesetID(ruleset map[string]any) (string, error) {
	switch id := ruleset[idField].(type) {
	case float64:
		return fmt.Sprintf("%d", int64(id)), nil
	case int64:
		return fmt.Sprintf("%d", id), nil
	case int:
		return fmt.Sprintf("%d", id), nil
	case string:
		return id, nil
	default:
		return "", fmt.Errorf("ruleset %v has no usable id", ruleset[elementName])
	}
}

func rulesetsPath(repo Repo) string {
	return fmt.Sprintf("repos/%s/%s/rulesets", repo.Owner, repo.Name)
}

func rulesetPath(repo Repo, id string) string {
	return rulesetsPath(repo) + "/" + id
}
