package resource

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/kota65535/ghs/internal/schema"
)

var topicsNode = schema.Node{
	Kind:    schema.KindObject,
	Segment: "topics",
	Method:  http.MethodPut,
}

func TestTopicsNormalizesToWhatGitHubStores(t *testing.T) {
	// GitHub lowercases the names and returns them sorted whatever order they
	// were sent in. Comparing the declaration as written would report a
	// difference that apply sends and the next read undoes.
	desired := map[string]any{"names": []any{"Zebra", "alpha", "Mango"}}

	got := Topics{}.Normalize(topicsNode, desired)
	want := []any{"alpha", "mango", "zebra"}
	if !reflect.DeepEqual(got["names"], want) {
		t.Errorf("names = %+v, want %+v", got["names"], want)
	}

	// The declaration itself is untouched: what the file says stays what the
	// file says.
	if !reflect.DeepEqual(desired["names"], []any{"Zebra", "alpha", "Mango"}) {
		t.Errorf("the declaration was modified: %+v", desired["names"])
	}
}

func TestTopicsLeavesADeclarationItCannotOrderAlone(t *testing.T) {
	for _, desired := range []map[string]any{
		{},
		{"names": nil},
		{"names": "not-a-list"},
	} {
		got := Topics{}.Normalize(topicsNode, desired)
		if !reflect.DeepEqual(got, desired) {
			t.Errorf("Normalize(%+v) = %+v, want it left alone", desired, got)
		}
	}
}

func TestTopicsWritesTheWholeSet(t *testing.T) {
	rec := newRecorder(t, nil)
	srv := rec.server()
	defer srv.Close()

	err := Topics{}.Apply(context.Background(), newTestClient(t, srv), topicsNode,
		At(testRepo).Child("topics"), map[string]any{"names": []any{"cli", "go"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := rec.only()
	if got.method != http.MethodPut || got.path != "/repos/kota65535/ghs/topics" {
		t.Errorf("sent %s %s, want PUT the topics path", got.method, got.path)
	}
	if names, ok := got.body["names"].([]any); !ok || len(names) != 2 {
		t.Errorf("body = %+v, want the declared names", got.body)
	}
}
