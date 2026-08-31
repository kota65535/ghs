package resource

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/kota65535/ghs/internal/schema"
)

var testRepo = Repo{Owner: "kota65535", Name: "ghs"}

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

// recorder captures the requests a resource makes, and answers them from a
// table of canned responses keyed by "METHOD /path".
type recorder struct {
	t         *testing.T
	responses map[string]string
	status    map[string]int
	requests  []request
}

type request struct {
	method string
	path   string
	query  string
	body   map[string]any
}

type response struct {
	body   string
	status int
}

func newRecorder(t *testing.T, responses map[string]string) *recorder {
	return &recorder{t: t, responses: responses, status: map[string]int{}}
}

// fails makes one endpoint answer with a status, for the cases where what the
// API refuses matters as much as what it returns.
func (r *recorder) fails(call string, status int) *recorder {
	r.status[call] = status
	return r
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

		call := req.Method + " " + req.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if status, ok := r.status[call]; ok {
			w.WriteHeader(status)
			io.WriteString(w, `{"message": "refused"}`)
			return
		}
		if body, ok := r.responses[call]; ok {
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

// calls lists the requests as "METHOD /path", for asserting on their order.
func (r *recorder) calls() []string {
	out := make([]string, 0, len(r.requests))
	for _, req := range r.requests {
		out = append(out, req.method+" "+req.path)
	}
	return out
}

func TestPathWalksDownFromTheRepository(t *testing.T) {
	path := At(testRepo)
	if got := path.String(); got != "repos/kota65535/ghs" {
		t.Errorf("root path = %q, want the repository", got)
	}

	// The element names of a collection get into the path by being walked
	// through, which is how the variables of an environment are addressed.
	nested := path.Child("environments").Element("production").Child("variables")
	if got := nested.String(); got != "repos/kota65535/ghs/environments/production/variables" {
		t.Errorf("nested path = %q, want the environment's variables", got)
	}

	// A namespace adds its segment like any other node.
	if got := path.Child("actions").Child("permissions").String(); got != "repos/kota65535/ghs/actions/permissions" {
		t.Errorf("path = %q, want the permissions endpoint", got)
	}

	// Nothing to add leaves the path alone, which is what the root does.
	if got := path.Child("").String(); got != path.String() {
		t.Errorf("path = %q, want it unchanged by an empty segment", got)
	}
}

func TestPathEscapesElementNames(t *testing.T) {
	// Environment names allow characters that mean something in a URL.
	got := At(testRepo).Child("environments").Element("review/pr-1").String()
	if got != "repos/kota65535/ghs/environments/review%2Fpr-1" {
		t.Errorf("path = %q, want the name escaped", got)
	}
}

func TestSpecialCasesAreLookedUpByKey(t *testing.T) {
	// Most nodes need nothing said about them. The ones that do are the places
	// GitHub does not follow its own pattern.
	if _, generic := CollectionFor("rulesets").(GenericCollection); generic {
		t.Error("rulesets got the general treatment, want its own: it is addressed by a server-issued id")
	}
	if _, generic := CollectionFor("environments").(GenericCollection); generic {
		t.Error("environments got the general treatment, want its own: what it reports is not what declares it")
	}
	if _, generic := CollectionFor("actions.variables").(GenericCollection); !generic {
		t.Error("actions.variables did not get the general treatment, which is all it needs")
	}
	if _, generic := ObjectFor("actions.permissions").(GenericObject); !generic {
		t.Error("actions.permissions did not get the general treatment, which is all it needs")
	}
}

func TestGenericObjectReadsAndWritesTheNodesPath(t *testing.T) {
	node := schema.Node{Kind: schema.KindObject, Segment: "permissions", Method: http.MethodPut}
	path := At(testRepo).Child("actions").Child("permissions")

	t.Run("fetch", func(t *testing.T) {
		rec := newRecorder(t, map[string]string{
			"GET /repos/kota65535/ghs/actions/permissions": `{"enabled": true, "allowed_actions": "all"}`,
		})
		srv := rec.server()
		defer srv.Close()

		current, err := GenericObject{}.Fetch(context.Background(), newTestClient(t, srv), node, path)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if current["enabled"] != true || current["allowed_actions"] != "all" {
			t.Errorf("current = %+v, want what the API reported", current)
		}
	})

	t.Run("apply uses the method the description records", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		desired := map[string]any{"enabled": true}
		if err := (GenericObject{}).Apply(context.Background(), newTestClient(t, srv), node, path, desired); err != nil {
			t.Fatalf("Apply: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPut || got.path != "/repos/kota65535/ghs/actions/permissions" {
			t.Errorf("request = %s %s, want PUT on the node's path", got.method, got.path)
		}
		if got.body["enabled"] != true {
			t.Errorf("body = %+v, want the declared fields", got.body)
		}
	})
}

func TestGenericObjectTreatsAnInapplicableNodeAsAbsent(t *testing.T) {
	// The allowed actions are listed only while the policy is to select them,
	// and GitHub answers 409 otherwise. That is not a failure: there is nothing
	// there, and a field declared against it reads as a change against nothing.
	node := schema.Node{Segment: "selected-actions", Kind: schema.KindObject, Method: http.MethodPut, Conditional: true}
	path := At(testRepo).Child("actions").Child("permissions").Child("selected-actions")

	for _, status := range []int{http.StatusConflict, http.StatusUnprocessableEntity} {
		rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/actions/permissions/selected-actions", status)
		srv := rec.server()

		current, err := GenericObject{}.Fetch(context.Background(), newTestClient(t, srv), node, path)
		srv.Close()

		if err != nil {
			t.Errorf("status %d: Fetch: %v", status, err)
		}
		if current != nil {
			t.Errorf("status %d: current = %+v, want nothing", status, current)
		}
	}
}

func TestGenericObjectReportsRealFailures(t *testing.T) {
	// A conditional node answering 403 means the token cannot see it, which is
	// a different thing from the setting not applying.
	node := schema.Node{Segment: "selected-actions", Method: http.MethodPut, Conditional: true}
	path := At(testRepo).Child("actions").Child("permissions").Child("selected-actions")

	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/actions/permissions/selected-actions", http.StatusForbidden)
	srv := rec.server()
	defer srv.Close()

	if _, err := (GenericObject{}).Fetch(context.Background(), newTestClient(t, srv), node, path); err == nil {
		t.Fatal("Fetch succeeded, want the failure reported")
	}
}
