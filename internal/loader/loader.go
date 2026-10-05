// Package loader reads interface description documents from local
// files, resolves all references (including cross-file and cyclic
// ones) and normalizes them into the spec model.
//
// Supported description versions: OpenAPI 3.x and Swagger 2.0.
// Everything is done locally; no network access is performed.
package loader

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
	"gopkg.in/yaml.v3"
)

// Error is a load/resolve failure carrying source location.
type Error struct {
	Loc spec.Location
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Loc, e.Msg) }

func errAt(file string, n *yaml.Node, format string, args ...any) *Error {
	loc := spec.Location{File: file}
	if n != nil {
		loc.Line, loc.Col = n.Line, n.Column
	}
	return &Error{Loc: loc, Msg: fmt.Sprintf(format, args...)}
}

// resolver parses YAML files on demand and resolves $ref targets with
// memoization, so repeated references to the same target cost one
// resolution regardless of how many times they appear.
type resolver struct {
	files      map[string]*yaml.Node // abs path -> parsed document node
	schemaMemo map[string]*spec.Schema
	inProgress map[string]bool
}

func newResolver() *resolver {
	return &resolver{
		files:      map[string]*yaml.Node{},
		schemaMemo: map[string]*spec.Schema{},
		inProgress: map[string]bool{},
	}
}

// Load reads the document at path, resolves every reference and
// returns the normalized document. Errors carry file/line/column
// location information.
func Load(path string) (*spec.Document, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	r := newResolver()
	root, err := r.parseFile(abs)
	if err != nil {
		return nil, err
	}
	b := &docBuilder{r: r, file: abs}
	return b.build(root)
}

// parseFile reads and YAML-parses a file, caching the result.
func (r *resolver) parseFile(abs string) (*yaml.Node, error) {
	if n, ok := r.files[abs]; ok {
		return n, nil
	}
	var doc yaml.Node
	if err := readYAMLFile(abs, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, errAt(abs, &doc, "empty document")
	}
	root := doc.Content[0]
	r.files[abs] = root
	return root, nil
}

// refTarget is a resolved reference: the target node plus the file it
// lives in and a canonical key used for memoization.
type refTarget struct {
	node *yaml.Node
	file string
	key  string // "absfile#pointer"
	name string // last pointer segment, used as component name
}

// resolveRef resolves a $ref value relative to fromFile.
func (r *resolver) resolveRef(ref, fromFile string, refNode *yaml.Node) (*refTarget, error) {
	filePart, pointer, _ := strings.Cut(ref, "#")
	file := fromFile
	if filePart != "" {
		if strings.Contains(filePart, "://") {
			return nil, errAt(fromFile, refNode, "remote references are not supported: %q", ref)
		}
		file = filepath.Clean(filepath.Join(filepath.Dir(fromFile), filePart))
	}
	root, err := r.parseFile(file)
	if err != nil {
		return nil, err
	}
	node := root
	if pointer != "" {
		node, err = resolvePointer(root, pointer)
		if err != nil {
			return nil, errAt(file, refNode, "resolving %q: %v", ref, err)
		}
	}
	name := ""
	if pointer != "" {
		segs := strings.Split(pointer, "/")
		name = unescapePointerToken(segs[len(segs)-1])
	}
	return &refTarget{node: node, file: file, key: file + "#" + pointer, name: name}, nil
}

// resolvePointer walks a JSON Pointer ("/a/b/0") down a YAML node.
func resolvePointer(root *yaml.Node, pointer string) (*yaml.Node, error) {
	if !strings.HasPrefix(pointer, "/") && pointer != "" {
		return nil, fmt.Errorf("invalid pointer %q", pointer)
	}
	cur := root
	for _, tok := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if tok == "" {
			continue
		}
		tok = unescapePointerToken(tok)
		switch cur.Kind {
		case yaml.MappingNode:
			cur = mappingValue(cur, tok)
			if cur == nil {
				return nil, fmt.Errorf("pointer segment %q not found", tok)
			}
		case yaml.SequenceNode:
			var idx int
			if _, err := fmt.Sscanf(tok, "%d", &idx); err != nil || idx < 0 || idx >= len(cur.Content) {
				return nil, fmt.Errorf("invalid sequence index %q", tok)
			}
			cur = cur.Content[idx]
		default:
			return nil, fmt.Errorf("cannot descend into scalar at %q", tok)
		}
	}
	return cur, nil
}

func unescapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~1", "/")
	return strings.ReplaceAll(tok, "~0", "~")
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingKeys returns the keys of a mapping node in document order.
func mappingKeys(m *yaml.Node) []string {
	var keys []string
	if m == nil || m.Kind != yaml.MappingNode {
		return keys
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	return keys
}

func scalarString(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func scalarBool(n *yaml.Node) bool {
	return n != nil && n.Value == "true"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
