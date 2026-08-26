package diff

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Format selects how a plan is written out.
type Format string

const (
	// FormatText is the human-readable output used on a terminal.
	FormatText Format = "text"
	// FormatMarkdown is meant to be posted as a pull request comment.
	FormatMarkdown Format = "markdown"
	// FormatJSON is meant to be consumed by other programs.
	FormatJSON Format = "json"
)

// ParseFormat converts a --format value into a Format.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatText, FormatMarkdown, FormatJSON:
		return Format(s), nil
	default:
		return "", fmt.Errorf("unknown format %q (want text, markdown or json)", s)
	}
}

// missingLabel stands in for a field the API response does not contain. It is
// deliberately not "null", which is a value GitHub can legitimately report.
const missingLabel = "(missing)"

// Option adjusts how a plan is rendered.
type Option func(*renderOptions)

type renderOptions struct {
	color bool
}

// WithColor colorizes the text format. Callers enable it only when writing to
// a terminal, so redirected output stays free of escape sequences.
func WithColor() Option {
	return func(o *renderOptions) { o.color = true }
}

// Render writes the changes in the requested format.
func Render(w io.Writer, changes []Change, format Format, opts ...Option) error {
	var options renderOptions
	for _, opt := range opts {
		opt(&options)
	}

	switch format {
	case FormatMarkdown:
		return renderMarkdown(w, changes)
	case FormatJSON:
		return renderJSON(w, changes)
	default:
		return renderText(w, changes, options)
	}
}

const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
)

func colorize(s, color string, enabled bool) string {
	if !enabled {
		return s
	}
	return color + s + ansiReset
}

func renderText(w io.Writer, changes []Change, options renderOptions) error {
	if len(changes) == 0 {
		_, err := fmt.Fprintln(w, "No changes. Settings match the declared configuration.")
		return err
	}

	// Changes are grouped under their resource so the output reads like the
	// settings file it came from.
	for _, group := range groupByResource(changes) {
		if _, err := fmt.Fprintln(w, group.resource); err != nil {
			return err
		}

		width := 0
		for _, c := range group.changes {
			if n := len(c.field); n > width {
				width = n
			}
		}

		for _, c := range group.changes {
			current := formatValue(c.change.Current, c.change.CurrentMissing)
			if c.change.CurrentMissing {
				current = colorize(current, ansiYellow, options.color)
			} else {
				current = colorize(current, ansiRed, options.color)
			}

			_, err := fmt.Fprintf(w, "  %s %-*s  %s => %s\n",
				colorize("~", ansiYellow, options.color),
				width, c.field,
				current,
				colorize(formatValue(c.change.Desired, false), ansiGreen, options.color))
			if err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "Plan: %s to change.\n", pluralize(len(changes)))
	return err
}

func renderMarkdown(w io.Writer, changes []Change) error {
	if _, err := fmt.Fprintln(w, "### ghs plan"); err != nil {
		return err
	}
	if len(changes) == 0 {
		_, err := fmt.Fprintln(w, "\nNo changes. Settings match the declared configuration.")
		return err
	}

	if _, err := fmt.Fprint(w, "\n| Field | Current | Desired |\n| --- | --- | --- |\n"); err != nil {
		return err
	}
	for _, c := range changes {
		_, err := fmt.Fprintf(w, "| `%s` | `%s` | `%s` |\n",
			c.Path, formatValue(c.Current, c.CurrentMissing), formatValue(c.Desired, false))
		if err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "\n**Plan: %s to change.**\n", pluralize(len(changes)))
	return err
}

// jsonChange is the wire form of a change. The field names are snake_case so
// the output reads like the settings file and the API it mirrors.
type jsonChange struct {
	Path           string `json:"path"`
	Current        any    `json:"current"`
	Desired        any    `json:"desired"`
	CurrentMissing bool   `json:"current_missing"`
}

type jsonPlan struct {
	Changes []jsonChange `json:"changes"`
	Summary struct {
		Changed int `json:"changed"`
	} `json:"summary"`
}

func renderJSON(w io.Writer, changes []Change) error {
	plan := jsonPlan{Changes: make([]jsonChange, 0, len(changes))}
	for _, c := range changes {
		plan.Changes = append(plan.Changes, jsonChange{
			Path:           c.Path,
			Current:        c.Current,
			Desired:        c.Desired,
			CurrentMissing: c.CurrentMissing,
		})
	}
	plan.Summary.Changed = len(changes)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

// formatValue renders a value the way it appears in settings.yml.
func formatValue(v any, missing bool) string {
	if missing {
		return missingLabel
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func pluralize(n int) string {
	if n == 1 {
		return "1 field"
	}
	return fmt.Sprintf("%d fields", n)
}

type resourceGroup struct {
	resource string
	changes  []struct {
		field  string
		change Change
	}
}

func groupByResource(changes []Change) []resourceGroup {
	order := []string{}
	byResource := map[string][]Change{}

	for _, c := range changes {
		resource, field := splitPath(c.Path)
		if _, seen := byResource[resource]; !seen {
			order = append(order, resource)
		}
		c.Path = field
		byResource[resource] = append(byResource[resource], c)
	}
	sort.Strings(order)

	groups := make([]resourceGroup, 0, len(order))
	for _, resource := range order {
		g := resourceGroup{resource: resource}
		for _, c := range byResource[resource] {
			field := c.Path
			c.Path = resource + "." + field
			g.changes = append(g.changes, struct {
				field  string
				change Change
			}{field: field, change: c})
		}
		groups = append(groups, g)
	}
	return groups
}

// splitPath separates the leading resource key from the rest of the path.
func splitPath(path string) (resource, field string) {
	if i := strings.Index(path, "."); i >= 0 {
		return path[:i], path[i+1:]
	}
	return path, path
}
