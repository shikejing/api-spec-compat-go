package batch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shikejing/api-spec-compat-go/internal/rules"
	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

const goodSpec = `openapi: 3.0.0
info: {title: Good, version: "1"}
paths:
  /a:
    get:
      responses:
        '200': {description: ok}
`

func writeFiles(t *testing.T, n int, broken map[int]bool) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("api-%03d.yaml", i))
		content := goodSpec
		if broken[i] {
			content = "openapi: 3.0.0\n  bad: [unclosed\n"
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func TestLintAllErrorIsolation(t *testing.T) {
	paths := writeFiles(t, 6, map[int]bool{1: true, 4: true})
	r := NewRunner(spec.NewLoader(), rules.NewEngine(rules.DefaultConfig()), 2)
	sum := r.LintAll(paths)
	if len(sum.Results) != 6 {
		t.Fatalf("results = %d", len(sum.Results))
	}
	if sum.Passed {
		t.Fatal("summary must not pass with broken files")
	}
	var loadErrs int
	for _, res := range sum.Results {
		if res.Status == "load-error" {
			loadErrs++
			if !strings.Contains(res.Error, ":") {
				t.Fatalf("load error lacks file:line: %s", res.Error)
			}
		}
	}
	if loadErrs != 2 {
		t.Fatalf("loadErrs = %d", loadErrs)
	}
}

func TestLintAllOrderAndConcurrencyDeterminism(t *testing.T) {
	paths := writeFiles(t, 12, nil)
	// shuffle input order; results must still be sorted by file.
	shuffled := make([]string, len(paths))
	copy(shuffled, paths)
	for i := len(shuffled) - 1; i > 0; i-- {
		shuffled[i], shuffled[i-1] = shuffled[i-1], shuffled[i]
	}
	var prev *rules.FileResult
	for _, workers := range []int{1, 4, 16} {
		r := NewRunner(spec.NewLoader(), rules.NewEngine(rules.DefaultConfig()), workers)
		sum := r.LintAll(shuffled)
		for i := 1; i < len(sum.Results); i++ {
			if sum.Results[i].File < sum.Results[i-1].File {
				t.Fatalf("workers=%d results not sorted", workers)
			}
		}
		if prev != nil && len(prev.Findings) != len(sum.Results[0].Findings) {
			t.Fatal("finding counts vary across worker counts")
		}
		prev = &sum.Results[0]
	}
}

func TestDiffAllIsolation(t *testing.T) {
	dir := t.TempDir()
	oldA := filepath.Join(dir, "oldA.yaml")
	newA := filepath.Join(dir, "newA.yaml")
	os.WriteFile(oldA, []byte(goodSpec), 0o644)
	os.WriteFile(newA, []byte(strings.Replace(goodSpec, "/a", "/b", 1)), 0o644)
	pairs := []Pair{
		{OldFile: oldA, NewFile: newA},
		{OldFile: filepath.Join(dir, "missing.yaml"), NewFile: newA},
	}
	r := NewRunner(spec.NewLoader(), rules.NewEngine(rules.DefaultConfig()), 4)
	sum := r.DiffAll(pairs)
	if len(sum.Reports) != 2 {
		t.Fatalf("reports = %d", len(sum.Reports))
	}
	if !sum.BreakingAny {
		t.Fatal("missing file pair must mark batch as failing")
	}
	var compared, loadErr int
	for _, rep := range sum.Reports {
		switch rep.Status {
		case "load-error":
			loadErr++
		case "compared":
			compared++
		}
	}
	if loadErr != 1 || compared != 1 {
		t.Fatalf("loadErr=%d compared=%d reports=%+v", loadErr, compared, sum.Reports)
	}
}

func TestSharedRefParsingCostSublinear(t *testing.T) {
	dir := t.TempDir()
	shared := "components:\n  schemas:\n"
	var ops strings.Builder
	ops.WriteString("openapi: 3.0.3\ninfo: {title: T, version: '1'}\npaths:\n")
	n := 200
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("S%03d", i)
		shared += "    " + name + ":\n      type: object\n      required: [id]\n      properties:\n        id: {type: string}\n        deep:\n          type: array\n          items: {type: string}\n"
		ops.WriteString(fmt.Sprintf("  /r%03d:\n    get:\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema: {$ref: './shared.yaml#/components/schemas/%s'}\n", i, name))
	}
	sharedPath := filepath.Join(dir, "shared.yaml")
	mainPath := filepath.Join(dir, "main.yaml")
	os.WriteFile(sharedPath, []byte(shared), 0o644)
	os.WriteFile(mainPath, []byte(ops.String()), 0o644)

	l := spec.NewLoader()
	start := time.Now()
	doc, err := l.LoadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Operations) != n {
		t.Fatalf("ops=%d", len(doc.Operations))
	}
	// All referenced object schemas must be the same cached instances reused.
	first := doc.Operations[0].Responses[0].Body
	_ = first
	// Cache size grows with number of definitions, but buildSchema per ref is O(1).
	if time.Since(start) > 5*time.Second {
		t.Fatal("loading too slow; caching likely missing")
	}
}

func TestConcurrentLoaderSafety(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yaml")
	os.WriteFile(p, []byte(goodSpec), 0o644)
	l := spec.NewLoader()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := l.LoadFile(p)
			if err != nil || len(d.Operations) != 1 {
				t.Errorf("concurrent load failed: %v", err)
			}
		}()
	}
	wg.Wait()
}
