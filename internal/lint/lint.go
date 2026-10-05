// Package lint provides a configurable rule engine that checks a
// normalized document for specification violations.
package lint

import (
	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// Severity of a finding.
type Severity string

const (
	SeverityOff   Severity = "off"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// Finding is a single rule violation.
type Finding struct {
	RuleID     string        `json:"ruleId"`
	Severity   Severity      `json:"severity"`
	Message    string        `json:"message"`
	Suggestion string        `json:"suggestion,omitempty"`
	Location   spec.Location `json:"location"`
}

// Config maps rule IDs to their configured severity ("off" disables).
type Config map[string]Severity

// DefaultConfig returns the built-in configuration.
func DefaultConfig() Config { return Config{} }

// Engine runs a set of rules against a document.
type Engine struct {
	cfg Config
}

// NewEngine builds an engine from a config; unknown rules are ignored,
// rules absent from cfg use their default severity.
func NewEngine(cfg Config) *Engine { return &Engine{cfg: cfg} }

// Run evaluates all enabled rules and returns findings sorted
// deterministically by (file, line, col, ruleID).
func (e *Engine) Run(doc *spec.Document) []Finding { return nil }
