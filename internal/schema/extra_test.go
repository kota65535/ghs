package schema

import "testing"

func TestExtraFieldsAreMergedIn(t *testing.T) {
	for resourceName, fields := range extraFields {
		resource, ok := Lookup(resourceName)
		if !ok {
			t.Errorf("%s is patched but not a registered resource", resourceName)
			continue
		}
		for name, want := range fields {
			got, ok := resource.Get(name)
			if !ok {
				t.Errorf("%s.%s was not merged into the field definitions", resourceName, name)
				continue
			}
			if got.Type != want.Type {
				t.Errorf("%s.%s has type %q, want %q", resourceName, name, got.Type, want.Type)
			}
		}
	}
}

func TestExtraFieldsAreStillMissingFromTheDescription(t *testing.T) {
	// Each patch exists because the description omits the field. Once the
	// description gains it, the entry is dead weight, and the merge stays
	// silent about that -- this test is what says so.
	for resourceName, fields := range extraFields {
		for name := range fields {
			if _, described := generated[resourceName].Fields[name]; described {
				t.Errorf("%s.%s is now in the generated definitions; delete it from extraFields",
					resourceName, name)
			}
		}
	}
}

func TestMergingDoesNotMutateTheGeneratedDefinitions(t *testing.T) {
	// The merge copies each field map; sharing them would let a patch leak
	// into the generated side and defeat the check above.
	for resourceName := range extraFields {
		merged, ok := Lookup(resourceName)
		if !ok {
			continue
		}
		if len(merged.Fields) <= len(generated[resourceName].Fields) {
			t.Errorf("%s: merged fields (%d) did not grow past the generated ones (%d)",
				resourceName, len(merged.Fields), len(generated[resourceName].Fields))
		}
	}
}
