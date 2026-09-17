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
		"has_discussions": {
			Type:        "boolean",
			Description: "Either `true` to enable discussions for this repository or `false` to disable them.",
		},
	},
}

// extraNodes describes settings the generator has nothing to generate from:
// endpoints that take no request body at all, so the field that stands for
// them is ghs's own invention rather than the API's.
//
// The keys are the path down to the parent of each node, as in extraFields.
//
// This is a heavier statement than a patched field -- a generated node states
// what the API accepts, one written here states what ghs decided to call it --
// so an entry needs an endpoint whose whole shape is the on/off it is written
// as. Anything richer belongs in operations in gen/main.go instead.
var extraNodes = map[string]map[string]Node{
	"": {
		// Dependabot security updates are turned on with PUT and off with
		// DELETE on /repos/{owner}/{repo}/automated-security-fixes, neither of
		// which takes a body, so there is no request schema to generate a
		// field from. The repository response reports the setting under
		// security_and_analysis.dependabot_security_updates, but PATCH
		// /repos/{owner}/{repo} ignores it there.
		//
		// Method is the enabling one. Which of the two a change actually sends
		// follows from the declared value, which is what
		// resource.AutomatedSecurityFixes is for.
		"automated-security-fixes": {
			Kind:    KindObject,
			Segment: "automated-security-fixes",
			Method:  "PUT",
			Summary: "Enable Dependabot security updates",
			Fields: map[string]Field{
				"enabled": {
					Type:        "boolean",
					Description: "Either `true` to enable Dependabot security updates for this repository, or `false` to disable them.",
					Default:     false,
				},
			},
		},

		// Private vulnerability reporting is the same pair of bodyless
		// requests on /repos/{owner}/{repo}/private-vulnerability-reporting,
		// and the plainest of them to read: GET answers 200 with the flag, so
		// resource.PrivateVulnerabilityReporting says nothing about reading.
		//
		// Not conditional, though the description gives the endpoint a 422 as
		// well. That 422 is the shared bad_request response rather than a way
		// of saying the setting has no place here, and conditional handling
		// would turn it into an empty current state: init would leave the
		// setting out, plan would report a change against nothing, and only
		// apply would fail. A failed read is worth failing on.
		"private-vulnerability-reporting": {
			Kind:    KindObject,
			Segment: "private-vulnerability-reporting",
			Method:  "PUT",
			Summary: "Enable private vulnerability reporting for a repository",
			Fields: map[string]Field{
				"enabled": {
					Type:        "boolean",
					Description: "Either `true` to allow security researchers to report vulnerabilities privately through this repository, or `false` to disallow it.",
					Default:     false,
				},
			},
		},

		// Dependabot alerts are the same pair of bodyless requests on
		// /repos/{owner}/{repo}/vulnerability-alerts. This one is not reported
		// in the repository response at all: what it is now is the answer to a
		// GET on the path, which is a 204 or a 404 rather than a field.
		// resource.VulnerabilityAlerts reads that.
		"vulnerability-alerts": {
			Kind:    KindObject,
			Segment: "vulnerability-alerts",
			Method:  "PUT",
			Summary: "Enable vulnerability alerts",
			Fields: map[string]Field{
				"enabled": {
					Type:        "boolean",
					Description: "Either `true` to enable Dependabot alerts for this repository, or `false` to disable them.",
					Default:     false,
				},
			},
		},
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
			fields[NameField] = Field{
				Type:        "string",
				Description: "The name this entry is identified by. An entry the file does not declare is deleted.",
			}
		}
	}

	node.Fields = fields

	// The hand-written nodes join the generated ones before the walk goes on,
	// so that one of them is patched like any other node below this point.
	if extra := extraNodes[path]; len(extra) > 0 {
		nodes := make(map[string]Node, len(node.Nodes)+len(extra))
		for name, child := range node.Nodes {
			nodes[name] = child
		}
		for name, child := range extra {
			if _, described := nodes[name]; described {
				continue
			}
			nodes[name] = child
		}
		node.Nodes = nodes
	}

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
