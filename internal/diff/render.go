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

// NoChangesMessage is what every format reports when nothing differs.
const NoChangesMessage = "No changes. Settings match the declared configuration."

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

// Render writes the plan in the requested format.
func Render(w io.Writer, plan *Plan, format Format, opts ...Option) error {
	var options renderOptions
	for _, opt := range opts {
		opt(&options)
	}

	plan = plan.Prune()

	switch format {
	case FormatMarkdown:
		return renderMarkdown(w, plan)
	case FormatJSON:
		return renderJSON(w, plan)
	default:
		return renderText(w, plan, options)
	}
}

const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
)

func colorize(s, color string, enabled bool) string {
	if !enabled || color == "" {
		return s
	}
	return color + s + ansiReset
}

// marker is the sign a change is listed under, following the convention of
// diffs and of comparable tools.
func marker(action Action) (sign, color string) {
	switch action {
	case ActionCreate:
		return "+", ansiGreen
	case ActionDelete:
		return "-", ansiRed
	default:
		return "~", ansiYellow
	}
}

// bodyIndent is how far the contents of a node are set in from the key naming
// it, and closeIndent how far the bracket that ends a block is. Nested blocks
// repeat the pair, so a bracket lines up under what it closes.
const (
	bodyIndent  = "    "
	closeIndent = "  "
)

// noSign takes the place of a sign on a line that is there for context rather
// than because it changed, keeping such a line in step with the ones around it.
const noSign = " "

func renderText(w io.Writer, plan *Plan, options renderOptions) error {
	if plan == nil {
		_, err := fmt.Fprintln(w, NoChangesMessage)
		return err
	}

	if err := writeBody(w, plan, "", options); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\nPlan: %s.\n", plan.Summarize())
	return err
}

// writeBody writes a node's own changes and then the nodes below it, which is
// the order the settings file puts them in.
func writeBody(w io.Writer, plan *Plan, indent string, options renderOptions) error {
	if plan.Collection {
		return writeElements(w, plan.Elements, indent, options)
	}

	if err := writeFieldChanges(w, plan.Fields, indent, fieldWidth(plan.Fields, nil), options); err != nil {
		return err
	}
	return writeChildren(w, plan.Children, indent, options)
}

// writeChildren writes each node below this one under the key that names it.
func writeChildren(w io.Writer, children []*Plan, indent string, options renderOptions) error {
	for _, child := range children {
		sign := colorize("~", ansiYellow, options.color)

		if child.Collection {
			if _, err := fmt.Fprintf(w, "%s%s %s: [\n", indent, sign, child.Name); err != nil {
				return err
			}
			if err := writeElements(w, child.Elements, indent+bodyIndent, options); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s%s]\n", indent, closeIndent); err != nil {
				return err
			}
			continue
		}

		if _, err := fmt.Fprintf(w, "%s%s %s:\n", indent, sign, child.Name); err != nil {
			return err
		}
		if err := writeBody(w, child, indent+bodyIndent, options); err != nil {
			return err
		}
	}
	return nil
}

// writeElements writes the elements of a collection as the sequence the
// settings file writes them as.
func writeElements(w io.Writer, elements []ElementDiff, indent string, options renderOptions) error {
	for _, element := range elements {
		sign, color := marker(element.Action)
		if _, err := fmt.Fprintf(w, "%s%s {\n", indent, colorize(sign, color, options.color)); err != nil {
			return err
		}

		body := indent + bodyIndent
		if element.Values != nil {
			// An element arriving or going takes all of its fields with it, so
			// they are listed out: which fields those are is the thing to
			// review.
			if err := writeValues(w, element.Values, sign, color, body, options); err != nil {
				return err
			}
		} else if err := writeElementChanges(w, element, body, options); err != nil {
			return err
		}

		if err := writeChildren(w, element.Children, body, options); err != nil {
			return err
		}

		if _, err := fmt.Fprintf(w, "%s%s},\n", indent, closeIndent); err != nil {
			return err
		}
	}
	return nil
}

// writeElementChanges writes the changed fields of an element, led by the name
// that says which element it is.
//
// The name is written without a sign, as the context it is: it identifies the
// element rather than being part of what changes about it.
func writeElementChanges(w io.Writer, element ElementDiff, indent string, options renderOptions) error {
	width := fieldWidth(element.Fields, []string{NameField})

	_, err := fmt.Fprintf(w, "%s%s %-*s %s\n",
		indent, noSign, width, NameField+":", formatValue(element.Name, false))
	if err != nil {
		return err
	}
	return writeFieldChanges(w, element.Fields, indent, width, options)
}

