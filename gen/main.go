// Command gen produces internal/schema/fields_gen.go from the official GitHub
// OpenAPI description.
//
// The request body schema of an API operation is the authoritative list of
// fields that operation accepts, so it is also the authoritative list of
// fields ghs may manage. Generating from it keeps ghs current with GitHub
// without a hand-maintained allow list.
//
// Usage:
//
//	go run ./gen                      # downloads the description pinned in gen/openapi.ref
//	GHS_OPENAPI_FILE=api.json go run ./gen   # uses a local copy instead
package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// operation identifies the API operation whose request body defines the
// writable fields of a resource.
type operation struct {
	resource string // top-level key in settings.yml
	path     string // OpenAPI path
	method   string // OpenAPI method
	kind     string // schema.Kind constant the resource is generated with
}

// operations lists every resource ghs generates field definitions for.
// Adding a resource means adding a line here.
//
// A collection is generated from the operation that creates one element,
// because that request body is the list of fields an element may declare.
//
// Several lines may name the same resource, whose fields are then the union of
// what those operations accept. GitHub spreads a repository's Actions settings
// over a handful of endpoints, but they are one thing to declare.
var operations = []operation{
	{resource: "repository", path: "/repos/{owner}/{repo}", method: "patch", kind: "KindObject"},
	{resource: "variables", path: "/repos/{owner}/{repo}/actions/variables", method: "post", kind: "KindCollection"},
	{resource: "rulesets", path: "/repos/{owner}/{repo}/rulesets", method: "post", kind: "KindCollection"},
	{resource: "environments", path: "/repos/{owner}/{repo}/environments/{environment_name}", method: "put", kind: "KindCollection"},

	// The retention endpoint is left out: its only field is named "days",
	// which says nothing about what it counts once it sits among the fields
	// below. Writing the API's own names only works while those names carry
	// their meaning with them.
	{resource: "actions", path: "/repos/{owner}/{repo}/actions/permissions", method: "put", kind: "KindObject"},
	{resource: "actions", path: "/repos/{owner}/{repo}/actions/permissions/workflow", method: "put", kind: "KindObject"},
	{resource: "actions", path: "/repos/{owner}/{repo}/actions/permissions/selected-actions", method: "put", kind: "KindObject"},
	{resource: "actions", path: "/repos/{owner}/{repo}/actions/permissions/fork-pr-contributor-approval", method: "put", kind: "KindObject"},
}

// resourceNames returns the resources to generate, in the order they are first
// named above, so that regenerating produces the same file.
func resourceNames() []string {
	var names []string
	seen := map[string]bool{}
	for _, op := range operations {
		if seen[op.resource] {
			continue
		}
		seen[op.resource] = true
		names = append(names, op.resource)
	}
	for _, nested := range nestedOperations {
		if !seen[nested.parent] {
			names = append(names, nested.parent)
			seen[nested.parent] = true
		}
	}
	return names
}

// nestedOperation identifies the operation that creates one entry of a
// collection belonging to another resource.
//
// A list a resource owns is sometimes written one entry at a time through an
// API of its own. That is a choice about how the list is updated, not about
// whether it belongs to the resource, so the entries are declared as a field
// of it and generated from the body that creates one.
type nestedOperation struct {
	parent string // top-level key whose elements hold the collection
	field  string // key the collection takes within an element
	path   string
	method string
}

var nestedOperations = []nestedOperation{
	{
		parent: "environments", field: "variables",
		path:   "/repos/{owner}/{repo}/environments/{environment_name}/variables",
		method: "post",
	},
}

const (
	refFile = "gen/openapi.ref"
	outFile = "internal/schema/fields_gen.go"
	specURL = "https://raw.githubusercontent.com/github/rest-api-description/%s/descriptions/api.github.com/api.github.com.json"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	ref, err := readRef(filepath.Join(root, refFile))
	if err != nil {
		return err
	}

	spec, err := loadSpec(ref)
	if err != nil {
		return err
	}

	src, err := generate(spec, ref)
	if err != nil {
		return err
	}

	out := filepath.Join(root, outFile)
	if err := os.WriteFile(out, src, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Fprintf(os.Stderr, "gen: wrote %s from rest-api-description@%s\n", outFile, ref[:12])
	return nil
}

// repoRoot walks up from the working directory until it finds go.mod, so the
// generator behaves the same whether it is run from the module root or from
// the directory of a //go:generate directive.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// readRef returns the commit pinned in gen/openapi.ref, ignoring comments.
func readRef(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line, nil
	}
	return "", fmt.Errorf("%s contains no ref", path)
}

