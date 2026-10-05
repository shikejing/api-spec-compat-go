// Package diff compares two normalized documents and classifies every
// change by backward-compatibility impact.
package diff

import (
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
	Path      string        `json:"path"` // logical path, e.g. GET /pets/{id} -> response 200 -> schema.name
	Reason    string        `json:"reason"`
	Location  spec.Location `json:"location,omitempty"`
}

// Report is the outcome of comparing two documents.
type Report struct {
	Changes []Change `json:"changes"`
}

// Breaking reports whether any breaking change was found.
func (r *Report) Breaking() bool { return false }

// Compare diffs oldDoc against newDoc. The result is deterministic.
func Compare(oldDoc, newDoc *spec.Document) *Report { return &Report{} }
