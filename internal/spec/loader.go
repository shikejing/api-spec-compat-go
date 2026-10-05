package spec

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Loader loads documents and resolves cross-file references.
// It caches file contents, parsed nodes and built schemas, so the cost of
// resolving the same reference does not grow with the number of occurrences.
type Loader struct {
	mu       sync.Mutex
	files    map[string][]byte
	parsed   map[string]*yaml.Node
	docs     map[string]*Document
	schemas  map[string]*Schema
	inline   map[nodeKey]*Schema
	absCache map[string]string
	calls    map[string]*loadCall
}

type loadCall struct {
	done chan struct{}
	doc  *Document
	err  error
}

type nodeKey struct {
	file string
	line int
	col  int
}

// NewLoader creates an empty Loader.
func NewLoader() *Loader {
	return &Loader{
		files:    map[string][]byte{},
		parsed:   map[string]*yaml.Node{},
		docs:     map[string]*Document{},
		schemas:  map[string]*Schema{},
		inline:   map[nodeKey]*Schema{},
		absCache: map[string]string{},
		calls:    map[string]*loadCall{},
	}
}

// LoadFile loads, parses and normalizes a single document. Concurrent calls
// for the same file share one in-flight build (singleflight).
func (l *Loader) LoadFile(path string) (*Document, error) {
	abs, err := l.absPath(path, "")
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	if d, ok := l.docs[abs]; ok {
		l.mu.Unlock()
		return d, nil
	}
	if call, ok := l.calls[abs]; ok {
		l.mu.Unlock()
		<-call.done
		return call.doc, call.err
	}
	call := &loadCall{done: make(chan struct{})}
	l.calls[abs] = call
	l.mu.Unlock()

	root, err := l.ensureParsed(abs)
	if err == nil {
		var doc *Document
		doc, err = l.buildDocument(abs, root)
		if err == nil {
			doc.Path = abs
			l.mu.Lock()
			l.docs[abs] = doc
			l.mu.Unlock()
			call.doc = doc
		}
	}
	if err != nil {
		call.err = err
	}
	l.mu.Lock()
	delete(l.calls, abs)
	l.mu.Unlock()
	close(call.done)
	return call.doc, call.err
}

// LoadDocument is an alias for LoadFile.
func (l *Loader) LoadDocument(path string) (*Document, error) {
	return l.LoadFile(path)
}

// readFile returns cached raw file contents.
func (l *Loader) readFile(abs string) ([]byte, error) {
	l.mu.Lock()
	if data, ok := l.files[abs]; ok {
		l.mu.Unlock()
		return data, nil
	}
	l.mu.Unlock()
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, &ResolveError{Location: Location{File: abs}, Message: "cannot read file", Cause: err}
	}
	l.mu.Lock()
	l.files[abs] = data
	l.mu.Unlock()
	return data, nil
}

// ensureParsed parses a file once and caches the root node.
func (l *Loader) ensureParsed(abs string) (*yaml.Node, error) {
	l.mu.Lock()
	if root, ok := l.parsed[abs]; ok {
		l.mu.Unlock()
		return root, nil
	}
	l.mu.Unlock()

	data, err := l.readFile(abs)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, parseError(abs, err)
	}
	root := rootOf(&doc)
	if root == nil {
		return nil, &ResolveError{Location: Location{File: abs}, Message: "empty document"}
	}
	l.mu.Lock()
	l.parsed[abs] = root
	l.mu.Unlock()
	return root, nil
}

var yamlLineRE = regexp.MustCompile(`line (\d+)(?::(\d+))?`)

func parseError(abs string, err error) error {
	loc := Location{File: abs, Line: 1, Column: 1}
	var te yaml.TypeError
	if msg := err.Error(); msg != "" {
		if m := yamlLineRE.FindStringSubmatch(msg); m != nil {
			fmt.Sscanf(m[1], "%d", &loc.Line)
			if m[2] != "" {
				fmt.Sscanf(m[2], "%d", &loc.Column)
			}
		}
		_ = te
	}
	return &ResolveError{Location: loc, Message: "invalid YAML/JSON syntax", Cause: err}
}