func loadSpec(ref string) (map[string]any, error) {
	var r io.ReadCloser

	if local := os.Getenv("GHS_OPENAPI_FILE"); local != "" {
		f, err := os.Open(local)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", local, err)
		}
		r = f
	} else {
		url := fmt.Sprintf(specURL, ref)
		client := &http.Client{Timeout: 5 * time.Minute}
		resp, err := client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", url, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("download %s: %s", url, resp.Status)
		}
		r = resp.Body
	}
	defer r.Close()

	var spec map[string]any
	if err := json.NewDecoder(r).Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode description: %w", err)
	}
	return spec, nil
}

// field mirrors schema.Field during generation.
type field struct {
	Type     string
	Enum     []string
	Fields   map[string]field
	Elements map[string]field
}

func generate(spec map[string]any, ref string) ([]byte, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "// Code generated by `go run ./gen`. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "//\n")
	fmt.Fprintf(&b, "// Source: github/rest-api-description@%s\n", ref)
	fmt.Fprintf(&b, "\npackage schema\n\n")
	// A package path rather than a relative one: go generate runs the command
	// from the directory of the file it appears in.
	fmt.Fprintf(&b, "//go:generate go run github.com/kota65535/ghs/gen\n\n")
	fmt.Fprintf(&b, "// generated holds the field definitions exactly as the description states\n")
	fmt.Fprintf(&b, "// them. See extra.go for the fields it is missing.\n")
	fmt.Fprintf(&b, "var generated = map[string]Resource{\n")

	c := converter{spec: spec}
	for _, resource := range resourceNames() {
		var (
			fields = map[string]field{}
			kind   string
			from   []string
		)

		// A resource whose state is spread over several endpoints is put back
		// together here: the settings file describes the resource, not the
		// requests it takes to write one.
		for _, op := range operations {
			if op.resource != resource {
				continue
			}
			props, err := requestProperties(spec, op)
			if err != nil {
				return nil, err
			}
			for name, f := range c.properties(props, nil) {
				if _, taken := fields[name]; taken {
					return nil, fmt.Errorf("%s: %s is described by more than one operation", resource, name)
				}
				fields[name] = f
			}
			kind = op.kind
			from = append(from, fmt.Sprintf("%s %s", strings.ToUpper(op.method), op.path))
		}

		for _, nested := range nestedOperations {
			if nested.parent != resource {
				continue
			}
			entry, err := requestProperties(spec, operation{path: nested.path, method: nested.method})
			if err != nil {
				return nil, err
			}
			fields[nested.field] = field{Type: "array", Elements: c.properties(entry, nil)}
			from = append(from, fmt.Sprintf("%s %s", strings.ToUpper(nested.method), nested.path))
		}

		fmt.Fprintf(&b, "\t%q: {\n", resource)
		fmt.Fprintf(&b, "\t\tName: %q,\n", resource)
		fmt.Fprintf(&b, "\t\tKind: %s,\n", kind)
		for _, source := range from {
			fmt.Fprintf(&b, "\t\t// from %s\n", source)
		}
		fmt.Fprintf(&b, "\t\tFields: ")
		writeFields(&b, fields, 2)
		fmt.Fprintf(&b, ",\n")
		fmt.Fprintf(&b, "\t},\n")
	}

	fmt.Fprintf(&b, "}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}
	return src, nil
}

// requestProperties returns the properties of the operation's JSON request
// body schema.
func requestProperties(spec map[string]any, op operation) (map[string]any, error) {
	where := fmt.Sprintf("%s %s", strings.ToUpper(op.method), op.path)

	node, err := dig(spec, "paths", op.path, op.method, "requestBody", "content", "application/json", "schema")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	schema, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: schema is not an object", where)
	}

	// The body is sometimes a shared schema rather than one written out in
	// place, so the ref is followed before its properties are read.
	schema, _, ok = (converter{spec: spec}).resolve(schema, nil)
	if !ok {
		return nil, fmt.Errorf("%s: request body schema cannot be resolved", where)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return nil, fmt.Errorf("%s: no properties found", where)
	}
	return props, nil
}

