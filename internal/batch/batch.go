package batch

import (
	"runtime"
	"sort"
	"sync"

	"github.com/shikejing/api-spec-compat-go/internal/compat"
	"github.com/shikejing/api-spec-compat-go/internal/rules"
	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// Pair is one old/new comparison request.
type Pair struct {
	Name    string `json:"name,omitempty"`
	OldFile string `json:"oldFile"`
	NewFile string `json:"newFile"`
}

// LintSummary is the aggregated lint output for many files.
type LintSummary struct {
	Tool       string             `json:"tool"`
	Results    []rules.FileResult `json:"results"`
	ErrorCount int                `json:"errorCount"`
	WarnCount  int                `json:"warningCount"`
	Passed     bool               `json:"passed"`
}

// DiffSummary is the aggregated compatibility output for many pairs.
type DiffSummary struct {
	Tool        string          `json:"tool"`
	Reports     []compat.Report `json:"reports"`
	BreakingAny bool            `json:"breaking"`
}

// Runner performs concurrent, order-independent batch processing.
type Runner struct {
	Loader     *spec.Loader
	Engine     *rules.Engine
	Comparator *compat.Comparator
	MaxWorkers int
}

// NewRunner creates a Runner with sane defaults.
func NewRunner(l *spec.Loader, e *rules.Engine, workers int) *Runner {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	return &Runner{Loader: l, Engine: e, Comparator: compat.NewComparator(), MaxWorkers: workers}
}

// LintAll lints many files; one file failing never aborts the others.
func (r *Runner) LintAll(paths []string) *LintSummary {
	results := make([]rules.FileResult, len(paths))
	sem := make(chan struct{}, r.MaxWorkers)
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = r.Engine.LintFile(r.Loader, p)
		}(i, p)
	}
	wg.Wait()

	sort.SliceStable(results, func(i, j int) bool { return results[i].File < results[j].File })

	sum := &LintSummary{Tool: "ascheck", Results: results, Passed: true}
	for _, res := range results {
		if res.Status == "failed" || res.Status == "load-error" {
			sum.Passed = false
		}
		for _, f := range res.Findings {
			switch f.Severity {
			case rules.SeverityError:
				sum.ErrorCount++
			case rules.SeverityWarning:
				sum.WarnCount++
			}
		}
	}
	return sum
}

// DiffAll compares many pairs; one pair failing never aborts the others.
func (r *Runner) DiffAll(pairs []Pair) *DiffSummary {
	reports := make([]compat.Report, len(pairs))
	sem := make(chan struct{}, r.MaxWorkers)
	var wg sync.WaitGroup
	for i, pr := range pairs {
		wg.Add(1)
		go func(i int, pr Pair) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rep := r.Comparator.CompareFiles(r.Loader, pr.OldFile, pr.NewFile)
			if pr.Name != "" {
				rep.OldFile = pr.Name
			}
			reports[i] = *rep
		}(i, pr)
	}
	wg.Wait()

	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].OldFile != reports[j].OldFile {
			return reports[i].OldFile < reports[j].OldFile
		}
		return reports[i].NewFile < reports[j].NewFile
	})

	sum := &DiffSummary{Tool: "ascheck", Reports: reports}
	for _, rep := range reports {
		if rep.Status == "load-error" {
			sum.BreakingAny = true
		}
		if rep.HasBreaking() {
			sum.BreakingAny = true
		}
	}
	return sum
}
