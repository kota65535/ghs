package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kota65535/ghs/internal/schema"
)

// Labels manages the repository's issue labels, which are read and written the
// way any collection is except for one thing: creating a label and updating one
// do not take the same body.
//
// POST on the collection takes `name`, `color` and `description`. PATCH on
// `labels/{name}` takes `new_name`, `color` and `description` -- the name it is
// changing travels in the path, and the body names a label only to rename it.
// Everything else the embedded GenericCollection already does.
type Labels struct{ GenericCollection }

// Update implements Collection.
//
// The declaration is shaped by the create body, because that is the operation
// the fields are generated from, so it carries the name. PATCH has no such
// field: the label being changed is named in the path, and the body names one
// only to rename it, under the field the schema states as Rename.
//
// The path is built from the reported element, which holds the name GitHub
// knows the label by. The two names differ only in a rename, which is what a
// declaration under a new name is read as where nothing else about the label
// changes -- see diff.PairRenames.
func (l Labels) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	target, err := l.ElementPath(node, path, current)
	if err != nil {
		return err
	}
	body := withoutName(desired)
	if now := desired[node.Match]; now != current[node.Match] {
		body[node.Rename] = now
	}
	if err := send(ctx, c, http.MethodPatch, target.String(), body); err != nil {
		return fmt.Errorf("update %s: %w", target, err)
	}
	return nil
}