func dig(node any, keys ...string) (any, error) {
	for i, key := range keys {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s is not an object", strings.Join(keys[:i], "."))
		}
		next, ok := m[key]
		if !ok {
			return nil, fmt.Errorf("%s not found", strings.Join(keys[:i+1], "."))
		}
		node = next
	}
	return node, nil
}

// converter turns description schemas into field definitions, following the
// $ref indirections the description uses to share them.
type converter struct {
	spec map[string]any
}

func (c converter) properties(props map[string]any, seen []string) map[string]field {
	out := make(map[string]field, len(props))
	for name, raw := range props {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		out[name] = c.field(m, seen)
	}
	return out
}

func (c converter) field(m map[string]any, seen []string) field {
	m, seen, ok := c.resolve(m, seen)
	if !ok {
		// A ref that cannot be followed leaves the field free-form, which
		// passes its content through to the API unvalidated rather than
		// rejecting settings the API would accept.
		return field{}
	}

	f := field{}

	if t, ok := m["type"].(string); ok {
		f.Type = t
	}
	if raw, ok := m["enum"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				f.Enum = append(f.Enum, s)
			}
		}
		sort.Strings(f.Enum)
	}
	// Nested properties are recorded so that object fields are validated to
	// the same depth the description defines. An object without declared
	// properties stays free-form and is passed through unvalidated, which is
	// what happens to a schema built from oneOf: the ruleset rules are one
	// such, and which fields each variant takes is left to the API.
	if nested, ok := m["properties"].(map[string]any); ok && len(nested) > 0 {
		f.Fields = c.properties(nested, seen)
	}
	return f
}

// resolve follows a chain of $ref until it reaches a schema, reporting false
// when the chain cannot be followed or turns back on itself.
//
// The returned seen list records the refs followed to get here, so that a
// schema referring to itself -- directly or through others -- stops rather
// than recursing forever.
func (c converter) resolve(m map[string]any, seen []string) (map[string]any, []string, bool) {
	for {
		ref, isRef := m["$ref"].(string)
		if !isRef {
			return m, seen, true
		}
		for _, followed := range seen {
			if followed == ref {
				return nil, seen, false
			}
		}
		// A fresh slice per chain: sibling fields legitimately refer to the
		// same schema, and must not see each other's history.
		seen = append(append([]string(nil), seen...), ref)

		target, err := c.lookupRef(ref)
		if err != nil {
			return nil, seen, false
		}
		m = target
	}
}

// lookupRef returns the schema a local $ref names.
//
// Only refs within the description itself are supported, which is the only
// form it uses. Pointer escapes (~0, ~1) are not decoded because no key in the
// description needs them.
func (c converter) lookupRef(ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported ref %q", ref)
	}
	node, err := dig(c.spec, strings.Split(strings.TrimPrefix(ref, "#/"), "/")...)
	if err != nil {
		return nil, fmt.Errorf("ref %s: %w", ref, err)
	}
	target, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ref %s does not name a schema", ref)
	}
	return target, nil
}

func writeFields(b *strings.Builder, fields map[string]field, depth int) {
	indent := strings.Repeat("\t", depth)
	fmt.Fprintf(b, "map[string]Field{\n")

	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		f := fields[name]
		fmt.Fprintf(b, "%s\t%q: {", indent, name)

		var parts []string
		if f.Type != "" {
			parts = append(parts, fmt.Sprintf("Type: %q", f.Type))
		}
		if len(f.Enum) > 0 {
			quoted := make([]string, len(f.Enum))
			for i, v := range f.Enum {
				quoted[i] = fmt.Sprintf("%q", v)
			}
			parts = append(parts, fmt.Sprintf("Enum: []string{%s}", strings.Join(quoted, ", ")))
		}
		fmt.Fprintf(b, "%s", strings.Join(parts, ", "))

		if len(f.Fields) > 0 {
			if len(parts) > 0 {
				fmt.Fprintf(b, ", ")
			}
			parts = append(parts, "")
			fmt.Fprintf(b, "Fields: ")
			writeFields(b, f.Fields, depth+2)
		}
		if len(f.Elements) > 0 {
			if len(parts) > 0 {
				fmt.Fprintf(b, ", ")
			}
			fmt.Fprintf(b, "Elements: ")
			writeFields(b, f.Elements, depth+2)
		}
		fmt.Fprintf(b, "},\n")
	}
	fmt.Fprintf(b, "%s}", indent)
}
