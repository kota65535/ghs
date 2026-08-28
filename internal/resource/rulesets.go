package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kota65535/ghs/internal/schema"
)

// Rulesets manages the repository's rulesets, which GitHub addresses by an id
// it issues rather than by the name the settings file matches them on.
type Rulesets struct{}

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
func (Rulesets) FetchAll(ctx context.Context, c Client, node schema.Node, path Path) (map[string]map[string]any, error) {
	// includes_parents is on by default and brings in the rulesets an
	// organization applies to this repository. Those cannot be managed here,
	// and leaving them in would have every plan offer to delete them.
	var listed []map[string]any
	err := eachPage(func(page int) (int, error) {
		var response []map[string]any
		query := fmt.Sprintf("%s?includes_parents=false&per_page=%d&page=%d", path, perPage, page)
		if err := c.DoWithContext(ctx, http.MethodGet, query, nil, &response); err != nil {
			return 0, fmt.Errorf("list %s: %w", path, err)
		}
		listed = append(listed, response...)
		return len(response), nil
	})
	if err != nil {
		return nil, err
	}

	full := make([]map[string]any, 0, len(listed))
	for _, summary := range listed {
		target, err := Rulesets{}.ElementPath(path, summary)
		if err != nil {
			return nil, err
		}

		var ruleset map[string]any
		if err := c.DoWithContext(ctx, http.MethodGet, target.String(), nil, &ruleset); err != nil {
			return nil, fmt.Errorf("get %s: %w", target, err)
		}
		full = append(full, ruleset)
	}

	return byName(full, node.Segment)
}

// Create implements Collection.
func (Rulesets) Create(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error {
	if err := send(ctx, c, http.MethodPost, path.String(), desired); err != nil {
		return fmt.Errorf("create ruleset %v in %s: %w", desired[elementName], path, err)
	}
	return nil
}

// Update implements Collection.
func (r Rulesets) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	target, err := r.ElementPath(path, current)
	if err != nil {
		return err
	}
	if err := send(ctx, c, http.MethodPut, target.String(), desired); err != nil {
		return fmt.Errorf("update ruleset %v: %w", current[elementName], err)
	}
	return nil
}

// Delete implements Collection.
func (r Rulesets) Delete(ctx context.Context, c Client, node schema.Node, path Path, current map[string]any) error {
	target, err := r.ElementPath(path, current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, target.String()); err != nil {
		return fmt.Errorf("delete ruleset %v: %w", current[elementName], err)
	}
	return nil
}

// ElementPath implements Collection, addressing a ruleset by the id GitHub
// issued for it.
//
// The id arrives as a JSON number, so it is a float64 here; rendering it with
// %v would spell a large one in exponent notation and produce a path the API
// does not recognize.
func (Rulesets) ElementPath(path Path, element map[string]any) (Path, error) {
	switch id := element[idField].(type) {
	case float64:
		return path.Element(fmt.Sprintf("%d", int64(id))), nil
	case int64:
		return path.Element(fmt.Sprintf("%d", id)), nil
	case int:
		return path.Element(fmt.Sprintf("%d", id)), nil
	case string:
		return path.Element(id), nil
	default:
		return Path{}, fmt.Errorf("ruleset %v has no usable id", element[elementName])
	}
}
