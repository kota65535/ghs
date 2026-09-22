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
// the fields are generated from, so it carries the name. Sending it to PATCH
// would declare a field that request has no such thing as. Renaming is not what
// it would ask for either: `name` is what an element is matched on, so a label
// declared under a new name is a different element, created and deleted like
// any other, and `new_name` has no part to play in it.
func (l Labels) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	target, err := l.ElementPath(path, desired)
	if err != nil {
		return err
	}
	if err := send(ctx, c, http.MethodPatch, target.String(), withoutName(desired)); err != nil {
		return fmt.Errorf("update %s: %w", target, err)
	}
	return nil
}
