package schema

import "testing"

// at walks down to a node by the keys leading to it, failing when the path
// does not exist.
func at(t *testing.T, keys ...string) Node {
	t.Helper()

	node := Root()
	for i, key := range keys {
		child, ok := node.Child(key)
		if !ok {
			t.Fatalf("%v is not a node; run go generate ./...", keys[:i+1])
		}
		node = child
	}
	return node
}

func TestRootIsTheRepository(t *testing.T) {
	root := Root()

	if root.Method != "PATCH" {
		t.Errorf("Method = %q, want PATCH: the top level of the file is the repository", root.Method)
	}
	// The root is where paths are measured from, so it adds nothing to one.
	if root.Segment != "" {
		t.Errorf("Segment = %q, want it empty", root.Segment)
	}

	// A sample of the fields the request body declares. These are what the
	// settings file is expected to manage day to day.
	for _, field := range []string{"has_issues", "delete_branch_on_merge", "squash_merge_commit_title", "security_and_analysis"} {
		if _, ok := root.Field(field); !ok {
			t.Errorf("%s is missing from the root fields", field)
		}
	}

	// Fields that only ever appear in responses must not be writable.
	for _, field := range []string{"id", "full_name", "owner", "node_id", "pushed_at"} {
		if _, ok := root.Field(field); ok {
			t.Errorf("%s is writable according to the description, want it absent", field)
		}
	}
}

func TestPathsBecomeKeys(t *testing.T) {
	// Every key names the path segment it stands for, so finding where a
	// setting goes is a matter of reading the endpoint that changes it.
	for _, tc := range []struct {
		keys    []string
		segment string
		method  string
		kind    Kind
	}{
		{[]string{"actions"}, "actions", "", KindNamespace},
		{[]string{"actions", "permissions"}, "permissions", "PUT", KindObject},
		{[]string{"actions", "permissions", "workflow"}, "workflow", "PUT", KindObject},
		{[]string{"actions", "variables"}, "variables", "POST", KindCollection},
		{[]string{"rulesets"}, "rulesets", "POST", KindCollection},
		{[]string{"environments"}, "environments", "PUT", KindCollection},
		{[]string{"environments", "variables"}, "variables", "POST", KindCollection},
	} {
		node := at(t, tc.keys...)
		if node.Segment != tc.segment {
			t.Errorf("%v: Segment = %q, want %q", tc.keys, node.Segment, tc.segment)
		}
		if node.Method != tc.method {
			t.Errorf("%v: Method = %q, want %q", tc.keys, node.Method, tc.method)
		}
		if node.Kind != tc.kind {
			t.Errorf("%v: Kind = %q, want %q", tc.keys, node.Kind, tc.kind)
		}
	}
}

func TestANamespaceHoldsNoFields(t *testing.T) {
	// GitHub has nothing at /repos/{owner}/{repo}/actions, so there is nothing
	// to write there. The key exists because the settings below it are reached
	// through that segment.
	actions := at(t, "actions")

	if !actions.IsNamespace() {
		t.Errorf("Kind = %q, want %q", actions.Kind, KindNamespace)
	}
	if actions.Writable() {
		t.Error("Writable() = true, want false: a namespace has no operation")
	}
	if len(actions.Fields) != 0 {
		t.Errorf("Fields = %v, want none", actions.Fields)
	}
	if len(actions.Nodes) == 0 {
		t.Error("Nodes is empty, want the settings reached through this segment")
	}
}

func TestActionsSettingsAreSplitByEndpoint(t *testing.T) {
	// The fields sit under the path that writes them, so the name of each one
	// keeps the context the endpoint gives it. "days" on its own says nothing
	// about what it counts; under artifact-and-log-retention it does.
	for _, tc := range []struct {
		keys  []string
		field string
	}{
		{[]string{"actions", "permissions"}, "enabled"},
		{[]string{"actions", "permissions"}, "allowed_actions"},
		{[]string{"actions", "permissions", "workflow"}, "default_workflow_permissions"},
		{[]string{"actions", "permissions", "fork-pr-contributor-approval"}, "approval_policy"},
		{[]string{"actions", "permissions", "selected-actions"}, "github_owned_allowed"},
		{[]string{"actions", "permissions", "artifact-and-log-retention"}, "days"},
	} {
		if _, ok := at(t, tc.keys...).Field(tc.field); !ok {
			t.Errorf("%v: %s is missing", tc.keys, tc.field)
		}
	}
}

