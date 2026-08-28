package schema

import (
	"strings"
	"testing"
)

// nodeAt walks down to a node by its dotted key, as extraFields writes it.
func nodeAt(key string) (Node, bool) {
	node := Root()
	if key == "" {
		return node, true
	}
	for _, name := range strings.Split(key, ".") {
		child, ok := node.Child(name)
		if !ok {
			return Node{}, false
		}
		node = child
	}
	return node, true
}

// generatedAt is nodeAt against the generated description, before the patches
// are merged in.
func generatedAt(key string) (Node, bool) {
	node := generated
	if key == "" {
		return node, true
	}
	for _, name := range strings.Split(key, ".") {
		child, ok := node.Child(name)
		if !ok {
			return Node{}, false
		}
		node = child
	}
	return node, true
}

func TestExtraFieldsAreMergedIn(t *testing.T) {
	for key, fields := range extraFields {
		node, ok := nodeAt(key)
		if !ok {
			t.Errorf("%q is patched but is not a node", key)
			continue
		}
		for name, want := range fields {
			got, ok := node.Field(name)
			if !ok {
				t.Errorf("%s: %s was not merged in", or(key), name)
				continue
			}
			if got.Type != want.Type {
				t.Errorf("%s: %s has type %q, want %q", or(key), name, got.Type, want.Type)
			}
		}
	}
}

func TestExtraFieldsAreStillMissingFromTheDescription(t *testing.T) {
	// Each patch exists because the description omits the field. Once the
	// description gains it, the entry is dead weight, and the merge stays
	// silent about that -- this test is what says so.
	for key, fields := range extraFields {
		node, ok := generatedAt(key)
		if !ok {
			continue
		}
		for name := range fields {
			if _, described := node.Field(name); described {
				t.Errorf("%s: %s is now in the generated description; delete it from extraFields",
					or(key), name)
			}
		}
	}
}

func TestMergingDoesNotMutateTheGeneratedDescription(t *testing.T) {
	// The merge copies each field map; sharing them would let a patch leak
	// into the generated side and defeat the check above.
	for key := range extraFields {
		merged, ok := nodeAt(key)
		if !ok {
			continue
		}
		before, ok := generatedAt(key)
		if !ok {
			continue
		}
		if len(merged.Fields) <= len(before.Fields) {
			t.Errorf("%s: merged fields (%d) did not grow past the generated ones (%d)",
				or(key), len(merged.Fields), len(before.Fields))
		}
	}
}

func TestEveryCollectionGainsItsNameField(t *testing.T) {
	// The name is added to every collection during the merge, so a collection
	// GitHub adds later gets one without anybody remembering to say so.
	var check func(node Node, where string)
	check = func(node Node, where string) {
		if node.IsCollection() {
			if _, ok := node.Field(NameField); !ok {
				t.Errorf("%s: collection has no %s field", where, NameField)
			}
		}
		for _, name := range node.ChildNames() {
			child, _ := node.Child(name)
			check(child, where+name)
		}
	}
	check(Root(), "")
}

func or(key string) string {
	if key == "" {
		return "the repository"
	}
	return key
}
