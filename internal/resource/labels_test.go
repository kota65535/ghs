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

// TestLabelsAreRegistered checks that labels get their own implementation
// rather than the general one, which they need because the create body and the
// update body are not the same shape.
func TestLabelsAreRegistered(t *testing.T) {
	if _, ours := CollectionFor("labels").(Labels); !ours {
		t.Error("labels got the general treatment, which sends a name PATCH has no field for")
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

	current, err := Labels{}.FetchAll(context.Background(), newTestClient(t, srv), labelsNode, labelsPath())
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

		if err := (Labels{}).Create(context.Background(), newTestClient(t, srv), labelsNode, labelsPath(), desired); err != nil {
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

	t.Run("update patches the label without its name", func(t *testing.T) {
		// A label name is free text, so it reaches the path escaped. It does not
		// reach the body at all: PATCH takes new_name, color and description, so
		// a name there would be a field the request does not have.
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "good first issue", "color": "ffffff"}
		if err := (Labels{}).Update(context.Background(), newTestClient(t, srv), labelsNode, labelsPath(), current, desired); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPatch || got.path != "/repos/kota65535/ghs/labels/good%20first%20issue" {
			t.Errorf("request = %s %s, want PATCH on the label", got.method, got.path)
		}
		if got.body["color"] != "7057ff" || got.body["description"] != "Good for newcomers" {
			t.Errorf("body = %+v, want the declared label", got.body)
		}
		if _, sent := got.body["name"]; sent {
			t.Errorf("body = %+v, want no name: the path carries it and PATCH renames with new_name", got.body)
		}
		if _, renamed := got.body["new_name"]; renamed {
			t.Errorf("body = %+v, want no new_name: the label keeps the name it has", got.body)
		}
	})

	t.Run("a renamed label is patched where GitHub has it", func(t *testing.T) {
		// What diff.PairRenames folds into an update arrives here as a
		// declaration whose name is not the reported one.
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"id": float64(1), "name": "bug", "color": "7057ff"}
		renamed := map[string]any{"name": "defect", "color": "7057ff"}
		if err := (Labels{}).Update(context.Background(), newTestClient(t, srv), labelsNode, labelsPath(), current, renamed); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodPatch || got.path != "/repos/kota65535/ghs/labels/bug" {
			t.Errorf("request = %s %s, want PATCH on the label as GitHub knows it", got.method, got.path)
		}
		if got.body["new_name"] != "defect" {
			t.Errorf("body = %+v, want the new name as new_name", got.body)
		}
		if _, sent := got.body["name"]; sent {
			t.Errorf("body = %+v, want no name: PATCH renames with new_name", got.body)
		}
	})

	t.Run("delete removes the label", func(t *testing.T) {
		rec := newRecorder(t, nil)
		srv := rec.server()
		defer srv.Close()

		current := map[string]any{"name": "wontfix", "color": "ffffff"}
		if err := (Labels{}).Delete(context.Background(), newTestClient(t, srv), labelsNode, labelsPath(), current); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		got := rec.only()
		if got.method != http.MethodDelete || got.path != "/repos/kota65535/ghs/labels/wontfix" {
			t.Errorf("request = %s %s, want DELETE on the label", got.method, got.path)
		}
	})
}
