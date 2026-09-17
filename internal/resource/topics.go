package resource

import (
	"sort"
	"strings"

	"github.com/kota65535/ghs/internal/schema"
)

// Topics manages the repository's topics, which GitHub rewrites as it stores
// them: the names are lowercased, and they come back sorted whatever order
// they were sent in.
//
// Reading and writing are otherwise ordinary, which is what the embedded
// GenericObject is for -- GET on the path answers {"names": [...]}, and PUT
// replaces the set.
type Topics struct{ GenericObject }

// namesField holds the topics, and is the whole of what the endpoint takes.
const namesField = "names"

// Normalize implements Object.
//
// Without it a repository whose topics are declared as `[Go, cli]` differs
// from itself forever: apply sends that, GitHub stores `[cli, go]`, and the
// next plan offers to send `[Go, cli]` again.
func (Topics) Normalize(node schema.Node, desired map[string]any) map[string]any {
	declared, ok := desired[namesField].([]any)
	if !ok {
		// Nothing to put in order: either the field is not declared, or it is
		// not a list, which the comparison reports as the difference it is.
		return desired
	}

	names := make([]any, 0, len(declared))
	for _, name := range declared {
		if text, ok := name.(string); ok {
			names = append(names, strings.ToLower(text))
			continue
		}
		// A topic that is not a string is left as it is: what it differs from
		// is the comparison's business, not this one's.
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, aok := names[i].(string)
		b, bok := names[j].(string)
		if !aok || !bok {
			return false
		}
		return a < b
	})

	normalized := make(map[string]any, len(desired))
	for key, value := range desired {
		normalized[key] = value
	}
	normalized[namesField] = names
	return normalized
}
