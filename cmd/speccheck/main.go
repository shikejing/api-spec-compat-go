// Command speccheck lints interface description documents and diffs
// document versions for backward compatibility. All processing is
// local; no external services are used.
//
// Usage:
//
//	speccheck lint  [--config f] [--format json|text] [--jobs N] file...
//	speccheck diff  [--format json|text] [--fail-on warning|breaking] OLD NEW
//
// Exit status: 0 when everything passes, 1 when any document fails to
// load, any error-severity finding exists, or breaking (or, with
// --fail-on=warning, warning-level) changes are detected; 2 on usage
// errors.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/shikejing/api-spec-compat-go/internal/batch"
	"github.com/shikejing/api-spec-compat-go/internal/diff"
	"github.com/shikejing/api-spec-compat-go/internal/lint"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "lint":
		return cmdLint(args[1:])
	case "diff":
		return cmdDiff(args[1:])
	case "rules":
		return cmdRules()
	default:
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  speccheck lint  [--config f] [--format json|text] [--jobs N] file...
  speccheck diff  [--format json|text] [--fail-on warning|breaking] OLD NEW
  speccheck rules`)
}

func cmdLint(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	configPath := fs.String("config", "", "rule config file (YAML)")
	format := fs.String("format", "json", "output format: json|text")
	jobs := fs.Int("jobs", 0, "concurrency (0 = NumCPU)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "lint: no input files")
		return 2
	}
	cfg := lint.DefaultConfig()
	if *configPath != "" {
		overrides, err := lint.LoadConfig(*configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lint: "+err.Error())
			return 2
		}
		for _, p := range lint.ValidateConfig(overrides) {
			fmt.Fprintln(os.Stderr, "lint: "+p)
			return 2
		}
		for id, sev := range overrides {
			cfg[id] = sev
		}
	}
	summary := batch.LintFiles(files, batch.Options{Concurrency: *jobs, Config: cfg})
	switch *format {
	case "json":
		writeJSON(summary)
	case "text":
		for _, r := range summary.Results {
			if r.Error != "" {
				fmt.Printf("%s: ERROR %s\n", r.File, r.Error)
				continue
			}
			for _, f := range r.Findings {
				fmt.Printf("%s: %s [%s] %s\n", f.Location, f.Severity, f.RuleID, f.Message)
				if f.Suggestion != "" {
					fmt.Printf("  suggestion: %s\n", f.Suggestion)
				}
			}
		}
		fmt.Printf("errors=%d warnings=%d loadErrors=%d ok=%v\n",
			summary.ErrorCount, summary.WarnCount, summary.LoadErrors, summary.OK)
	default:
		fmt.Fprintln(os.Stderr, "lint: unknown format "+*format)
		return 2
	}
	if !summary.OK {
		return 1
	}
	return 0
}

func cmdDiff(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	format := fs.String("format", "json", "output format: json|text")
	failOn := fs.String("fail-on", "breaking", "minimum level causing a non-zero exit: breaking|warning")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintln(os.Stderr, "diff: need exactly OLD and NEW files")
		return 2
	}
	results := batch.DiffPairs([]batch.FilePair{{Old: rest[0], New: rest[1]}}, batch.Options{})
	r := results[0]
	if r.Error != "" {
		fmt.Fprintln(os.Stderr, "diff: "+r.Error)
		return 1
	}
	switch *format {
	case "json":
		writeJSON(r.Report)
	case "text":
		for _, c := range r.Report.Changes {
			fmt.Printf("%s [%s/%s] %s: %s\n", c.Level, c.Direction, c.Kind, c.Path, c.Reason)
		}
		counts := r.Report.Counts()
		fmt.Printf("breaking=%d warning=%d info=%d\n",
			counts[diff.LevelBreaking], counts[diff.LevelWarning], counts[diff.LevelInfo])
	default:
		fmt.Fprintln(os.Stderr, "diff: unknown format "+*format)
		return 2
	}
	counts := r.Report.Counts()
	switch *failOn {
	case "breaking":
		if counts[diff.LevelBreaking] > 0 {
			return 1
		}
	case "warning":
		if counts[diff.LevelBreaking]+counts[diff.LevelWarning] > 0 {
			return 1
		}
	default:
		fmt.Fprintln(os.Stderr, "diff: unknown --fail-on value "+*failOn)
		return 2
	}
	return 0
}

func cmdRules() int {
	for _, r := range lint.Rules() {
		fmt.Printf("%s (default: %s)\n  %s\n", r.ID(), r.DefaultSeverity(), r.Description())
	}
	return 0
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "encoding output: "+err.Error())
	}
}
