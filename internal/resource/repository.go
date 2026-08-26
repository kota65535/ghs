package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Repository manages the repository object itself: GET and PATCH on
// /repos/{owner}/{repo}.
type Repository struct{}

// Name implements Resource.
func (Repository) Name() string { return "repository" }

// Fetch implements Resource.
func (Repository) Fetch(ctx context.Context, c Client, repo Repo) (map[string]any, error) {
	var current map[string]any
	if err := c.DoWithContext(ctx, http.MethodGet, repoPath(repo), nil, &current); err != nil {
		return nil, fmt.Errorf("get %s: %w", repo, err)
	}
	return current, nil
}

// Apply implements Resource.
func (Repository) Apply(ctx context.Context, c Client, repo Repo, desired map[string]any) error {
	body, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("encode request body: %w", err)
	}
	// The response is discarded: what the settings now are is a question for
	// the next plan, which asks the API rather than trusting this reply.
	if err := c.DoWithContext(ctx, http.MethodPatch, repoPath(repo), bytes.NewReader(body), nil); err != nil {
		return fmt.Errorf("patch %s: %w", repo, err)
	}
	return nil
}

func repoPath(repo Repo) string {
	return fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)
}
