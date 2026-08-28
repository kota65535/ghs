package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recorder captures the requests a collection makes, and answers them from a
// table of canned responses keyed by "METHOD /path".
type recorder struct {
	t         *testing.T
	responses map[string]string
	requests  []request
}

type request struct {
	method string
	path   string
	query  string
	body   map[string]any
}

func newRecorder(t *testing.T, responses map[string]string) *recorder {
	return &recorder{t: t, responses: responses}
}

func (r *recorder) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// EscapedPath rather than Path: the latter is decoded, which would hide
		// whether an element name reached the server escaped.
		captured := request{method: req.Method, path: req.URL.EscapedPath(), query: req.URL.RawQuery}
		if req.Body != nil {
			if raw, err := io.ReadAll(req.Body); err == nil && len(raw) > 0 {
				if err := json.Unmarshal(raw, &captured.body); err != nil {
					r.t.Errorf("decode request body of %s %s: %v", req.Method, req.URL.Path, err)
				}
			}
		}
		r.requests = append(r.requests, captured)

		w.Header().Set("Content-Type", "application/json")
		if body, ok := r.responses[req.Method+" "+req.URL.Path]; ok {
			io.WriteString(w, body)
			return
		}
		io.WriteString(w, "{}")
	}))
}

// only returns the single request made, failing when there was not exactly one.
func (r *recorder) only() request {
	r.t.Helper()
	if len(r.requests) != 1 {
		r.t.Fatalf("made %d requests, want 1: %+v", len(r.requests), r.requests)
	}
	return r.requests[0]
}

func TestVariablesFetchAllKeysElementsByName(t *testing.T) {
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/actions/variables": `{"total_count": 2, "variables": [
			{"name": "DEPLOY_REGION", "value": "ap-northeast-1", "created_at": "2026-01-01T00:00:00Z"},
			{"name": "LOG_LEVEL", "value": "info"}
		]}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Variables{}.FetchAll(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if len(current) != 2 {
		t.Fatalf("got %d variables, want 2: %+v", len(current), current)
	}
	if current["DEPLOY_REGION"]["value"] != "ap-northeast-1" {
		t.Errorf("DEPLOY_REGION = %+v, want its reported value", current["DEPLOY_REGION"])
	}
	// Fields the file does not declare stay put: they are simply not managed,
	// exactly as with a single-object resource.
	if current["DEPLOY_REGION"]["created_at"] == nil {
		t.Error("created_at was dropped, want the reported element kept whole")
	}
}

func TestVariablesFetchAllReadsEveryPage(t *testing.T) {
	// A full page means there may be another. Without following it the plan
	// would offer to create variables that already exist.
	var full []string
	for i := 0; i < variablesPerPage; i++ {
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
		io.WriteString(w, `{"variables": [{"name": "LAST", "value": "y"}]}`)
	}))
	defer srv.Close()

	current, err := Variables{}.FetchAll(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if len(current) != variablesPerPage+1 {
		t.Errorf("got %d variables, want %d", len(current), variablesPerPage+1)
	}
	if _, ok := current["LAST"]; !ok {
		t.Error("the second page was not read")
	}
}

func TestVariablesCreateUpdateDelete(t *testing.T) {
	repo := Repo{Owner: "kota65535", Name: "ghs"}
	desired := map[string]any{"name": "DEPLOY_REGION", "value": "ap-northeast-1"}

	t.Run("create", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (Variables{}).Create(context.Background(), newTestClient(t, srv), repo, desired); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPost || got.path != "/repos/kota65535/ghs/actions/variables" {
			t.Errorf("request = %s %s, want POST on the collection", got.method, got.path)
		}
		if got.body["name"] != "DEPLOY_REGION" || got.body["value"] != "ap-northeast-1" {
			t.Errorf("body = %+v, want the declared element", got.body)
		}
	})

	t.Run("update", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "DEPLOY_REGION", "value": "us-east-1"}
		if err := (Variables{}).Update(context.Background(), newTestClient(t, srv), repo, current, desired); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPatch || got.path != "/repos/kota65535/ghs/actions/variables/DEPLOY_REGION" {
			t.Errorf("request = %s %s, want PATCH on the element", got.method, got.path)
		}
		// The whole declaration is sent, as it is for a single-object
		// resource: what is applied stays identical to what was reviewed.
		if got.body["value"] != "ap-northeast-1" {
			t.Errorf("body = %+v, want the declared element", got.body)
		}
	})

	t.Run("delete", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "LEGACY", "value": "x"}
		if err := (Variables{}).Delete(context.Background(), newTestClient(t, srv), repo, current); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/actions/variables/LEGACY" {
			t.Errorf("request = %s %s, want DELETE on the element", got.method, got.path)
		}
	})
}

func TestVariablesReportsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"message": "Resource not accessible by integration"}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	repo := Repo{Owner: "kota65535", Name: "ghs"}

	if _, err := (Variables{}).FetchAll(context.Background(), client, repo); err == nil {
		t.Error("FetchAll succeeded, want an error")
	}
	if err := (Variables{}).Create(context.Background(), client, repo, map[string]any{"name": "A"}); err == nil {
		t.Error("Create succeeded, want an error")
	}
}

func TestLookupCollection(t *testing.T) {
	for _, name := range []string{"variables", "rulesets", "environments"} {
		c, err := LookupCollection(name)
		if err != nil {
			t.Errorf("LookupCollection(%q): %v", name, err)
			continue
		}
		if c.Name() != name {
			t.Errorf("Name = %q, want %q", c.Name(), name)
		}
	}

	if _, err := LookupCollection("repository"); err == nil {
		t.Error("LookupCollection(\"repository\") succeeded, want it looked up as a resource instead")
	}
	if _, err := Lookup("variables"); err == nil {
		t.Error("Lookup(\"variables\") succeeded, want it looked up as a collection instead")
	}
}

func TestEachPageStopsOnAShortPage(t *testing.T) {
	var pages []int
	err := eachPage(2, func(page int) (int, error) {
		pages = append(pages, page)
		if page == 3 {
			return 1, nil
		}
		return 2, nil
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
	err := eachPage(2, func(int) (int, error) {
		calls++
		return 2, nil
	})
	if err == nil {
		t.Fatal("eachPage returned no error, want it to give up")
	}
	if calls != maxPages {
		t.Errorf("made %d calls, want it capped at %d", calls, maxPages)
	}
}

func TestByNameRejectsAnUnnamedElement(t *testing.T) {
	if _, err := byName([]map[string]any{{"value": "x"}}, "variables"); err == nil {
		t.Error("byName succeeded, want an element with no name rejected")
	}
}
