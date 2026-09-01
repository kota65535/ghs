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

// operation is one node of settings.yml and the API operation that writes its
// fields.
type operation struct {
	// key is where the node is written, as the keys leading down to it from
	// the top level of the file. It is empty for the root, whose fields are
	// the top level itself.
	//
	// The keys follow the API's own path, so that finding where a setting goes
	// is a matter of reading the endpoint that changes it.
	key []string

	path   string // OpenAPI path
	method string // OpenAPI method
	kind   string // schema.Kind constant the node is generated with

	// conditional marks a path that exists only in some repositories.
	conditional bool
}

// operations lists every node ghs generates a description for. Adding a
// setting means adding a line here.
//
// A collection is generated from the operation that creates one element,
// because that request body is the list of fields an element may declare.
//
// Nodes that only stand for a path segment are not listed: they are implied by
// the keys of the nodes below them. GitHub has nothing at
// /repos/{owner}/{repo}/actions, but its permissions and variables are reached
// through it.
var operations = []operation{
	{key: nil, path: "/repos/{owner}/{repo}", method: "patch", kind: "KindObject"},

	{key: []string{"rulesets"}, path: "/repos/{owner}/{repo}/rulesets", method: "post", kind: "KindCollection"},
	{key: []string{"environments"}, path: "/repos/{owner}/{repo}/environments/{environment_name}", method: "put", kind: "KindCollection"},
	{key: []string{"environments", "variables"}, path: "/repos/{owner}/{repo}/environments/{environment_name}/variables", method: "post", kind: "KindCollection"},

	{key: []string{"actions", "variables"}, path: "/repos/{owner}/{repo}/actions/variables", method: "post", kind: "KindCollection"},
	{key: []string{"actions", "permissions"}, path: "/repos/{owner}/{repo}/actions/permissions", method: "put", kind: "KindObject"},
	{key: []string{"actions", "permissions", "workflow"}, path: "/repos/{owner}/{repo}/actions/permissions/workflow", method: "put", kind: "KindObject"},
	{key: []string{"actions", "permissions", "fork-pr-contributor-approval"}, path: "/repos/{owner}/{repo}/actions/permissions/fork-pr-contributor-approval", method: "put", kind: "KindObject", conditional: true},
	{key: []string{"actions", "permissions", "selected-actions"}, path: "/repos/{owner}/{repo}/actions/permissions/selected-actions", method: "put", kind: "KindObject", conditional: true},
	{key: []string{"actions", "permissions", "artifact-and-log-retention"}, path: "/repos/{owner}/{repo}/actions/permissions/artifact-and-log-retention", method: "put", kind: "KindObject"},
}

const (
	refFile = "gen/openapi.ref"
	outFile = "internal/schema/nodes_gen.go"
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
	Type        string
	Enum        []string
	Description string
	Fields      map[string]field
}

// node mirrors schema.Node during generation.
type node struct {
	kind        string
	segment     string
	method      string
	summary     string
	conditional bool
	from        string // the operation it was generated from, for a comment

	fields map[string]field
	nodes  map[string]*node
}

// child returns the node written under name, adding it as a namespace if this
// is the first time it is reached. A namespace is what a path segment with no
// operation of its own amounts to.
func (n *node) child(name string) *node {
	if n.nodes == nil {
		n.nodes = map[string]*node{}
	}
	if existing, ok := n.nodes[name]; ok {
		return existing
	}
	created := &node{kind: "KindNamespace", segment: name}
	n.nodes[name] = created
	return created
}

