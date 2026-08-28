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

// bodyIndent is how far the fields of an object are set in from its heading,
// and closeIndent how far the bracket that ends a nested block is. Nested
// blocks repeat the pair, so a bracket lines up under the field it closes.
const (
	bodyIndent  = "    "
	closeIndent = "  "
)

// noSign takes the place of a sign on a line that is there for context rather
// than because it changed, keeping such a line in step with the ones around it.
const noSign = " "

func renderText(w io.Writer, changes []Change, options renderOptions) error {
	if len(changes) == 0 {
		_, err := fmt.Fprintln(w, NoChangesMessage)
		return err
	}

	// One block per resource, written the way the settings file writes it: a
	// mapping of fields, or a sequence of elements.
	for _, group := range groupChanges(changes) {
		if err := writeResource(w, group, options); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "Plan: %s.\n", Summarize(changes))
	return err
}

func writeResource(w io.Writer, group resourceGroup, options renderOptions) error {
	// The resource itself is always being changed -- it is the repository, and
	// that exists. What is created or deleted is an element within it.
	sign := colorize("~", ansiYellow, options.color)

	if group.elements == nil {
		if _, err := fmt.Fprintf(w, "%s %s:\n", sign, group.resource); err != nil {
			return err
		}
		return writeFieldChanges(w, group.fields, bodyIndent, options)
	}

	if _, err := fmt.Fprintf(w, "%s %s: [\n", sign, group.resource); err != nil {
		return err
	}
	return writeSequence(w, group.elements, "", options)
}

// writeSequence writes the entries of a collection as the sequence the
// settings file writes it as, closing the bracket its caller opened.
func writeSequence(w io.Writer, entries []elementGroup, indent string, options renderOptions) error {
	for _, entry := range entries {
		if err := writeElement(w, entry, indent+bodyIndent, options); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "%s%s]\n", indent, closeIndent)
	return err
}

// writeElement writes one entry of a collection as the mapping it is written
// as in the settings file.
//
// An entry holding a collection of its own is written the same way, so this
// calls itself for those: the variables of an environment are a sequence of
// mappings under the environment, exactly as the environments are under the
// repository.
func writeElement(w io.Writer, element elementGroup, indent string, options renderOptions) error {
	sign, color := marker(element.action)
	if _, err := fmt.Fprintf(w, "%s%s {\n", indent, colorize(sign, color, options.color)); err != nil {
		return err
	}

	body := indent + bodyIndent
	if element.values != nil {
		// An element arriving or going takes all of its fields with it, so
		// they are listed out: which fields those are is the thing to review.
		if err := writeValues(w, element.values, sign, color, body, options); err != nil {
			return err
		}
	} else if err := writeElementChanges(w, element, body, options); err != nil {
		return err
	}

	for _, nested := range element.nested {
		_, err := fmt.Fprintf(w, "%s%s %s: [\n",
			body, colorize("~", ansiYellow, options.color), nested.field)
		if err != nil {
			return err
		}
		if err := writeSequence(w, nested.entries, body, options); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "%s%s},\n", indent, closeIndent)
	return err
}

// writeElementChanges writes the changed fields of an element, led by the name
// that says which element it is.
//
// The name is written without a sign, as the context it is: it identifies the
// entry rather than being part of what changes about it.
func writeElementChanges(w io.Writer, element elementGroup, indent string, options renderOptions) error {
	labels := make([]string, 0, len(element.fields)+1)
	labels = append(labels, NameField)
	for _, f := range element.fields {
		labels = append(labels, f.label)
	}

	width := labelWidth(labels)
	_, err := fmt.Fprintf(w, "%s%s %-*s %s\n",
		indent, noSign, width, NameField+":", formatValue(element.name, false))
	if err != nil {
		return err
	}
	return writeFieldChangesWidth(w, element.fields, indent, width, options)
}

// writeFieldChanges writes one line per changed field, naming the field, the
// value it holds and the value it is to hold.
func writeFieldChanges(w io.Writer, fields []fieldChange, indent string, options renderOptions) error {
	labels := make([]string, 0, len(fields))
	for _, f := range fields {
		labels = append(labels, f.label)
	}
	return writeFieldChangesWidth(w, fields, indent, labelWidth(labels), options)
}

