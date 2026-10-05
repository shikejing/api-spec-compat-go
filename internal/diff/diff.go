// Package diff compares two normalized documents and classifies every
// change by backward-compatibility impact.
//
// Classification semantics:
//   - breaking: definitely breaks existing clients.
//   - warning: cannot be decided statically (e.g. union type
//     replacement, overlapping enum changes, unknown constructs);
//     judged conservatively — never reported as compatible.
//   - info: backward-compatible.
//
// Request-side changes are judged by whether old client requests are
// still accepted; response-side changes by whether old clients can
// still consume every response the new version may produce.
package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// Level classifies the compatibility impact of a change.
type Level string

const (
	// LevelBreaking definitely breaks backward compatibility.
	LevelBreaking Level = "breaking"
	// LevelWarning cannot be decided statically; judged conservatively.
	LevelWarning Level = "warning"
	// LevelInfo is a backward-compatible change.
	LevelInfo Level = "info"
)

// Direction distinguishes request-side from response-side changes.
type Direction string

const (
	DirectionRequest  Direction = "request"
	DirectionResponse Direction = "response"
	DirectionNone     Direction = "none"
)

// Kind identifies the nature of a change.
type Kind string

const (
	KindAdded            Kind = "added"
	KindRemoved          Kind = "removed"
	KindTypeChanged      Kind = "type-changed"
	KindTypeNarrowed     Kind = "type-narrowed"
	KindRequiredChanged  Kind = "required-changed"
	KindEnumChanged      Kind = "enum-changed"
	KindUnionChanged     Kind = "union-changed"
	KindUnknownConstruct Kind = "unknown-construct"
)

// Change is a single detected difference.
type Change struct {
	Level     Level         `json:"level"`
	Kind      Kind          `json:"kind"`
	Direction Direction     `json:"direction"`
	Path      string        `json:"path"` // logical path, e.g. "GET /pets -> response 200 -> .name"
	Reason    string        `json:"reason"`
	Location  spec.Location `json:"location,omitempty"`
}

// Report is the outcome of comparing two documents.
type Report struct {
	Changes []Change `json:"changes"`
}

// Breaking reports whether any breaking change was found.
func (r *Report) Breaking() bool {
	for _, c := range r.Changes {
		if c.Level == LevelBreaking {
			return true
		}
	}
	return false
}

// Counts tallies changes per level, in a stable three-element order:
// breaking, warning, info.
func (r *Report) Counts() map[Level]int {
	m := map[Level]int{LevelBreaking: 0, LevelWarning: 0, LevelInfo: 0}
	for _, c := range r.Changes {
		m[c.Level]++
	}
	return m
}

