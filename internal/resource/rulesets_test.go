package resource

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var rulesetsNode = schema.Node{
	Kind:    schema.KindCollection,
	Segment: "rulesets",
	Method:  http.MethodPost,
}

func rulesetsPath() Path { return At(testRepo).Child("rulesets") }

func TestRulesetsFetchAllReadsEachRulesetInFull(t *testing.T) {
	// The listing is not trusted to carry rules and conditions, so each
	// ruleset is read on its own. A plan built from a listing without them
	// would report every declared rule as missing.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/rulesets": `[{"id": 7, "name": "protect-main"}]`,
		"GET /repos/kota65535/ghs/rulesets/7": `{
			"id": 7,
			"name": "protect-main",
			"target": "branch",
			"enforcement": "active",
			"rules": [{"type": "pull_request", "parameters": {"required_approving_review_count": 1}}]
		}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Rulesets{}.FetchAll(context.Background(), newTestClient(t, srv), rulesetsNode, rulesetsPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	ruleset, ok := current["protect-main"]
	if !ok {
		t.Fatalf("protect-main is missing: %+v", current)
	}
	if ruleset["rules"] == nil {
		t.Error("rules are missing, want the ruleset read in full")
	}
	// The id is kept because it is what addresses the ruleset later.
	if ruleset[idField] != float64(7) {
		t.Errorf("id = %v, want 7", ruleset[idField])
	}
}

func TestRulesetsFetchAllExcludesInheritedRulesets(t *testing.T) {
	// Rulesets an organization applies to the repository cannot be managed
	// here. Left in the listing, every plan would offer to delete them.
	rec := newRecorder(t, map[string]string{"GET /repos/kota65535/ghs/rulesets": `[]`})
	srv := rec.server()
	defer srv.Close()

	if _, err := (Rulesets{}).FetchAll(context.Background(), newTestClient(t, srv), rulesetsNode, rulesetsPath()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	got := rec.only()
	if !strings.Contains(got.query, "includes_parents=false") {
		t.Errorf("query = %q, want inherited rulesets excluded", got.query)
	}
}

func TestRulesetsAreAddressedByTheIDGitHubIssued(t *testing.T) {
	desired := map[string]any{"name": "protect-main", "target": "branch", "enforcement": "active"}
	current := map[string]any{"name": "protect-main", "id": float64(7), "enforcement": "evaluate"}

	t.Run("create posts to the collection", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (Rulesets{}).Create(context.Background(), newTestClient(t, srv), rulesetsNode, rulesetsPath(), desired); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPost || got.path != "/repos/kota65535/ghs/rulesets" {
			t.Errorf("request = %s %s, want POST on the collection", got.method, got.path)
		}
	})

	t.Run("update puts to the id", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (Rulesets{}).Update(context.Background(), newTestClient(t, srv), rulesetsNode, rulesetsPath(), current, desired); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPut || got.path != "/repos/kota65535/ghs/rulesets/7" {
			t.Errorf("request = %s %s, want PUT on the ruleset id", got.method, got.path)
		}
		// The id belongs to GitHub, not to the settings file, so it is not
		// sent back.
		if _, sent := got.body[idField]; sent {
			t.Errorf("body = %+v, want the id left out", got.body)
		}
	})

	t.Run("delete removes the id", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (Rulesets{}).Delete(context.Background(), newTestClient(t, srv), rulesetsNode, rulesetsPath(), current); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/rulesets/7" {
			t.Errorf("request = %s %s, want DELETE on the ruleset id", got.method, got.path)
		}
	})
}

func TestRulesetIDIsRenderedAsAnInteger(t *testing.T) {
	// The id arrives as a JSON number. Spelling a large one in exponent
	// notation would build a path the API does not recognize.
	path, err := Rulesets{}.ElementPath(rulesetsPath(), map[string]any{idField: float64(123456789)})
	if err != nil {
		t.Fatalf("ElementPath: %v", err)
	}
	if !strings.HasSuffix(path.String(), "/123456789") {
		t.Errorf("path = %q, want it to end in the id", path)
	}

	if _, err := (Rulesets{}).ElementPath(rulesetsPath(), map[string]any{"name": "no-id"}); err == nil {
		t.Error("ElementPath succeeded without an id, want an error")
	}
}

func TestRulesetsUpdateWithoutAnIDFails(t *testing.T) {
	// Without an id there is nothing to address, and guessing would risk
	// writing over a different ruleset.
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := Rulesets{}.Update(context.Background(), newTestClient(t, srv), rulesetsNode, rulesetsPath(),
		map[string]any{"name": "protect-main"}, map[string]any{"name": "protect-main"})
	if err == nil {
		t.Fatal("Update succeeded, want an error")
	}
	if len(rec.requests) != 0 {
		t.Errorf("made %d requests, want none", len(rec.requests))
	}
}
