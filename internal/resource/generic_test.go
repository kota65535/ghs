package resource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var variablesNode = schema.Node{
	Kind:    schema.KindCollection,
	Segment: "variables",
	Method:  http.MethodPost,
}

func variablesPath() Path { return At(testRepo).Child("actions").Child("variables") }

func TestGenericCollectionKeysElementsByName(t *testing.T) {
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/actions/variables": `{"total_count": 2, "variables": [
			{"name": "DEPLOY_REGION", "value": "ap-northeast-1", "created_at": "2026-01-01T00:00:00Z"},
			{"name": "LOG_LEVEL", "value": "info"}
		]}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := GenericCollection{}.FetchAll(context.Background(), newTestClient(t, srv), variablesNode, variablesPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if len(current) != 2 {
		t.Fatalf("got %d elements, want 2: %+v", len(current), current)
	}
	if current["DEPLOY_REGION"]["value"] != "ap-northeast-1" {
		t.Errorf("DEPLOY_REGION = %+v, want its reported value", current["DEPLOY_REGION"])
	}
	// Fields the file does not declare stay put: they are simply not managed.
	if current["DEPLOY_REGION"]["created_at"] == nil {
		t.Error("created_at was dropped, want the reported element kept whole")
	}
}

func TestGenericCollectionAcceptsABareList(t *testing.T) {
	// GitHub wraps some of these lists in an object and returns others bare.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/rulesets": `[{"id": 7, "name": "protect-main"}]`,
	})
	srv := rec.server()
	defer srv.Close()

	node := schema.Node{Kind: schema.KindCollection, Segment: "rulesets", Method: http.MethodPost}
	current, err := GenericCollection{}.FetchAll(context.Background(), newTestClient(t, srv), node, At(testRepo).Child("rulesets"))
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if _, ok := current["protect-main"]; !ok {
		t.Errorf("current = %+v, want the bare list read", current)
	}
}

func TestGenericCollectionReadsEveryPage(t *testing.T) {
	// A full page means there may be another. Without following it the plan
	// would offer to create elements that already exist.
	var full []string
	for i := 0; i < perPage; i++ {
		full = append(full, fmt.Sprintf(`{"name": "VAR_%02d", "value": "x"}`, i))
	}

	var page int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			fmt.Fprintf(w, `{"variables": [%s]}`, strings.Join(full, ","))
			return
		}
		fmt.Fprint(w, `{"variables": [{"name": "LAST", "value": "y"}]}`)
	}))
	defer srv.Close()

	current, err := GenericCollection{}.FetchAll(context.Background(), newTestClient(t, srv), variablesNode, variablesPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if len(current) != perPage+1 {
		t.Errorf("got %d elements, want %d", len(current), perPage+1)
	}
	if _, ok := current["LAST"]; !ok {
		t.Error("the second page was not read")
	}
}

func TestGenericCollectionCreateUpdateDelete(t *testing.T) {
	desired := map[string]any{"name": "DEPLOY_REGION", "value": "ap-northeast-1"}

	t.Run("create posts to the collection", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (GenericCollection{}).Create(context.Background(), newTestClient(t, srv), variablesNode, variablesPath(), desired); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPost || got.path != "/repos/kota65535/ghs/actions/variables" {
			t.Errorf("request = %s %s, want POST on the collection", got.method, got.path)
		}
		if got.body["name"] != "DEPLOY_REGION" {
			t.Errorf("body = %+v, want the declared element", got.body)
		}
	})

	t.Run("update patches the element", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "DEPLOY_REGION", "value": "us-east-1"}
		if err := (GenericCollection{}).Update(context.Background(), newTestClient(t, srv), variablesNode, variablesPath(), current, desired); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPatch || got.path != "/repos/kota65535/ghs/actions/variables/DEPLOY_REGION" {
			t.Errorf("request = %s %s, want PATCH on the element", got.method, got.path)
		}
		// The whole declaration is sent: what is applied stays identical to
		// what was reviewed.
		if got.body["value"] != "ap-northeast-1" {
			t.Errorf("body = %+v, want the declared element", got.body)
		}
	})

	t.Run("delete removes the element", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "LEGACY", "value": "x"}
		if err := (GenericCollection{}).Delete(context.Background(), newTestClient(t, srv), variablesNode, variablesPath(), current); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/actions/variables/LEGACY" {
			t.Errorf("request = %s %s, want DELETE on the element", got.method, got.path)
		}
	})
}

func TestGenericCollectionReportsAPIErrors(t *testing.T) {
	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/actions/variables", http.StatusForbidden)
	srv := rec.server()
	defer srv.Close()

	if _, err := (GenericCollection{}).FetchAll(context.Background(), newTestClient(t, srv), variablesNode, variablesPath()); err == nil {
		t.Error("FetchAll succeeded, want an error")
	}
}

func TestFetchAllRejectsAnUnnamedElement(t *testing.T) {
	// Without a name there is nothing to match the element against.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/actions/variables": `{"variables": [{"value": "x"}]}`,
	})
	srv := rec.server()
	defer srv.Close()

	if _, err := (GenericCollection{}).FetchAll(context.Background(), newTestClient(t, srv), variablesNode, variablesPath()); err == nil {
		t.Error("FetchAll succeeded, want an element with no name rejected")
	}
}

func TestEachPageStopsOnAShortPage(t *testing.T) {
	var pages []int
	err := eachPage(func(page int) (int, error) {
		pages = append(pages, page)
		if page == 3 {
			return perPage - 1, nil
		}
		return perPage, nil
	})
	if err != nil {
		t.Fatalf("eachPage: %v", err)
	}
	if len(pages) != 3 {
		t.Errorf("read %d pages, want 3", len(pages))
	}
}

func TestEachPageGivesUpOnAnEndlessList(t *testing.T) {
	calls := 0
	err := eachPage(func(int) (int, error) {
		calls++
		return perPage, nil
	})
	if err == nil {
		t.Fatal("eachPage returned no error, want it to give up")
	}
	if calls != maxPages {
		t.Errorf("made %d calls, want it capped at %d", calls, maxPages)
	}
}
