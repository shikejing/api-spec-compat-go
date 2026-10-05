package compat

import "github.com/shikejing/api-spec-compat-go/internal/spec"

// Level is the backward-compatibility classification.
type Level string

const (
	LevelCompat   Level = "compatible"
	LevelWarning  Level = "potentially-breaking"
	LevelBreaking Level = "breaking"
)

// Direction distinguishes the wire direction a change applies to.
type Direction string

const (
	DirectionRequest  Direction = "request"
	DirectionResponse Direction = "response"
)

// Kind enumerates the change shapes we classify.
type Kind string

const (
	KindAdded               Kind = "added"
	KindRemoved             Kind = "removed"
	KindTypeChanged         Kind = "type-changed"
	KindTypeNarrowed        Kind = "type-narrowed"
	KindTypeWidened         Kind = "type-widened"
	KindRequiredAdded       Kind = "required-added"
	KindRequiredRemoved     Kind = "required-removed"
	KindEnumNarrowed        Kind = "enum-narrowed"
	KindEnumWidened         Kind = "enum-widened"
	KindConstraintTightened Kind = "constraint-tightened"
	KindConstraintRelaxed   Kind = "constraint-relaxed"
	KindDeprecated          Kind = "deprecated"
	KindUndefined           Kind = "undefined-construct"
)

// Change is one classified difference.
type Change struct {
	Level       Level          `json:"level"`
	Direction   Direction      `json:"direction"`
	Kind        Kind           `json:"kind"`
	Path        string         `json:"path"`
	Operation   string         `json:"operation,omitempty"`
	Description string         `json:"description"`
	Reason      string         `json:"reason,omitempty"`
	OldLocation *spec.Location `json:"oldLocation,omitempty"`
	NewLocation *spec.Location `json:"newLocation,omitempty"`
}

// Report is the deterministic result of comparing two versions.
type Report struct {
	OldFile string   `json:"oldFile"`
	NewFile string   `json:"newFile"`
	Status  string   `json:"status"`
	Error   string   `json:"error,omitempty"`
	Changes []Change `json:"changes"`
}
