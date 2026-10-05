package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/batch"
	"github.com/shikejing/api-spec-compat-go/internal/report"
	"github.com/shikejing/api-spec-compat-go/internal/rules"
	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

const usage = `ascheck - API spec linting and backward-compatibility analysis

Usage:
  ascheck lint [flags] FILE [FILE...]
  ascheck diff [flags] --pair OLD=NEW [--pair OLD=NEW ...]
  ascheck rules

Lint flags:
  --config PATH         YAML config with rule severities
  --set RULE=SEVERITY   override one rule (repeatable)
  --output PATH         write JSON summary to file (default: stdout)
  --workers N           concurrency (default: NumCPU)

Diff flags:
  --pair OLD=NEW        pair of versions to compare (repeatable)
  --output PATH         write JSON report to file (default: stdout)
  --workers N           concurrency (default: NumCPU)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "lint":
		os.Exit(runLint(os.Args[2:]))
	case "diff":
		os.Exit(runDiff(os.Args[2:]))
	case "rules":
		os.Exit(runRules(os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

type flagSet struct {
	args   []string
	values map[string][]string
}

func parseFlags(args []string, booleans map[string]bool) (*flagSet, error) {
	fs := &flagSet{values: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			keyVal := strings.TrimPrefix(a, "--")
			key := keyVal
			val := ""
			hasVal := false
			if idx := strings.IndexByte(keyVal, '='); idx >= 0 {
				key = keyVal[:idx]
				val = keyVal[idx+1:]
				hasVal = true
			}
			if booleans[key] {
				fs.values[key] = append(fs.values[key], "true")
				continue
			}
			if !hasVal {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag --%s requires a value", key)
				}
				val = args[i]
			}
			fs.values[key] = append(fs.values[key], val)
			continue
		}
		fs.args = append(fs.args, a)
	}
	return fs, nil
}

func (f *flagSet) first(name string) string {
	if v := f.values[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

func runLint(args []string) int {
	fs, err := parseFlags(args, map[string]bool{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(fs.args) == 0 {
		fmt.Fprintln(os.Stderr, "lint requires at least one FILE")
		return 2
	}
	cfg := rules.DefaultConfig()
	if cp := fs.first("config"); cp != "" {
		c, err := rules.LoadConfig(cp)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		// Merge: file values override defaults.
		for k, v := range c.Rules {
			cfg.Rules[k] = v
		}
	}
	for _, set := range fs.values["set"] {
		eq := strings.SplitN(set, "=", 2)
		if len(eq) != 2 {
			fmt.Fprintf(os.Stderr, "invalid --set %q, expected RULE=SEVERITY\n", set)
			return 2
		}
		sev, ok := rules.ParseSeverity(eq[1])
		if !ok {
			fmt.Fprintf(os.Stderr, "invalid severity %q\n", eq[1])
			return 2
		}
		cfg.Rules[eq[0]] = sev
	}

	workers := atoiOr(fs.first("workers"), 0)
	r := batch.NewRunner(spec.NewLoader(), rules.NewEngine(cfg), workers)
	summary := r.LintAll(fs.args)

	out := os.Stdout
	if op := fs.first("output"); op != "" {
		f, err := os.Create(op)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		defer f.Close()
		out = f
	}
	if err := report.JSON(out, summary); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if !summary.Passed {
		return 1
	}
	return 0
}

func runDiff(args []string) int {
	fs, err := parseFlags(args, map[string]bool{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var pairs []batch.Pair
	for _, p := range fs.values["pair"] {
		eq := strings.SplitN(p, "=", 2)
		if len(eq) != 2 {
			fmt.Fprintf(os.Stderr, "invalid --pair %q, expected OLD=NEW\n", p)
			return 2
		}
		pairs = append(pairs, batch.Pair{OldFile: eq[0], NewFile: eq[1]})
	}
	if len(pairs) == 0 {
		// Also accept positional form: exactly two files.
		if len(fs.args) == 2 {
			pairs = []batch.Pair{{OldFile: fs.args[0], NewFile: fs.args[1]}}
		} else {
			fmt.Fprintln(os.Stderr, "diff requires --pair OLD=NEW (repeatable) or two positional files")
			return 2
		}
	}
	workers := atoiOr(fs.first("workers"), 0)
	r := batch.NewRunner(spec.NewLoader(), rules.NewEngine(rules.DefaultConfig()), workers)
	summary := r.DiffAll(pairs)

	out := os.Stdout
	if op := fs.first("output"); op != "" {
		f, err := os.Create(op)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		defer f.Close()
		out = f
	}
	if err := report.JSON(out, summary); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if summary.BreakingAny {
		return 1
	}
	return 0
}

func runRules(args []string) int {
	type ruleInfo struct {
		ID      string `json:"id"`
		Default string `json:"default"`
	}
	var infos []ruleInfo
	for _, c := range rules.ListBuiltin() {
		infos = append(infos, ruleInfo{ID: c.ID(), Default: string(c.DefaultSeverity())})
	}
	return writeResult(infos)
}

func writeResult(v any) int {
	if err := report.JSON(os.Stdout, v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

func atoiOr(s string, def int) int {
	n := def
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return def
		}
		n = n*10 + int(s[i]-'0')
	}
	if s == "" {
		return def
	}
	return n
}
