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

// Renames implements Renamer.
//
// Deleting a label takes it off every issue and pull request that carried it,
// and creating one under the new name does not put it back, so a label
// declared under a new name is worth renaming rather than replacing. What
// counts as a rename is diff.PairRenames's decision; this reports only that
// the endpoint can carry one out.
func (Labels) Renames() {}

// Update implements Collection.
//
// The declaration is shaped by the create body, because that is the operation
// the fields are generated from, so it carries the name. PATCH has no such
// field: the label being changed is named in the path, and the body names one
// only to rename it, as new_name.
//
// The path is therefore built from the reported element rather than the
// declared one. The two hold the same name except in a rename, and there the
// label has still to be addressed as GitHub knows it.
func (l Labels) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	target, err := l.ElementPath(path, current)
	if err != nil {
		return err
	}

	body := withoutName(desired)
	was, err := nameOf(current)
	if err != nil {
		return err
	}
	if now, err := nameOf(desired); err == nil && now != was {
		body[newNameField] = now
	}

	if err := send(ctx, c, http.MethodPatch, target.String(), body); err != nil {
		return fmt.Errorf("update %s: %w", target, err)
	}
	return nil
}

// newNameField is what PATCH takes a label's new name as.
const newNameField = "new_name"