// writeFieldChanges writes one line per changed field, naming the field, the
// value it holds and the value it is to hold.
func writeFieldChanges(w io.Writer, fields []Change, indent string, width int, options renderOptions) error {
	for _, change := range fields {
		// A value that exists on only one side is written under the sign for
		// what happens to it, with nothing on the other side of an arrow to
		// read against. A list entry being dropped is the usual case.
		if sign, color, value, only := oneSided(change); only {
			label := fmt.Sprintf("%s%s %-*s ", indent, colorize(sign, color, options.color), width, change.Label+":")
			if err := writeValue(w, label, value, sign, color, indent, "", options); err != nil {
				return err
			}
			continue
		}

		_, err := fmt.Fprintf(w, "%s%s %-*s %s -> %s\n",
			indent,
			colorize("~", ansiYellow, options.color),
			width, change.Label+":",
			colorize(formatValue(change.Current, false), ansiRed, options.color),
			colorize(formatValue(change.Desired, false), ansiGreen, options.color))
		if err != nil {
			return err
		}
	}
	return nil
}

// fieldWidth is how wide the labels are once the colon that follows them is
// counted, which is what the values are lined up against.
func fieldWidth(fields []Change, extra []string) int {
	width := 0
	for _, label := range extra {
		if n := len(label) + len(":"); n > width {
			width = n
		}
	}
	for _, change := range fields {
		if n := len(change.Label) + len(":"); n > width {
			width = n
		}
	}
	return width
}

// oneSided reports a change that has a value on one side of the comparison
// only, along with how to write it.
func oneSided(c Change) (sign, color string, value any, ok bool) {
	switch {
	case c.CurrentMissing:
		return "+", ansiGreen, c.Desired, true
	case c.DesiredMissing:
		return "-", ansiRed, c.Current, true
	default:
		return "", "", nil, false
	}
}

// writeValues lists the fields of an element that is being created or deleted,
// opening out nested objects and lists rather than printing them as one long
// line. Every line carries the element's own sign: all of it is arriving, or
// all of it is going.
func writeValues(w io.Writer, values map[string]any, sign, color, indent string, options renderOptions) error {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	width := 0
	for _, name := range names {
		if n := len(name) + len(":"); n > width {
			width = n
		}
	}

	for _, name := range names {
		label := fmt.Sprintf("%s%s %-*s ", indent, colorize(sign, color, options.color), width, name+":")
		if err := writeValue(w, label, values[name], sign, color, indent, "", options); err != nil {
			return err
		}
	}
	return nil
}

// writeValue writes one value after the label that introduces it, going into
// objects and lists so that what they hold can be read a field at a time.
//
// The suffix trails whatever the value turns out to be, which is how a list
// entry gets its comma however deeply the entry itself nests.
func writeValue(w io.Writer, label string, value any, sign, color, indent, suffix string, options renderOptions) error {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			_, err := fmt.Fprintf(w, "%s{}%s\n", label, suffix)
			return err
		}
		// A block does not line up with anything, since what follows it is on
		// the next line. Padding it out would only leave a gap before the
		// bracket.
		if _, err := fmt.Fprintf(w, "%s{\n", unpad(label)); err != nil {
			return err
		}
		if err := writeValues(w, v, sign, color, indent+bodyIndent, options); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "%s%s}%s\n", indent, closeIndent, suffix)
		return err

	case []any:
		if len(v) == 0 {
			_, err := fmt.Fprintf(w, "%s[]%s\n", label, suffix)
			return err
		}
		if _, err := fmt.Fprintf(w, "%s[\n", unpad(label)); err != nil {
			return err
		}
		// List entries have no name to be introduced by, so the sign stands
		// in its place.
		entryLabel := fmt.Sprintf("%s%s%s ", indent, bodyIndent, colorize(sign, color, options.color))
		for _, entry := range v {
			if err := writeValue(w, entryLabel, entry, sign, color, indent+bodyIndent, ",", options); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(w, "%s%s]%s\n", indent, closeIndent, suffix)
		return err

	default:
		_, err := fmt.Fprintf(w, "%s%s%s\n", label, colorize(formatValue(value, false), color, options.color), suffix)
		return err
	}
}

// unpad drops the padding a label was given to line its value up with the
// labels around it, leaving the single space that separates the two.
//
// A label of nothing but space is indentation rather than padding -- it
// introduces an entry of a list, which has no name to be padded against -- and
// is left as it is.
func unpad(label string) string {
	trimmed := strings.TrimRight(label, " ")
	if trimmed == "" {
		return label
	}
	return trimmed + " "
}

