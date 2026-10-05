// Package lint provides a configurable rule engine that checks a
// normalized document for specification violations.
package lint

import (
	"fmt"
	"os"
	"sort"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
	"gopkg.in/yaml.v3"
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

// fileConfig is the on-disk configuration format.
type fileConfig struct {
	Rules map[string]string `yaml:"rules" json:"rules"`
}

// LoadConfig reads a YAML/JSON config file of the form:
//
//	rules:
//	  operation-id-required: error
//	  path-no-trailing-slash: off
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	cfg := Config{}
	for id, sev := range fc.Rules {
		s := Severity(sev)
		switch s {
		case SeverityOff, SeverityWarn, SeverityError:
			cfg[id] = s
		default:
			return nil, fmt.Errorf("rule %q: invalid severity %q (want off|warn|error)", id, sev)
		}
	}
	return cfg, nil
}

// DefaultConfig enables every built-in rule at its default severity.
func DefaultConfig() Config {
	cfg := Config{}
	for _, r := range Rules() {
		cfg[r.ID()] = r.DefaultSeverity()
	}
	return cfg
}

// Rule is a single check over a normalized document.
type Rule interface {
	ID() string
	DefaultSeverity() Severity
	// Description explains what the rule enforces, for documentation.
	Description() string
	Check(doc *spec.Document, report func(Finding))
}

// Engine runs a set of rules against a document.
type Engine struct {
	cfg Config
}

// NewEngine builds an engine from a config. Rules absent from cfg use
// their default severity; unknown rule IDs are ignored.
func NewEngine(cfg Config) *Engine { return &Engine{cfg: cfg} }

// Validate reports configuration entries that do not match a known rule.
func ValidateConfig(cfg Config) []string {
	known := map[string]bool{}
	for _, r := range Rules() {
		known[r.ID()] = true
	}
	var problems []string
	for id := range cfg {
		if !known[id] {
			problems = append(problems, fmt.Sprintf("unknown rule %q", id))
		}
	}
	sort.Strings(problems)
	return problems
}

// Run evaluates all enabled rules and returns findings sorted
// deterministically by (file, line, col, ruleID, message). The same
// input always yields byte-identical findings.
func (e *Engine) Run(doc *spec.Document) []Finding {
	var out []Finding
	for _, r := range Rules() {
		sev, ok := e.cfg[r.ID()]
		if !ok {
			sev = r.DefaultSeverity()
		}
		if sev == SeverityOff {
			continue
		}
		r.Check(doc, func(f Finding) {
			f.RuleID = r.ID()
			f.Severity = sev
			out = append(out, f)
		})
	}
	SortFindings(out)
	return out
}

// SortFindings orders findings deterministically.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Location.File != b.Location.File {
			return a.Location.File < b.Location.File
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		if a.Location.Col != b.Location.Col {
			return a.Location.Col < b.Location.Col
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		return a.Message < b.Message
	})
}