// absPath resolves a possibly relative file part against the referring file.
func (l *Loader) absPath(ref, fromFile string) (string, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return "", &ResolveError{Location: Location{File: fromFile}, Message: fmt.Sprintf("remote references are not supported: %s", ref)}
	}
	cacheKey := fromFile + "|" + ref
	l.mu.Lock()
	if a, ok := l.absCache[cacheKey]; ok {
		l.mu.Unlock()
		return a, nil
	}
	l.mu.Unlock()

	var abs string
	switch {
	case filepath.IsAbs(ref):
		abs = filepath.Clean(ref)
	case fromFile != "":
		abs = filepath.Clean(filepath.Join(filepath.Dir(fromFile), ref))
	default:
		a, err := filepath.Abs(ref)
		if err != nil {
			return "", &ResolveError{Location: Location{File: ref}, Message: "cannot resolve path", Cause: err}
		}
		abs = filepath.Clean(a)
	}
	l.mu.Lock()
	l.absCache[cacheKey] = abs
	l.mu.Unlock()
	return abs, nil
}

// splitRef separates the file part and the JSON-pointer fragment of a $ref.
func splitRef(ref string) (filePart, fragment string) {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// canonicalRef returns a stable cache key for a reference expression.
func (l *Loader) canonicalRef(ref, fromFile string) (string, error) {
	filePart, frag := splitRef(ref)
	abs := fromFile
	if filePart != "" {
		var err error
		abs, err = l.absPath(filePart, fromFile)
		if err != nil {
			return "", err
		}
	}
	return abs + "#" + frag, nil
}

// resolveRef resolves a $ref expression to its target node and owning file.
func (l *Loader) resolveRef(ref, fromFile string, at *yaml.Node) (*yaml.Node, string, error) {
	filePart, frag := splitRef(ref)
	targetFile := fromFile
	if filePart != "" {
		abs, err := l.absPath(filePart, fromFile)
		if err != nil {
			return nil, "", err
		}
		targetFile = abs
	}
	root, err := l.ensureParsed(targetFile)
	if err != nil {
		loc := nodeLoc(at, fromFile)
		return nil, "", &ResolveError{
			Location: *loc,
			Message:  fmt.Sprintf("cannot resolve reference %q: %v", ref, err),
			Cause:    err,
		}
	}
	target := root
	if frag != "" {
		target, err = walkPointer(root, frag)
		if err != nil {
			loc := nodeLoc(at, fromFile)
			return nil, "", &ResolveError{
				Location: *loc,
				Message:  fmt.Sprintf("unresolvable reference %q: %v", ref, err),
			}
		}
	}
	return target, targetFile, nil
}

// derefNode follows a node's $ref (if any) to the referenced raw node.
func (l *Loader) derefNode(n *yaml.Node, file string, seen map[string]bool) (*yaml.Node, string, error) {
	n = deref(n)
	refNode := mapField(n, "$ref")
	if refNode == nil {
		return n, file, nil
	}
	ref := scalarString(refNode)
	key, err := l.canonicalRef(ref, file)
	if err != nil {
		return nil, "", err
	}
	if seen[key] {
		return nil, "", &ResolveError{Location: *nodeLoc(refNode, file), Message: fmt.Sprintf("cyclic reference: %s", key)}
	}
	target, targetFile, err := l.resolveRef(ref, file, refNode)
	if err != nil {
		return nil, "", err
	}
	seen[key] = true
	t, f, err := l.derefNode(target, targetFile, seen)
	delete(seen, key)
	return t, f, err
}

// walkPointer evaluates a JSON pointer fragment against a node.
func walkPointer(root *yaml.Node, frag string) (*yaml.Node, error) {
	if !strings.HasPrefix(frag, "/") {
		return nil, fmt.Errorf("fragment must start with '/'")
	}
	cur := root
	for _, raw := range strings.Split(frag, "/")[1:] {
		tok, err := url.QueryUnescape(strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~"))
		if err != nil {
			return nil, fmt.Errorf("invalid pointer token %q", raw)
		}
		cur = deref(cur)
		if cur == nil {
			return nil, fmt.Errorf("pointer step %q enters a null node", tok)
		}
		switch cur.Kind {
		case yaml.MappingNode:
			next := mapField(cur, tok)
			if next == nil {
				return nil, fmt.Errorf("key %q not found", tok)
			}
			cur = next
		case yaml.SequenceNode:
			var i int
			if _, err := fmt.Sscanf(tok, "%d", &i); err != nil || i < 0 || i >= len(cur.Content) {
				return nil, fmt.Errorf("sequence index %q out of range", tok)
			}
			cur = deref(cur.Content[i])
		default:
			return nil, fmt.Errorf("pointer step %q cannot traverse scalar", tok)
		}
	}
	return cur, nil
}

// SchemaAt returns the normalized schema for a raw node, caching the result.
func (l *Loader) SchemaAt(n *yaml.Node, file string) (*Schema, error) {
	return l.buildSchema(n, file, map[string]bool{})
}
