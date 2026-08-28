package schema

// extraFields patches gaps in the OpenAPI description: fields the API accepts
// but does not describe. The keys are the path down to the node, joined by
// dots, with the empty key standing for the repository itself.
//
// This is the only hand-maintained part of the description, and it is a
// statement of fact rather than of policy -- each entry has been confirmed
// against the live API. Keep it short. If it starts growing, the generation
// approach is the thing to revisit, not this list.
//
// An entry stays until the description catches up;
// TestExtraFieldsAreStillMissingFromTheDescription fails once one becomes
// redundant, which is the signal to delete it.
var extraFields = map[string]map[string]Field{
	"": {
		// PATCH /repos/{owner}/{repo} accepts has_discussions -- verified
		// against the API -- but neither the request body schema nor the REST
		// documentation lists it. The description does know the field: it
		// appears in POST /user/repos and in the repository response schema.
		"has_discussions": {Type: "boolean"},
	},
}

// root is what the rest of ghs sees: the generated description with the
// patched fields merged in, and with the name every collection element carries.
var root = build()

func build() Node {
	node := generated
	patch(&node, "")
	return node
}

// patch merges the hand-maintained fields into a node and everything below it,
// and gives every collection the name its elements are identified by.
//
// Where the description and the patch disagree, the description wins, so an
// entry that has been described in the meantime changes nothing.
func patch(node *Node, path string) {
	fields := make(map[string]Field, len(node.Fields))
	for name, field := range node.Fields {
		fields[name] = field
	}

	for name, field := range extraFields[path] {
		if _, described := fields[name]; described {
			continue
		}
		fields[name] = field
	}

	// Every element of a collection is identified by name, so name is a field
	// of one whether or not the request body has it. Creating an environment
	// takes the name in the path -- PUT
	// /repos/{owner}/{repo}/environments/{environment_name} -- which leaves it
	// out of the body the description is generated from.
	if node.IsCollection() {
		if _, described := fields[NameField]; !described {
			fields[NameField] = Field{Type: "string"}
		}
	}

	node.Fields = fields

	if len(node.Nodes) == 0 {
		return
	}
	children := make(map[string]Node, len(node.Nodes))
	for name, child := range node.Nodes {
		prefix := name
		if path != "" {
			prefix = path + "." + name
		}
		patch(&child, prefix)
		children[name] = child
	}
	node.Nodes = children
}

// NameField identifies an element of a collection. Every element carries it,
// whether the API takes it in the request body or in the path.
const NameField = "name"
