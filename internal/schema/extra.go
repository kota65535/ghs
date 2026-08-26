package schema

// extraFields patches gaps in the OpenAPI description: fields the API accepts
// but does not describe.
//
// This is the only hand-maintained part of the field definitions, and it is a
// statement of fact rather than of policy -- each entry has been confirmed
// against the live API. Keep it short. If it starts growing, the generation
// approach is the thing to revisit, not this list.
//
// An entry stays until the description catches up;
// TestExtraFieldsAreStillMissingFromTheDescription fails once one becomes
// redundant, which is the signal to delete it.
var extraFields = map[string]map[string]Field{
	"repository": {
		// PATCH /repos/{owner}/{repo} accepts has_discussions -- verified
		// against the API -- but neither the request body schema nor the REST
		// documentation lists it. The description does know the field: it
		// appears in POST /user/repos and in the repository response schema.
		"has_discussions": {Type: "boolean"},
	},
}

// resources is what the rest of ghs sees: the generated definitions with the
// patched fields merged in. Where the two disagree, the description wins, so
// an entry that has been described in the meantime changes nothing.
var resources = mergeExtraFields()

func mergeExtraFields() map[string]Resource {
	merged := make(map[string]Resource, len(generated))

	for name, resource := range generated {
		fields := make(map[string]Field, len(resource.Fields))
		for fieldName, field := range resource.Fields {
			fields[fieldName] = field
		}
		for fieldName, field := range extraFields[name] {
			if _, described := fields[fieldName]; described {
				continue
			}
			fields[fieldName] = field
		}

		resource.Fields = fields
		merged[name] = resource
	}
	return merged
}