func TestConditionalPathsAreMarked(t *testing.T) {
	// The allowed actions are listed only while the policy is to select them,
	// and GitHub answers 409 otherwise. Marking the node is what lets a read
	// tell that apart from a failure.
	if !at(t, "actions", "permissions", "selected-actions").Conditional {
		t.Error("selected-actions is not marked conditional")
	}
	if at(t, "actions", "permissions").Conditional {
		t.Error("permissions is marked conditional, want it always present")
	}
}

func TestCollectionElementsCarryAName(t *testing.T) {
	// Creating an environment takes its name in the path, so the request body
	// the description is generated from has no name. Elements are matched by
	// name, so declaring one has to be allowed.
	for _, keys := range [][]string{
		{"environments"},
		{"rulesets"},
		{"actions", "variables"},
		{"environments", "variables"},
	} {
		node := at(t, keys...)
		if !node.IsCollection() {
			t.Errorf("%v: Kind = %q, want %q", keys, node.Kind, KindCollection)
		}
		name, ok := node.Field(NameField)
		if !ok {
			t.Errorf("%v: %s is missing", keys, NameField)
			continue
		}
		if name.Type != "string" {
			t.Errorf("%v: %s has type %q, want string", keys, NameField, name.Type)
		}
	}
}

func TestReferencedSchemasAreFollowed(t *testing.T) {
	// The ruleset request body shares schemas through $ref. Without following
	// them the enum and the nested fields are lost, and settings the API
	// rejects would pass validation.
	rulesets := at(t, "rulesets")

	enforcement, _ := rulesets.Field("enforcement")
	if len(enforcement.Enum) == 0 {
		t.Error("rulesets.enforcement has no enum, want the accepted values")
	}

	conditions, _ := rulesets.Field("conditions")
	refName, ok := conditions.Fields["ref_name"]
	if !ok {
		t.Fatal("rulesets.conditions.ref_name is missing")
	}
	if _, ok := refName.Fields["include"]; !ok {
		t.Error("rulesets.conditions.ref_name.include is missing")
	}

	// A schema built from oneOf declares no properties of its own, so a rule
	// stays free-form: which fields each variant takes is left to the API.
	rules, _ := rulesets.Field("rules")
	if rules.Type != "array" {
		t.Errorf("rulesets.rules has type %q, want array", rules.Type)
	}
}

func TestEnumsAndNestedFieldsAreCaptured(t *testing.T) {
	root := Root()

	title, _ := root.Field("squash_merge_commit_title")
	if len(title.Enum) == 0 {
		t.Error("squash_merge_commit_title has no enum, want the accepted values")
	}

	analysis, _ := root.Field("security_and_analysis")
	if len(analysis.Fields) == 0 {
		t.Fatal("security_and_analysis has no nested fields, want them captured for validation")
	}
	if _, ok := analysis.Fields["secret_scanning"]; !ok {
		t.Error("security_and_analysis.secret_scanning is missing")
	}
}

func TestNoFieldIsAlsoAKey(t *testing.T) {
	// A key naming a node and a field of the same name would leave no way to
	// tell them apart in the file. The generator rejects it; this guards the
	// merged description, patches included.
	var check func(node Node, where string)
	check = func(node Node, where string) {
		for _, name := range node.ChildNames() {
			if _, taken := node.Field(name); taken {
				t.Errorf("%s%s is both a field and a key", where, name)
			}
			child, _ := node.Child(name)
			check(child, where+name+".")
		}
	}
	check(Root(), "")
}
