package rules

import (
	"sort"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// Severity of a rule or a finding.
type Severity string

const (
	SeverityOff     Severity = "off"
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Finding is one deterministic validation result.
type Finding struct {
	RuleID   string        `json:"ruleId"`
	Severity Severity      `json:"severity"`
	Message  string        `json:"message"`
	Location spec.Location `json:"location"`
	Fix      string        `json:"fix,omitempty"`
	Path     string        `json:"path,omitempty"`
}

// FileResult aggregates findings for one input document.
type FileResult struct {
	File     string    `json:"file"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Findings []Finding `json:"findings"`
}

// Config carries enable/severity switches for rules.
type Config struct {
	Rules map[string]Severity `json:"rules,omitempty"`
}

// DefaultConfig returns all built-in rules at their default severity.
func DefaultConfig() Config {
	cfg := Config{Rules: map[string]Severity{}}
	for _, c := range builtinCheckers() {
		cfg.Rules[c.ID()] = c.DefaultSeverity()
	}
	return cfg
}

// ParseSeverity parses a severity token; invalid values yield false.
func ParseSeverity(s string) (Severity, bool) {
	switch Severity(strings.ToLower(strings.TrimSpace(s))) {
	case SeverityOff:
		return SeverityOff, true
	case SeverityInfo:
		return SeverityInfo, true
	case SeverityWarning:
		return SeverityWarning, true
	case SeverityError:
		return SeverityError, true
	default:
		return "", false
	}
}

// EffectiveSeverity resolves the configured severity for a rule id; an
// unknown/unconfigured rule falls back to its default, "off" disables it.
func (c Config) EffectiveSeverity(id string) Severity {
	if s, ok := c.Rules[id]; ok {
		return s
	}
	for _, ck := range builtinCheckers() {
		if ck.ID() == id {
			return ck.DefaultSeverity()
		}
	}
	return SeverityOff
}

// Engine runs the configured rule set against documents.
type Engine struct {
	cfg    Config
	checks []Checker
}

// Checker is one validation rule.
type Checker interface {
	ID() string
	DefaultSeverity() Severity
	Check(doc *spec.Document) []Finding
}

// NewEngine builds an engine with all built-in rules and the given config.
// Only rules present in cfg are switched, others keep their defaults.
func NewEngine(cfg Config) *Engine {
	if cfg.Rules == nil {
		cfg = DefaultConfig()
	}
	return &Engine{cfg: cfg, checks: builtinCheckers()}
}

// Lint validates a single already-loaded document and returns findings sorted
// deterministically by location, rule id and message.
func (e *Engine) Lint(doc *spec.Document) []Finding {
	out := []Finding{}
	for _, c := range e.checks {
		sev := e.cfg.EffectiveSeverity(c.ID())
		if sev == SeverityOff {
			continue
		}
		for _, f := range c.Check(doc) {
			f.RuleID = c.ID()
			f.Severity = sev
			out = append(out, f)
		}
	}
	sortFindings(out)
	return out
}

// LintFile loads and validates one file; load failures become a failed result.
func (e *Engine) LintFile(l *spec.Loader, path string) FileResult {
	doc, err := l.LoadFile(path)
	if err != nil {
		return FileResult{File: path, Status: "load-error", Error: err.Error(), Findings: []Finding{}}
	}
	findings := e.Lint(doc)
	status := "passed"
	for _, f := range findings {
		if f.Severity == SeverityError {
			status = "failed"
			break
		}
	}
	if status == "passed" {
		for _, f := range findings {
			if f.Severity == SeverityWarning {
				status = "warned"
				break
			}
		}
	}
	return FileResult{File: doc.Path, Status: status, Findings: findings}
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Location.File != b.Location.File {
			return a.Location.File < b.Location.File
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		if a.Location.Column != b.Location.Column {
			return a.Location.Column < b.Location.Column
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		return a.Message < b.Message
	})
}
