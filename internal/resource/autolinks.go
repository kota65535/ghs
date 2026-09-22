package resource

import (
	"context"
	"fmt"
	"maps"
	"net/http"

	"github.com/kota65535/ghs/internal/schema"
)

// Autolinks manages the repository's autolink references, which depart from
// the usual pattern twice.
//
// GitHub identifies one by the prefix it matches and addresses it by an id it
// issues, so the file matches on key_prefix while the requests go to the id.
// And there is no endpoint that changes an autolink: the API has a create and
// a delete and nothing in between, so an update is the two of them in turn.
type Autolinks struct{}

// FetchAll implements Collection.
//
// Autolinks belong to the paid plans, and a repository on GitHub Free answers
// 403 rather than with an empty list. That is not a failure to read: the
// repository has no autolinks to speak of, and reporting it as an error would
// stop a run over a key the file may not even declare. A declared autolink
// then reads as a change against nothing, which is what the plan says about
// any conditional path that is not there.
func (Autolinks) FetchAll(ctx context.Context, c Client, node schema.Node, path Path) (map[string]map[string]any, error) {
	elements, err := listElements(ctx, c, path, node.Segment)
	if err != nil {
		if node.Conditional && isForbidden(err) {
			return nil, nil
		}
		return nil, err
	}
	return byName(elements, node.KeyField(), node.Segment)
}

// Create implements Collection.
func (Autolinks) Create(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error {
	if err := send(ctx, c, http.MethodPost, path.String(), desired); err != nil {
		return fmt.Errorf("create autolink %v in %s: %w", desired[node.KeyField()], path, err)
	}
	return nil
}

// Update implements Collection by deleting the autolink and creating it again,
// which is the only way GitHub offers to change one.
//
// The delete comes first because the prefix is what identifies an autolink:
// GitHub refuses a second one on a prefix it already has. A failure between the
// two leaves the autolink missing rather than duplicated, and the next apply
// creates it.
func (a Autolinks) Update(ctx context.Context, c Client, node schema.Node, path Path, current, desired map[string]any) error {
	if err := a.Delete(ctx, c, node, path, current); err != nil {
		return err
	}
	return a.Create(ctx, c, node, path, replacing(node, current, desired))
}

// Delete implements Collection.
func (a Autolinks) Delete(ctx context.Context, c Client, node schema.Node, path Path, current map[string]any) error {
	target, err := a.ElementPath(path, current)
	if err != nil {
		return err
	}
	if err := deleteAt(ctx, c, target.String()); err != nil {
		return fmt.Errorf("delete autolink %v: %w", current[node.KeyField()], err)
	}
	return nil
}

// keyPrefix is what identifies an autolink, which the schema states as the
// node's key. It is spelled out here for the one place no node is at hand.
const keyPrefix = "key_prefix"

// ElementPath implements Collection, addressing an autolink by the id GitHub
// issued for it.
func (Autolinks) ElementPath(path Path, element map[string]any) (Path, error) {
	target, ok := pathByID(path, element)
	if !ok {
		return Path{}, fmt.Errorf("autolink %v has no usable id", element[keyPrefix])
	}
	return target, nil
}

// replacing is the body that puts a changed autolink back: what GitHub reports
// about it now, so far as the file could have declared it, with the declaration
// written over the top.
//
// It exists because a create states the whole autolink while the file states
// only what it manages. Recreating one from the declaration alone would send
// is_alphanumeric back to its default whenever the file changes a url_template
// and says nothing about it -- a change nobody wrote and the plan never
// reported. Carrying the rest over is what keeps a field the file leaves out
// unmanaged here as it is everywhere else.
func replacing(node schema.Node, current, desired map[string]any) map[string]any {
	body := make(map[string]any, len(node.Fields))
	for name := range node.Fields {
		if value, reported := current[name]; reported {
			body[name] = value
		}
	}
	maps.Copy(body, desired)
	return body
}
