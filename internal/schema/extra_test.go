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

func TestExtraNodesAreMergedIn(t *testing.T) {
	for key, nodes := range extraNodes {
		parent, ok := nodeAt(key)
		if !ok {
			t.Errorf("%q holds hand-written nodes but is not a node", key)
			continue
		}
		for name, want := range nodes {
			got, ok := parent.Child(name)
			if !ok {
				t.Errorf("%s: %s was not merged in", or(key), name)
				continue
			}
			if got.Method != want.Method || got.Segment != want.Segment {
				t.Errorf("%s: %s is %s %q, want %s %q",
					or(key), name, got.Method, got.Segment, want.Method, want.Segment)
			}
		}
	}
}

func TestExtraNodesAreStillMissingFromTheDescription(t *testing.T) {
	// A hand-written node names a setting the generator could not reach. Once
	// the description gains an operation for it, the entry in gen/main.go is
	// the place for it and this one is stale.
	for key, nodes := range extraNodes {
		parent, ok := generatedAt(key)
		if !ok {
			continue
		}
		for name := range nodes {
			if _, described := parent.Child(name); described {
				t.Errorf("%s: %s is now generated; delete it from extraNodes", or(key), name)
			}
		}
	}
}

func TestEveryCollectionGainsItsNameField(t *testing.T) {
	// The field an element is identified by is added to every collection during
	// the merge, so a collection GitHub adds later gets one without anybody
	// remembering to say so. For nearly all of them that field is the name; an
	// autolink states its own, and the description already has it.
	var check func(node Node, where string)
	check = func(node Node, where string) {
		if node.IsCollection() {
			if _, ok := node.Field(node.KeyField()); !ok {
				t.Errorf("%s: collection has no %s field", where, node.KeyField())
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
