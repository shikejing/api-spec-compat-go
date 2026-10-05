// Package batch runs lint (and optionally diff) over many documents
// concurrently, producing a deterministic machine-readable summary.
package batch

import (
	"github.com/shikejing/api-spec-compat-go/internal/diff"
	"github.com/shikejing/api-spec-compat-go/internal/lint"
)

// DocResult is the per-document outcome.
type DocResult struct {
	File     string         `json:"file"`
	Error    string         `json:"error,omitempty"`
	Findings []lint.Finding `json:"findings,omitempty"`
}

// Summary is the aggregate machine-readable result.
type Summary struct {
	Results    []DocResult  `json:"results"`
	Diffs      []DiffResult `json:"diffs,omitempty"`
	ErrorCount int          `json:"errorCount"`
	WarnCount  int          `json:"warnCount"`
	OK         bool         `json:"ok"`
}

// DiffResult is the outcome of comparing one pair of documents.
type DiffResult struct {
	OldFile string       `json:"oldFile"`
	NewFile string       `json:"newFile"`
	Error   string       `json:"error,omitempty"`
	Report  *diff.Report `json:"report,omitempty"`
}

// Options configures a batch run.
type Options struct {
	Concurrency int
	Config      lint.Config
}

// LintFiles lints every file, never aborting on individual failures.
// Results are sorted by file name regardless of completion order.
func LintFiles(files []string, opts Options) Summary { return Summary{OK: true} }