func generate(spec map[string]any, ref string) ([]byte, error) {
	root, err := buildTree(spec)
	if err != nil {
		return nil, err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "// Code generated by `go run ./gen`. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "//\n")
	fmt.Fprintf(&b, "// Source: github/rest-api-description@%s\n", ref)
	fmt.Fprintf(&b, "\npackage schema\n\n")
	// A package path rather than a relative one: go generate runs the command
	// from the directory of the file it appears in.
	fmt.Fprintf(&b, "//go:generate go run github.com/kota65535/ghs/gen\n\n")
	fmt.Fprintf(&b, "// generated describes the settings file exactly as the API description\n")
	fmt.Fprintf(&b, "// states it. See extra.go for what it is missing.\n")
	fmt.Fprintf(&b, "var generated = ")
	writeNode(&b, root, 0)
	fmt.Fprintf(&b, "\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}
	return src, nil
}

// buildTree places every operation at the key it is written under, creating the
// namespaces the keys pass through.
func buildTree(spec map[string]any) (*node, error) {
	c := converter{spec: spec}
	root := &node{}

	for _, op := range operations {
		target := root
		for _, key := range op.key {
			target = target.child(key)
		}

		if target.method != "" {
			return nil, fmt.Errorf("%s: two operations write the same node", strings.Join(op.key, "."))
		}

		props, err := requestProperties(spec, op)
		if err != nil {
			return nil, err
		}

		target.kind = op.kind
		target.method = strings.ToUpper(op.method)
		target.summary = operationSummary(spec, op)
		target.conditional = op.conditional
		target.fields = c.properties(props, nil)
		target.from = fmt.Sprintf("%s %s", strings.ToUpper(op.method), op.path)
		// The root is where paths are measured from, so it adds nothing to
		// one.
		if len(op.key) > 0 {
			target.segment = lastSegment(op.path)
		}
	}

	if err := checkCollisions(root, ""); err != nil {
		return nil, err
	}
	return root, nil
}

// lastSegment is what an operation's path adds under its parent's, which is
// its final segment unless that segment only names an element of a collection.
//
// Creating an environment is a PUT on environments/{environment_name}, and
// what the node adds to the path is "environments": the name comes from the
// element being written, not from the description.
func lastSegment(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if !strings.HasPrefix(parts[i], "{") {
			return parts[i]
		}
	}
	return ""
}

// checkCollisions rejects a node whose own field is named the same as one of
// the keys below it, which would leave no way to tell them apart in the file.
func checkCollisions(n *node, where string) error {
	for name := range n.nodes {
		if _, taken := n.fields[name]; taken {
			return fmt.Errorf("%s%s is both a field and a key of its own", where, name)
		}
	}
	for name, child := range n.nodes {
		if err := checkCollisions(child, where+name+"."); err != nil {
			return err
		}
	}
	return nil
}

func writeNode(b *strings.Builder, n *node, depth int) {
	indent := strings.Repeat("\t", depth)

	fmt.Fprintf(b, "Node{\n")
	fmt.Fprintf(b, "%s\tKind: %s,\n", indent, n.kind)
	if n.segment != "" {
		fmt.Fprintf(b, "%s\tSegment: %q,\n", indent, n.segment)
	}
	if n.method != "" {
		fmt.Fprintf(b, "%s\tMethod: %q,\n", indent, n.method)
	}
	if n.summary != "" {
		fmt.Fprintf(b, "%s\tSummary: %q,\n", indent, n.summary)
	}
	if n.conditional {
		fmt.Fprintf(b, "%s\tConditional: true,\n", indent)
	}

	if len(n.fields) > 0 {
		if n.from != "" {
			fmt.Fprintf(b, "%s\t// from %s\n", indent, n.from)
		}
		fmt.Fprintf(b, "%s\tFields: ", indent)
		writeFields(b, n.fields, depth+1)
		fmt.Fprintf(b, ",\n")
	}

	if len(n.nodes) > 0 {
		fmt.Fprintf(b, "%s\tNodes: map[string]Node{\n", indent)
		names := make([]string, 0, len(n.nodes))
		for name := range n.nodes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(b, "%s\t\t%q: ", indent, name)
			writeNode(b, n.nodes[name], depth+2)
			fmt.Fprintf(b, ",\n")
		}
		fmt.Fprintf(b, "%s\t},\n", indent)
	}

	fmt.Fprintf(b, "%s}", indent)
}

// operationSummary is the one-line title the description gives an operation,
// which is what a node stands for: "Set GitHub Actions permissions for a
// repository" says more about the key it is written under than the key does.
//
// An operation without one is not an error: the summary is a label, and a node
// without it is simply written without one.
func operationSummary(spec map[string]any, op operation) string {
	node, err := dig(spec, "paths", op.path, op.method, "summary")
	if err != nil {
		return ""
	}
	summary, _ := node.(string)
	return summary
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
	// Read before the refs are followed: where a property both refers to a
	// shared schema and describes itself, what it says about itself is the
	// description of this field in particular.
	description, _ := m["description"].(string)

	m, seen, ok := c.resolve(m, seen)
	if !ok {
		// A ref that cannot be followed leaves the field free-form, which
		// passes its content through to the API unvalidated rather than
		// rejecting settings the API would accept.
		return field{Description: description}
	}

	f := field{Description: description}

	if t, ok := m["type"].(string); ok {
		f.Type = t
	}
	// A field described only where its shared schema is defined takes the
	// description from there.
	if f.Description == "" {
		f.Description, _ = m["description"].(string)
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
		if f.Description != "" {
			parts = append(parts, fmt.Sprintf("Description: %q", f.Description))
		}
		fmt.Fprintf(b, "%s", strings.Join(parts, ", "))

		if len(f.Fields) > 0 {
			if len(parts) > 0 {
				fmt.Fprintf(b, ", ")
			}
			fmt.Fprintf(b, "Fields: ")
			writeFields(b, f.Fields, depth+2)
		}
		fmt.Fprintf(b, "},\n")
	}
	fmt.Fprintf(b, "%s}", indent)
}
