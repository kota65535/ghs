package resource

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var autolinksNode = schema.Node{
	Kind:        schema.KindCollection,
	Segment:     "autolinks",
	Method:      http.MethodPost,
	Conditional: true,
	Key:         "key_prefix",
	Fields: map[string]schema.Field{
		"key_prefix":      {Type: "string"},
		"url_template":    {Type: "string"},
		"is_alphanumeric": {Type: "boolean"},
	},
}

func autolinksPath() Path { return At(testRepo).Child("autolinks") }

func TestAutolinksAreKeyedByTheirPrefix(t *testing.T) {
	// Nothing about an autolink is called a name: what identifies one in the
	// file is the prefix it matches, and the id is GitHub's own.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/autolinks": `[
			{"id": 1, "key_prefix": "JIRA-", "url_template": "https://jira.example.com/browse/<num>", "is_alphanumeric": true},
			{"id": 2, "key_prefix": "TICKET-", "url_template": "https://tickets.example.com/<num>", "is_alphanumeric": false}
		]`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := Autolinks{}.FetchAll(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if len(current) != 2 {
		t.Fatalf("got %d elements, want 2: %+v", len(current), current)
	}
	autolink, ok := current["JIRA-"]
	if !ok {
		t.Fatalf("JIRA- is missing: %+v", current)
	}
	// The id is kept because it is what addresses the autolink later.
	if autolink[idField] != float64(1) {
		t.Errorf("id = %v, want 1", autolink[idField])
	}
}

func TestAutolinksAreAbsentWhereThePlanDoesNotHaveThem(t *testing.T) {
	// Autolinks are not part of GitHub Free, where the listing answers 403.
	// That is not a failure to read: there is nothing there, and a declared
	// autolink reads as a change against nothing rather than stopping the run
	// on every other setting in the file.
	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/autolinks", http.StatusForbidden)
	srv := rec.server()
	defer srv.Close()

	current, err := Autolinks{}.FetchAll(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if current != nil {
		t.Errorf("current = %+v, want nothing", current)
	}
}

func TestAutolinksReportOtherFailures(t *testing.T) {
	// A listing that fails for any other reason is a failure to read, and
	// treating it as an empty set would have the plan offer to create every
	// declared autolink over again.
	rec := newRecorder(t, nil).fails("GET /repos/kota65535/ghs/autolinks", http.StatusInternalServerError)
	srv := rec.server()
	defer srv.Close()

	if _, err := (Autolinks{}).FetchAll(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath()); err == nil {
		t.Fatal("FetchAll succeeded, want the failure reported")
	}
}

func TestAutolinksAreCreatedAndDeletedByID(t *testing.T) {
	desired := map[string]any{"key_prefix": "JIRA-", "url_template": "https://jira.example.com/browse/<num>"}
	current := map[string]any{"id": float64(7), "key_prefix": "JIRA-", "url_template": "https://old.example.com/<num>"}

	t.Run("create posts to the collection", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (Autolinks{}).Create(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath(), desired); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPost || got.path != "/repos/kota65535/ghs/autolinks" {
			t.Errorf("request = %s %s, want POST on the collection", got.method, got.path)
		}
	})

	t.Run("delete removes the id", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (Autolinks{}).Delete(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath(), current); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/autolinks/7" {
			t.Errorf("request = %s %s, want DELETE on the autolink id", got.method, got.path)
		}
	})

	t.Run("an autolink without an id cannot be addressed", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		err := Autolinks{}.Delete(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath(),
			map[string]any{"key_prefix": "JIRA-"})
		if err == nil {
			t.Fatal("Delete succeeded, want an error")
		}
		if len(rec.requests) != 0 {
			t.Errorf("made %d requests, want none", len(rec.requests))
		}
	})
}

func TestAutolinksUpdateDeletesAndCreates(t *testing.T) {
	// There is no endpoint that changes an autolink. Changing one means
	// removing it and putting the new one in its place, in that order: the
	// prefix identifies the autolink, so the old one has to be gone before the
	// new one can take it.
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	current := map[string]any{"id": float64(7), "key_prefix": "JIRA-", "url_template": "https://old.example.com/<num>", "is_alphanumeric": false}
	desired := map[string]any{"key_prefix": "JIRA-", "url_template": "https://jira.example.com/browse/<num>", "is_alphanumeric": false}

	if err := (Autolinks{}).Update(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath(), current, desired); err != nil {
		t.Fatalf("Update: %v", err)
	}

	want := []string{
		"DELETE /repos/kota65535/ghs/autolinks/7",
		"POST /repos/kota65535/ghs/autolinks",
	}
	if got := rec.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if body := rec.requests[1].body; body["url_template"] != "https://jira.example.com/browse/<num>" {
		t.Errorf("body = %+v, want the declared autolink", body)
	}
}

func TestAutolinksUpdateKeepsWhatTheFileDoesNotDeclare(t *testing.T) {
	// A create states the whole autolink, so recreating one from the
	// declaration alone would reset the fields the file leaves out -- a change
	// nobody wrote and the plan never reported. What is not declared is carried
	// over from what GitHub reports, which is what leaves it unmanaged.
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	current := map[string]any{"id": float64(7), "key_prefix": "JIRA-", "url_template": "https://old.example.com/<num>", "is_alphanumeric": false}
	desired := map[string]any{"key_prefix": "JIRA-", "url_template": "https://jira.example.com/browse/<num>"}

	if err := (Autolinks{}).Update(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath(), current, desired); err != nil {
		t.Fatalf("Update: %v", err)
	}

	body := rec.requests[1].body
	if body["is_alphanumeric"] != false {
		t.Errorf("is_alphanumeric = %v, want the reported value kept", body["is_alphanumeric"])
	}
	// The id belongs to GitHub, not to the settings file, so it is not sent
	// back.
	if _, sent := body[idField]; sent {
		t.Errorf("body = %+v, want the id left out", body)
	}
}

func TestAutolinksUpdateDoesNotCreateWhenTheDeleteFails(t *testing.T) {
	// A create against a prefix that is still taken would be refused, and a
	// second autolink is not what the file asked for either way.
	rec := newRecorder(t, nil).fails("DELETE /repos/kota65535/ghs/autolinks/7", http.StatusNotFound)
	srv := rec.server()
	defer srv.Close()

	current := map[string]any{"id": float64(7), "key_prefix": "JIRA-"}
	desired := map[string]any{"key_prefix": "JIRA-", "url_template": "https://jira.example.com/browse/<num>"}

	err := Autolinks{}.Update(context.Background(), newTestClient(t, srv), autolinksNode, autolinksPath(), current, desired)
	if err == nil {
		t.Fatal("Update succeeded, want the failure reported")
	}
	if got := rec.calls(); len(got) != 1 {
		t.Errorf("calls = %v, want the create left unsent", got)
	}
	if !strings.Contains(err.Error(), "JIRA-") {
		t.Errorf("error = %v, want it to name the autolink", err)
	}
}
