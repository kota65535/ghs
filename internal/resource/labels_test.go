package resource

import (
	"context"
	"net/http"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var labelsNode = schema.Node{
	Kind:    schema.KindCollection,
	Segment: "labels",
	Method:  http.MethodPost,
}

func labelsPath() Path { return At(testRepo).Child("labels") }

// TestLabelsNeedNothingOfTheirOwn checks the assumption the labels node rests
// on: the endpoints follow GitHub's own pattern, so the general implementation
// is the whole of what reading and writing them takes.
func TestLabelsNeedNothingOfTheirOwn(t *testing.T) {
	if _, generic := CollectionFor("labels").(GenericCollection); !generic {
		t.Error("labels did not get the general treatment, which is all they need")
	}
}

func TestLabelsAreReadAsABareList(t *testing.T) {
	// A repository starts with the labels GitHub creates for it, and they are
	// listed bare rather than wrapped in an object.
	rec := newRecorder(t, map[string]string{
		"GET /repos/kota65535/ghs/labels": `[
			{"id": 1, "name": "bug", "color": "d73a4a", "description": "Something is not working"},
			{"id": 2, "name": "good first issue", "color": "7057ff", "description": "Good for newcomers"}
		]`,
	})
	srv := rec.server()
	defer srv.Close()

	current, err := GenericCollection{}.FetchAll(context.Background(), newTestClient(t, srv), labelsNode, labelsPath())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if len(current) != 2 {
		t.Fatalf("got %d labels, want 2: %+v", len(current), current)
	}
	if current["bug"]["color"] != "d73a4a" {
		t.Errorf("bug = %+v, want its reported color", current["bug"])
	}
}

func TestLabelsAreAddressedByName(t *testing.T) {
	desired := map[string]any{"name": "good first issue", "color": "7057ff", "description": "Good for newcomers"}

	t.Run("create posts to the collection", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		if err := (GenericCollection{}).Create(context.Background(), newTestClient(t, srv), labelsNode, labelsPath(), desired); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPost || got.path != "/repos/kota65535/ghs/labels" {
			t.Errorf("request = %s %s, want POST on the collection", got.method, got.path)
		}
		if got.body["color"] != "7057ff" {
			t.Errorf("body = %+v, want the declared label", got.body)
		}
	})

	t.Run("update patches the label", func(t *testing.T) {
		// A label name is free text, so it reaches the path escaped.
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "good first issue", "color": "ffffff"}
		if err := (GenericCollection{}).Update(context.Background(), newTestClient(t, srv), labelsNode, labelsPath(), current, desired); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPatch || got.path != "/repos/kota65535/ghs/labels/good%20first%20issue" {
			t.Errorf("request = %s %s, want PATCH on the label", got.method, got.path)
		}
		if got.body["color"] != "7057ff" {
			t.Errorf("body = %+v, want the declared label", got.body)
		}
	})

	t.Run("delete removes the label", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "wontfix", "color": "ffffff"}
		if err := (GenericCollection{}).Delete(context.Background(), newTestClient(t, srv), labelsNode, labelsPath(), current); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/labels/wontfix" {
			t.Errorf("request = %s %s, want DELETE on the label", got.method, got.path)
		}
	})
}
