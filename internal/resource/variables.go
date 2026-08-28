package resource

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Variables manages the repository's Actions variables.
//
// Their values are shown in plans as they are: a variable is where GitHub puts
// configuration that is not secret, the file declaring it is reviewed in the
// open anyway, and hiding the values would take the point out of reviewing a
// change to one.
type Variables struct{}

// Name implements Collection.
func (Variables) Name() string { return "variables" }

// variablesPerPage is what this endpoint accepts at most, which is lower than
// the usual hundred.
const variablesPerPage = 30

// FetchAll implements Collection.
func (Variables) FetchAll(ctx context.Context, c Client, repo Repo) (map[string]map[string]any, error) {
	var all []map[string]any

	err := eachPage(variablesPerPage, func(page int) (int, error) {
		var response struct {
			Variables []map[string]any `json:"variables"`
		}
		path := fmt.Sprintf("%s?per_page=%d&page=%d", variablesPath(repo), variablesPerPage, page)
		if err := c.DoWithContext(ctx, http.MethodGet, path, nil, &response); err != nil {
			return 0, fmt.Errorf("list variables of %s: %w", repo, err)
		}
		all = append(all, response.Variables...)
		return len(response.Variables), nil
	})
	if err != nil {
		return nil, err
	}

	return byName(all, "variables")
}

// Create implements Collection.
func (Variables) Create(ctx context.Context, c Client, repo Repo, desired map[string]any) error {
	if err := send(ctx, c, http.MethodPost, variablesPath(repo), desired); err != nil {
		return fmt.Errorf("create variable %v in %s: %w", desired[elementName], repo, err)
	}
	return nil
}

// Update implements Collection.
func (Variables) Update(ctx context.Context, c Client, repo Repo, current, desired map[string]any) error {
	name, err := nameOf(current)
	if err != nil {
		return err
	}
	if err := send(ctx, c, http.MethodPatch, variablePath(repo, name), desired); err != nil {
		return fmt.Errorf("update variable %s in %s: %w", name, repo, err)
	}
	return nil
}

// Delete implements Collection.
func (Variables) Delete(ctx context.Context, c Client, repo Repo, current map[string]any) error {
	name, err := nameOf(current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, variablePath(repo, name)); err != nil {
		return fmt.Errorf("delete variable %s from %s: %w", name, repo, err)
	}
	return nil
}

func variablesPath(repo Repo) string {
	return fmt.Sprintf("repos/%s/%s/actions/variables", repo.Owner, repo.Name)
}

func variablePath(repo Repo, name string) string {
	return variablesPath(repo) + "/" + url.PathEscape(name)
}

// nameOf returns the name of an element, which is how most collections address
// one.
func nameOf(element map[string]any) (string, error) {
	name, ok := element[elementName].(string)
	if !ok || name == "" {
		return "", fmt.Errorf("element has no name: %v", element)
	}
	return name, nil
}
