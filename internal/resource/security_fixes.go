package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kota65535/ghs/internal/schema"
)

// AutomatedSecurityFixes manages Dependabot security updates, which GitHub
// turns on with PUT and off with DELETE on the same path. Neither request
// takes a body, so the enabled field the settings file declares is not sent
// anywhere: it chooses the method.
//
// Reading is ordinary, which is what the embedded GenericObject is for -- GET
// on the path answers {"enabled": ..., "paused": ...}, and the enabled of that
// is the field being compared.
type AutomatedSecurityFixes struct{ GenericObject }

// enabledField is the only field of the node, and the whole of what the
// endpoint has to say.
const enabledField = "enabled"

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
