package resource

import (
	"context"
	"net/http"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var securityFixesNode = schema.Node{
	Kind:    schema.KindObject,
	Segment: "automated-security-fixes",
	Method:  http.MethodPut,
}

func securityFixesPath() Path { return At(testRepo).Child("automated-security-fixes") }

func TestAutomatedSecurityFixesChoosesTheMethodFromTheDeclaredValue(t *testing.T) {
	// The endpoint takes no body, so the declared value is not sent: it is the
	// difference between the request that enables and the one that disables.
	for _, tc := range []struct {
		enabled bool
		method  string
	}{
		{enabled: true, method: http.MethodPut},
		{enabled: false, method: http.MethodDelete},
	} {
		rec := newRecorder(t, nil)
		srv := rec.server()

		err := AutomatedSecurityFixes{}.Apply(context.Background(), newTestClient(t, srv),
			securityFixesNode, securityFixesPath(), map[string]any{"enabled": tc.enabled})
		srv.Close()
		if err != nil {
			t.Fatalf("Apply(enabled=%v): %v", tc.enabled, err)
		}

		got := rec.only()
		if got.method != tc.method {
			t.Errorf("enabled=%v sent %s, want %s", tc.enabled, got.method, tc.method)
		}
		if got.path != "/repos/kota65535/ghs/automated-security-fixes" {
			t.Errorf("enabled=%v sent %s, want the automated-security-fixes path", tc.enabled, got.path)
		}
		if got.body != nil {
			t.Errorf("enabled=%v sent a body (%+v), want none", tc.enabled, got.body)
		}
	}
}

func TestAutomatedSecurityFixesRefusesAValueThatIsNotTrueOrFalse(t *testing.T) {
	// A null means "clear this field" elsewhere, and there is no request here
	// that stands for it. Failing says so rather than picking one of the two.
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := AutomatedSecurityFixes{}.Apply(context.Background(), newTestClient(t, srv),
		securityFixesNode, securityFixesPath(), map[string]any{"enabled": nil})
	if err == nil {
		t.Fatal("Apply(enabled=null) succeeded, want an error")
	}
	if len(rec.requests) != 0 {
		t.Errorf("made %d requests, want none: %+v", len(rec.requests), rec.requests)
	}
}

func TestAutomatedSecurityFixesReadsA404AsDisabled(t *testing.T) {
	// "Not Found if Dependabot is not enabled for the repository" is the API
	// describing a state, not a read that failed. Passing it on as an error
	// would fail every plan against such a repository.
	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/automated-security-fixes", http.StatusNotFound)
	srv := rec.server()
	defer srv.Close()

	current, err := AutomatedSecurityFixes{}.Fetch(context.Background(), newTestClient(t, srv),
		securityFixesNode, securityFixesPath())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if current["enabled"] != false {
		t.Errorf("current = %+v, want enabled false", current)
	}
}

func TestAutomatedSecurityFixesPassesOnAFailedRead(t *testing.T) {
	// Only the 404 is an answer. Anything else is the request going wrong, and
	// reporting it as "disabled" would plan a change against a state nobody
	// read.
	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/automated-security-fixes", http.StatusInternalServerError)
	srv := rec.server()
	defer srv.Close()

	_, err := AutomatedSecurityFixes{}.Fetch(context.Background(), newTestClient(t, srv),
		securityFixesNode, securityFixesPath())
	if err == nil {
		t.Fatal("Fetch succeeded on a 500, want the error passed on")
	}
}

func TestAutomatedSecurityFixesReadsTheEnabledFlag(t *testing.T) {
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/automated-security-fixes": `{"enabled": true, "paused": false}`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := AutomatedSecurityFixes{}.Fetch(context.Background(), newTestClient(t, srv),
		securityFixesNode, securityFixesPath())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if current["enabled"] != true {
		t.Errorf("current = %+v, want enabled read from the response", current)
	}
}