func renderMarkdown(w io.Writer, plan *Plan) error {
	if _, err := fmt.Fprintln(w, "### ghs plan"); err != nil {
		return err
	}
	if plan == nil {
		_, err := fmt.Fprintf(w, "\n%s\n", NoChangesMessage)
		return err
	}

	if _, err := fmt.Fprint(w, "\n| Action | Path | Current | Desired |\n| --- | --- | --- | --- |\n"); err != nil {
		return err
	}
	for _, row := range flatten(plan) {
		_, err := fmt.Fprintf(w, "| %s | `%s` | `%s` | `%s` |\n",
			row.Action, row.Path,
			formatValue(row.Current, row.CurrentMissing),
			formatValue(row.Desired, row.DesiredMissing))
		if err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "\n**Plan: %s.**\n", plan.Summarize())
	return err
}

// jsonChange is the wire form of a change. The field names are snake_case so
// the output reads like the settings file and the API it mirrors.
type jsonChange struct {
	Action         Action `json:"action"`
	Path           string `json:"path"`
	Current        any    `json:"current"`
	Desired        any    `json:"desired"`
	CurrentMissing bool   `json:"current_missing"`
	DesiredMissing bool   `json:"desired_missing"`
}

type jsonPlan struct {
	Changes []jsonChange `json:"changes"`
	Summary struct {
		Created int `json:"created"`
		Changed int `json:"changed"`
		Deleted int `json:"deleted"`
	} `json:"summary"`
}

func renderJSON(w io.Writer, plan *Plan) error {
	rows := flatten(plan)
	out := jsonPlan{Changes: make([]jsonChange, 0, len(rows))}
	for _, row := range rows {
		out.Changes = append(out.Changes, jsonChange{
			Action:         row.Action,
			Path:           row.Path,
			Current:        row.Current,
			Desired:        row.Desired,
			CurrentMissing: row.CurrentMissing,
			DesiredMissing: row.DesiredMissing,
		})
	}

	summary := plan.Summarize()
	out.Summary.Created = summary.Created
	out.Summary.Changed = summary.Changed
	out.Summary.Deleted = summary.Deleted

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// row is one line of the flat formats: a change with the full path to where it
// sits, which is what a machine or a table needs.
type row struct {
	Action         Action
	Path           string
	Current        any
	Desired        any
	CurrentMissing bool
	DesiredMissing bool
}

// flatten walks a plan into one row per change, for the formats that have no
// nesting to put them in.
func flatten(plan *Plan) []row {
	var rows []row
	if plan == nil {
		return rows
	}

	for _, change := range plan.Fields {
		rows = append(rows, row{
			// A change to a field is always an update: what is created or
			// deleted is a whole element, reported below.
			Action:         ActionUpdate,
			Path:           join(plan.Path, change.Label),
			Current:        change.Current,
			Desired:        change.Desired,
			CurrentMissing: change.CurrentMissing,
			DesiredMissing: change.DesiredMissing,
		})
	}

	for _, element := range plan.Elements {
		if element.Values != nil {
			value := element.Values
			r := row{Action: element.Action, Path: element.Path}
			if element.Action == ActionDelete {
				r.Current = value
			} else {
				r.Desired = value
			}
			rows = append(rows, r)
			continue
		}
		for _, change := range element.Fields {
			rows = append(rows, row{
				Action:         ActionUpdate,
				Path:           join(element.Path, change.Label),
				Current:        change.Current,
				Desired:        change.Desired,
				CurrentMissing: change.CurrentMissing,
				DesiredMissing: change.DesiredMissing,
			})
		}
		for _, child := range element.Children {
			rows = append(rows, flatten(child)...)
		}
	}

	for _, child := range plan.Children {
		rows = append(rows, flatten(child)...)
	}

	return rows
}

func join(path, label string) string {
	if path == "" {
		return label
	}
	return path + "." + label
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

// Summary counts a plan by object rather than by field: a node with four
// changed fields is one object to change, the same as an element with one.
type Summary struct {
	Created int
	Changed int
	Deleted int
}

// Total is how many objects the plan touches.
func (s Summary) Total() int { return s.Created + s.Changed + s.Deleted }

// String describes the plan as work still to do.
func (s Summary) String() string {
	return s.describe("to create", "to change", "to delete", "nothing to do")
}

// Done describes the plan as work already carried out.
func (s Summary) Done() string {
	return s.describe("created", "changed", "deleted", "nothing changed")
}

func (s Summary) describe(created, changed, deleted, none string) string {
	var parts []string
	if s.Created > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Created, created))
	}
	if s.Changed > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Changed, changed))
	}
	if s.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Deleted, deleted))
	}
	if len(parts) == 0 {
		return none
	}
	return strings.Join(parts, ", ")
}