// Compare diffs oldDoc against newDoc. The result is deterministic and
// independent of map iteration order.
func Compare(oldDoc, newDoc *spec.Document) *Report {
	c := &comparer{seen: map[seenKey]bool{}}
	c.compareDocuments(oldDoc, newDoc)
	sort.SliceStable(c.changes, func(i, j int) bool {
		a, b := c.changes[i], c.changes[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Reason < b.Reason
	})
	return &Report{Changes: c.changes}
}

type seenKey struct {
	old, new *spec.Schema
	dir      Direction
}

type comparer struct {
	changes []Change
	seen    map[seenKey]bool
}

func (c *comparer) add(level Level, kind Kind, dir Direction, path, reason string, loc spec.Location) {
	c.changes = append(c.changes, Change{
		Level: level, Kind: kind, Direction: dir,
		Path: path, Reason: reason, Location: loc,
	})
}

func (c *comparer) compareDocuments(oldDoc, newDoc *spec.Document) {
	for _, path := range spec.SortedKeys(oldDoc.Paths) {
		if _, ok := newDoc.Paths[path]; !ok {
			c.add(LevelBreaking, KindRemoved, DirectionNone,
				path, fmt.Sprintf("path %q was removed", path), oldDoc.Paths[path].Loc)
		}
	}
	for _, path := range spec.SortedKeys(newDoc.Paths) {
		if _, ok := oldDoc.Paths[path]; !ok {
			c.add(LevelInfo, KindAdded, DirectionNone,
				path, fmt.Sprintf("path %q was added", path), newDoc.Paths[path].Loc)
		}
	}
	for _, path := range spec.SortedKeys(oldDoc.Paths) {
		newItem, ok := newDoc.Paths[path]
		if !ok {
			continue
		}
		c.comparePathItems(path, oldDoc.Paths[path], newItem)
	}
}

func (c *comparer) comparePathItems(path string, oldItem, newItem *spec.PathItem) {
	for _, method := range spec.SortedKeys(oldItem.Operations) {
		if _, ok := newItem.Operations[method]; !ok {
			c.add(LevelBreaking, KindRemoved, DirectionNone,
				opPath(method, path),
				fmt.Sprintf("operation %s was removed", opPath(method, path)),
				oldItem.Operations[method].Loc)
		}
	}
	for _, method := range spec.SortedKeys(newItem.Operations) {
		if _, ok := oldItem.Operations[method]; !ok {
			c.add(LevelInfo, KindAdded, DirectionNone,
				opPath(method, path),
				fmt.Sprintf("operation %s was added", opPath(method, path)),
				newItem.Operations[method].Loc)
		}
	}
	for _, method := range spec.SortedKeys(oldItem.Operations) {
		newOp, ok := newItem.Operations[method]
		if !ok {
			continue
		}
		c.compareOperations(oldItem.Operations[method], newOp)
	}
}

func opPath(method, path string) string {
	return fmt.Sprintf("%s %s", strings.ToUpper(method), path)
}

func paramKey(p *spec.Parameter) string { return p.In + ":" + p.Name }

func (c *comparer) compareOperations(oldOp, newOp *spec.Operation) {
	base := opPath(oldOp.Method, oldOp.Path)

	oldParams := map[string]*spec.Parameter{}
	for _, p := range oldOp.Parameters {
		oldParams[paramKey(p)] = p
	}
	newParams := map[string]*spec.Parameter{}
	for _, p := range newOp.Parameters {
		newParams[paramKey(p)] = p
	}
	for _, key := range spec.SortedKeys(oldParams) {
		op := oldParams[key]
		np, ok := newParams[key]
		pp := base + " -> parameter " + key
		if !ok {
			if op.Required {
				c.add(LevelBreaking, KindRemoved, DirectionRequest, pp,
					"required request parameter was removed", op.Loc)
			} else {
				c.add(LevelWarning, KindRemoved, DirectionRequest, pp,
					"optional request parameter was removed; clients may still send it", op.Loc)
			}
			continue
		}
		if !op.Required && np.Required {
			c.add(LevelBreaking, KindRequiredChanged, DirectionRequest, pp,
				"request parameter became required", np.Loc)
		}
		if op.Required && !np.Required {
			c.add(LevelInfo, KindRequiredChanged, DirectionRequest, pp,
				"request parameter became optional", np.Loc)
		}
		c.checkSchema(op.Schema, np.Schema, DirectionRequest, pp+" -> schema")
	}
	for _, key := range spec.SortedKeys(newParams) {
		if _, ok := oldParams[key]; ok {
			continue
		}
		np := newParams[key]
		pp := base + " -> parameter " + key
		if np.Required {
			c.add(LevelBreaking, KindAdded, DirectionRequest, pp,
				"required request parameter was added", np.Loc)
		} else {
			c.add(LevelInfo, KindAdded, DirectionRequest, pp,
				"optional request parameter was added", np.Loc)
		}
	}

	// Request body.
	bp := base + " -> requestBody"
	switch {
	case oldOp.RequestBody == nil && newOp.RequestBody != nil:
		c.add(LevelWarning, KindAdded, DirectionRequest, bp,
			"request body was added; old clients will not send it", newOp.Loc)
	case oldOp.RequestBody != nil && newOp.RequestBody == nil:
		c.add(LevelWarning, KindRemoved, DirectionRequest, bp,
			"request body was removed; clients may still send one", oldOp.Loc)
	case oldOp.RequestBody != nil && newOp.RequestBody != nil:
		c.checkSchema(oldOp.RequestBody, newOp.RequestBody, DirectionRequest, bp)
	}

	// Responses.
	for _, status := range spec.SortedKeys(oldOp.Responses) {
		oldResp := oldOp.Responses[status]
		rp := base + " -> response " + status
		newResp, ok := newOp.Responses[status]
		if !ok {
			c.add(LevelBreaking, KindRemoved, DirectionResponse, rp,
				"response status was removed", oldResp.Loc)
			continue
		}
		switch {
		case oldResp.Schema != nil && newResp.Schema == nil:
			c.add(LevelBreaking, KindRemoved, DirectionResponse, rp,
				"response schema was removed", newResp.Loc)
		case oldResp.Schema == nil && newResp.Schema != nil:
			c.add(LevelInfo, KindAdded, DirectionResponse, rp,
				"response schema was added", newResp.Loc)
		case oldResp.Schema != nil && newResp.Schema != nil:
			c.checkSchema(oldResp.Schema, newResp.Schema, DirectionResponse, rp+" -> schema")
		}
	}
	for _, status := range spec.SortedKeys(newOp.Responses) {
		if _, ok := oldOp.Responses[status]; !ok {
			c.add(LevelInfo, KindAdded, DirectionResponse,
				base+" -> response "+status,
				"response status was added", newOp.Responses[status].Loc)
		}
	}
}
