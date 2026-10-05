// Package batch runs lint (and optionally diff) over many documents
// concurrently, producing a deterministic machine-readable summary.
// A failure in one document never aborts the others, and the output
// does not depend on processing order or concurrency level.
package batch

import (
	"fmt"
	"runtime"
	"sort"
	"sync"

	"github.com/shikejing/api-spec-compat-go/internal/diff"
	"github.com/shikejing/api-spec-compat-go/internal/lint"
	"github.com/shikejing/api-spec-compat-go/internal/loader"
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
	LoadErrors int          `json:"loadErrors"`
	OK         bool         `json:"ok"`
}

// DiffResult is the outcome of comparing one pair of documents.
type DiffResult struct {
	OldFile string       `json:"oldFile"`
	NewFile string       `json:"newFile"`
	Error   string       `json:"error,omitempty"`
	Report  *diff.Report `json:"report,omitempty"`
}

// Breaking reports whether the pair has breaking changes.
func (d DiffResult) Breaking() bool {
	return d.Report != nil && d.Report.Breaking()
}

// Options configures a batch run.
type Options struct {
	// Concurrency is the number of worker goroutines; <= 0 uses
	// runtime.NumCPU().
	Concurrency int
	Config      lint.Config
}

// FilePair names an old/new document pair to diff.
type FilePair struct {
	Old string `json:"old"`
	New string `json:"new"`
}

func (o Options) workers() int {
	if o.Concurrency > 0 {
		return o.Concurrency
	}
	return runtime.NumCPU()
}

// LintFiles lints every file, never aborting on individual failures.
// Results are sorted by file name regardless of completion order.
func LintFiles(files []string, opts Options) Summary {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	results := make([]DocResult, len(sorted))
	runParallel(len(sorted), opts.workers(), func(i int) {
		results[i] = lintOne(sorted[i], opts.Config)
	})
	s := Summary{Results: results, OK: true}
	for _, r := range results {
		if r.Error != "" {
			s.LoadErrors++
			s.OK = false
		}
		for _, f := range r.Findings {
			switch f.Severity {
			case lint.SeverityError:
				s.ErrorCount++
			case lint.SeverityWarn:
				s.WarnCount++
			}
		}
	}
	if s.ErrorCount > 0 {
		s.OK = false
	}
	return s
}

// lintOne loads and lints a single file, converting panics into errors.
func lintOne(file string, cfg lint.Config) (r DocResult) {
	r.File = file
	defer func() {
		if rec := recover(); rec != nil {
			r.Error = fmt.Sprintf("internal error: %v", rec)
			r.Findings = nil
		}
	}()
	doc, err := loader.Load(file)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Findings = lint.NewEngine(cfg).Run(doc)
	if r.Findings == nil {
		r.Findings = []lint.Finding{}
	}
	return r
}

// DiffPairs compares each old/new pair concurrently. Results are
// sorted by (oldFile, newFile).
func DiffPairs(pairs []FilePair, opts Options) []DiffResult {
	sorted := append([]FilePair(nil), pairs...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Old != sorted[j].Old {
			return sorted[i].Old < sorted[j].Old
		}
		return sorted[i].New < sorted[j].New
	})
	results := make([]DiffResult, len(sorted))
	runParallel(len(sorted), opts.workers(), func(i int) {
		results[i] = diffOne(sorted[i])
	})
	return results
}

func diffOne(p FilePair) (r DiffResult) {
	r.OldFile, r.NewFile = p.Old, p.New
	defer func() {
		if rec := recover(); rec != nil {
			r.Error = fmt.Sprintf("internal error: %v", rec)
			r.Report = nil
		}
	}()
	oldDoc, err := loader.Load(p.Old)
	if err != nil {
		r.Error = fmt.Sprintf("loading old document: %v", err)
		return r
	}
	newDoc, err := loader.Load(p.New)
	if err != nil {
		r.Error = fmt.Sprintf("loading new document: %v", err)
		return r
	}
	r.Report = diff.Compare(oldDoc, newDoc)
	return r
}

// runParallel executes fn(i) for i in [0,n) across w workers.
func runParallel(n, w int, fn func(int)) {
	if n == 0 {
		return
	}
	if w < 1 {
		w = 1
	}
	if w > n {
		w = n
	}
	var wg sync.WaitGroup
	jobs := make(chan int)
	for i := 0; i < w; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				fn(j)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}