func writeFieldChangesWidth(w io.Writer, fields []fieldChange, indent string, width int, options renderOptions) error {
	for _, f := range fields {
		// A value that exists on only one side is written under the sign for
		// what happens to it, with nothing on the other side of an arrow to
		// read against. A list entry that is being dropped is the usual case.
		if sign, color, value, only := oneSided(f.change); only {
			label := fmt.Sprintf("%s%s %-*s ", indent, colorize(sign, color, options.color), width, f.label+":")
			if err := writeValue(w, label, value, sign, color, indent, "", options); err != nil {
				return err
			}
			continue
		}

		_, err := fmt.Fprintf(w, "%s%s %-*s %s -> %s\n",
			indent,
			colorize("~", ansiYellow, options.color),
			width, f.label+":",
			colorize(formatValue(f.change.Current, false), ansiRed, options.color),
			colorize(formatValue(f.change.Desired, false), ansiGreen, options.color))
		if err != nil {
			return err
		}
	}
	return nil
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

// signOf is what a change to a whole entry amounts to: an entry that exists on
// one side of the comparison only is arriving or going.
func signOf(c Change) Action {
	switch {
	case c.CurrentMissing:
		return ActionCreate
	case c.DesiredMissing:
		return ActionDelete
	default:
		return ActionUpdate
	}
}

// onlyValue returns the whole entry when a change is about one arriving or
// going rather than about a field within it.
func onlyValue(c Change) (map[string]any, bool) {
	_, _, value, ok := oneSided(c)
	if !ok {
		return nil, false
	}
	fields, ok := value.(map[string]any)
	return fields, ok
}

// labelWidth is how wide the field names are once the colon that follows them
// is counted, which is what the values are lined up against.
func labelWidth(labels []string) int {
	width := 0
	for _, label := range labels {
		if n := len(label) + len(":"); n > width {
			width = n
		}
	}
	return width
}

// writeValues lists the fields of an object that is being created or deleted,
// opening out nested objects and lists rather than printing them as one long
// line. Every line carries the object's own sign: all of it is arriving, or
// all of it is going.
func writeValues(w io.Writer, values map[string]any, sign, color, indent string, options renderOptions) error {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	width := labelWidth(names)

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

func renderMarkdown(w io.Writer, changes []Change) error {
	if _, err := fmt.Fprintln(w, "### ghs plan"); err != nil {
		return err
	}
	if len(changes) == 0 {
		_, err := fmt.Fprintf(w, "\n%s\n", NoChangesMessage)
		return err
	}

	if _, err := fmt.Fprint(w, "\n| Action | Path | Current | Desired |\n| --- | --- | --- | --- |\n"); err != nil {
		return err
	}
	for _, c := range changes {
		_, err := fmt.Fprintf(w, "| %s | `%s` | `%s` | `%s` |\n",
			c.action(), c.Path,
			formatValue(c.Current, c.CurrentMissing),
			formatValue(c.Desired, c.DesiredMissing))
		if err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "\n**Plan: %s.**\n", Summarize(changes))
	return err
}

// jsonChange is the wire form of a change. The field names are snake_case so
// the output reads like the settings file and the API it mirrors.
type jsonChange struct {
	Action         Action `json:"action"`
	Path           string `json:"path"`
	Element        string `json:"element,omitempty"`
	Nested         string `json:"nested,omitempty"`
	Entry          string `json:"entry,omitempty"`
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

func renderJSON(w io.Writer, changes []Change) error {
	plan := jsonPlan{Changes: make([]jsonChange, 0, len(changes))}
	for _, c := range changes {
		plan.Changes = append(plan.Changes, jsonChange{
			Action:         c.action(),
			Path:           c.Path,
			Element:        c.Element,
			Nested:         c.Nested,
			Entry:          c.Entry,
			Current:        c.Current,
			Desired:        c.Desired,
			CurrentMissing: c.CurrentMissing,
			DesiredMissing: c.DesiredMissing,
		})
	}

	summary := Summarize(changes)
	plan.Summary.Created = summary.Created
	plan.Summary.Changed = summary.Changed
	plan.Summary.Deleted = summary.Deleted

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
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

// Summary counts a plan by object rather than by field: a resource with four
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

// Summarize counts what a set of changes amounts to.
func Summarize(changes []Change) Summary {
	var s Summary
	// Field changes are counted once per object they belong to, which is the
	// resource itself for a single-object resource and the element for a
	// collection.
	objects := map[string]bool{}

	for _, change := range changes {
		switch change.action() {
		case ActionCreate:
			s.Created++
		case ActionDelete:
			s.Deleted++
		default:
			objects[resourceOf(change.Path)+"\x00"+change.Element] = true
		}
	}

	s.Changed = len(objects)
	return s
}

// fieldChange is one changed field together with the label it is listed under,
// which is its path with the enclosing object's prefix removed.
type fieldChange struct {
	label  string
	change Change
}

// elementGroup is everything happening to one element of a collection.
type elementGroup struct {
	name   string
	action Action

	// fields holds the changed fields of an element being updated, and values
	// the whole of one being created or deleted. An element is one or the
	// other, so only one of these is ever set.
	fields []fieldChange
	values map[string]any

	// nested holds the collections the element owns, such as the variables of
	// an environment.
	nested []nestedGroup
}

// nestedGroup is a collection held by an element, and the entries of it that
// are changing.
type nestedGroup struct {
	field   string
	entries []elementGroup
}

// nest returns the group for a collection the element owns, adding it if this
// is the first change seen for that field.
func (g *elementGroup) nest(field string) *nestedGroup {
	for i := range g.nested {
		if g.nested[i].field == field {
			return &g.nested[i]
		}
	}
	g.nested = append(g.nested, nestedGroup{field: field})
	return &g.nested[len(g.nested)-1]
}

// entry returns the group for one entry of the collection, adding it if this
// is the first change seen for that entry.
func (n *nestedGroup) entry(name string) *elementGroup {
	for i := range n.entries {
		if n.entries[i].name == name {
			return &n.entries[i]
		}
	}
	n.entries = append(n.entries, elementGroup{name: name, action: ActionUpdate})
	return &n.entries[len(n.entries)-1]
}

// resourceGroup is everything happening under one top-level key.
type resourceGroup struct {
	resource string

	// fields holds the changed fields of a single-object resource, and
	// elements the entries of a collection. A resource is one or the other, so
	// elements stays nil for the first and is what tells the two apart.
	fields   []fieldChange
	elements []elementGroup
}

// groupChanges collects changes into one group per resource, and within a
// collection into one per element, ordered by name so that the plan reads the
// same way however the changes arrived.
func groupChanges(changes []Change) []resourceGroup {
	var order []string
	byResource := map[string]*resourceGroup{}

	for _, c := range changes {
		name := resourceOf(c.Path)
		group, seen := byResource[name]
		if !seen {
			group = &resourceGroup{resource: name}
			byResource[name] = group
			order = append(order, name)
		}

		if c.Element == "" {
			group.fields = append(group.fields, fieldChange{
				label:  strings.TrimPrefix(c.Path, name+"."),
				change: c,
			})
			continue
		}

		element := group.element(c.Element)
		elementPath := ElementPath(name, c.Element)

		if action := c.action(); action == ActionCreate || action == ActionDelete {
			element.action = action
			// A create carries the declared element, a delete the current one.
			// Either way it is the whole element that the change is about.
			value := c.Desired
			if action == ActionDelete {
				value = c.Current
			}
			if fields, ok := value.(map[string]any); ok {
				element.values = fields
			}
			continue
		}

		// A change to a collection the element owns belongs under that
		// collection, not among the element's own fields.
		if c.Nested != "" {
			entry := element.nest(c.Nested).entry(c.Entry)
			entryPath := ElementPath(elementPath+"."+c.Nested, c.Entry)

			if value, present := onlyValue(c); present {
				entry.action = signOf(c)
				entry.values = value
				continue
			}
			entry.fields = append(entry.fields, fieldChange{
				label:  strings.TrimPrefix(c.Path, entryPath+"."),
				change: c,
			})
			continue
		}

		element.fields = append(element.fields, fieldChange{
			label:  strings.TrimPrefix(c.Path, elementPath+"."),
			change: c,
		})
	}

	sort.Strings(order)
	groups := make([]resourceGroup, 0, len(order))
	for _, name := range order {
		group := byResource[name]
		sort.SliceStable(group.elements, func(i, j int) bool {
			return group.elements[i].name < group.elements[j].name
		})
		groups = append(groups, *group)
	}
	return groups
}

// element returns the group for name, adding it if this is the first change
// seen for that element.
func (g *resourceGroup) element(name string) *elementGroup {
	for i := range g.elements {
		if g.elements[i].name == name {
			return &g.elements[i]
		}
	}
	g.elements = append(g.elements, elementGroup{name: name, action: ActionUpdate})
	return &g.elements[len(g.elements)-1]
}

// resourceOf returns the leading resource key of a change path, which ends at
// the first field separator or element subscript.
func resourceOf(path string) string {
	if i := strings.IndexAny(path, ".["); i >= 0 {
		return path[:i]
	}
	return path
}
