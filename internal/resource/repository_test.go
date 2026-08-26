package resource

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

// newTestClient returns a real go-gh REST client whose requests are routed to
// srv, so the tests exercise the same client the commands use.
func newTestClient(t *testing.T, srv *httptest.Server) *api.RESTClient {
	t.Helper()

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	client, err := api.NewRESTClient(api.ClientOptions{
		Host:      "github.com",
		AuthToken: "test-token",
		Transport: redirect{to: target, base: srv.Client().Transport},
	})
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}
	return client
}

// redirect sends every request to the test server while leaving the path
// intact, so assertions can be made on the path the client built.
type redirect struct {
	to   *url.URL
	base http.RoundTripper
}

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.to.Scheme
	req.URL.Host = r.to.Host
	req.Host = r.to.Host

	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func TestRepositoryFetch(t *testing.T) {
	var gotPath, gotMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"has_issues": true, "allow_auto_merge": false, "id": 1296269}`)
	}))
	defer srv.Close()

	current, err := Repository{}.Fetch(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if gotPath != "/repos/kota65535/ghs" {
		t.Errorf("path = %s, want /repos/kota65535/ghs", gotPath)
	}
	if current["has_issues"] != true {
		t.Errorf("has_issues = %v, want true", current["has_issues"])
	}
	// Numbers arrive as float64, which is the type shape declared settings are
	// normalized into before they are compared.
	if _, ok := current["id"].(float64); !ok {
		t.Errorf("id has type %T, want float64", current["id"])
	}
}

func TestRepositoryFetchReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message": "Not Found"}`)
	}))
	defer srv.Close()

	_, err := Repository{}.Fetch(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "nope"})
	if err == nil {
		t.Fatal("Fetch succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "kota65535/nope") {
		t.Errorf("err = %v, want the repository named", err)
	}
}

func TestRepositoryApplySendsDeclaredFields(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	desired := map[string]any{"allow_auto_merge": true, "homepage": nil}
	err := Repository{}.Apply(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}, desired)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/repos/kota65535/ghs" {
		t.Errorf("path = %s, want /repos/kota65535/ghs", gotPath)
	}
	if gotBody["allow_auto_merge"] != true {
		t.Errorf("body allow_auto_merge = %v, want true", gotBody["allow_auto_merge"])
	}
	// A declared null must reach the API: that is how a field gets cleared.
	value, present := gotBody["homepage"]
	if !present || value != nil {
		t.Errorf("body homepage = %v (present=%v), want an explicit null", value, present)
	}
}

func TestRepositoryApplyReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"message": "Validation Failed"}`)
	}))
	defer srv.Close()

	err := Repository{}.Apply(context.Background(), newTestClient(t, srv), Repo{Owner: "kota65535", Name: "ghs"}, map[string]any{"has_wiki": true})
	if err == nil {
		t.Fatal("Apply succeeded, want an error")
	}
}

func TestLookup(t *testing.T) {
	r, err := Lookup("repository")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if r.Name() != "repository" {
		t.Errorf("Name = %q, want repository", r.Name())
	}
	if _, err := Lookup("rulesets"); err == nil {
		t.Error("Lookup(\"rulesets\") succeeded, want an error while it is unimplemented")
	}
}
