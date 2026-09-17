package resource

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/kota65535/ghs/internal/schema"
)

// AutomatedSecurityFixes manages Dependabot security updates, which GitHub
// turns on with PUT and off with DELETE on the same path. Neither request
// takes a body, so the enabled field the settings file declares is not sent
// anywhere: it chooses the method.
//
// Reading is a GET that answers {"enabled": ..., "paused": ...}, which the
// embedded GenericObject does, except where Dependabot is not enabled for the
// repository at all and the answer is a 404 instead.
type AutomatedSecurityFixes struct{ GenericObject }

// enabledField is the only field of the node, and the whole of what the
// endpoint has to say.
const enabledField = "enabled"

// Fetch implements Object.
//
// A 404 here is an answer rather than a failed read: the API description gives
// it as "Not Found if Dependabot is not enabled for the repository", which is
// a repository whose security updates are off. Letting it through as an error
// would fail every plan and every `ghs init` against such a repository.
func (a AutomatedSecurityFixes) Fetch(ctx context.Context, c Client, node schema.Node, path Path) (map[string]any, error) {
	current, err := a.GenericObject.Fetch(ctx, c, node, path)
	if err != nil {
		if isNotFound(err) {
			return map[string]any{enabledField: false}, nil
		}
		return nil, err
	}
	return current, nil
}

// isNotFound reports an error that is the API saying there is nothing here,
// rather than the request having gone wrong.
func isNotFound(err error) bool {
	var httpErr *api.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

// Apply implements Object.
func (AutomatedSecurityFixes) Apply(ctx context.Context, c Client, node schema.Node, path Path, desired map[string]any) error {
	enabled, ok := desired[enabledField].(bool)
	if !ok {
		// The endpoint is an on and an off, so there is no request that stands
		// for anything else -- a null, which elsewhere means "clear this
		// field", has nothing to be sent as here.
		return fmt.Errorf("%s: %s must be true or false, got %v", path, enabledField, desired[enabledField])
	}

	method := http.MethodPut
	if !enabled {
		method = http.MethodDelete
	}
	if err := c.DoWithContext(ctx, method, path.String(), nil, nil); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}
