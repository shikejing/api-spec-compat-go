package batch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/api-spec-compat-go/internal/lint"
)

func TestLintFilesIsolation(t *testing.T) {
	files := []string{
		"testdata/good.yaml",
		"testdata/bad.yaml",
		"testdata/invalid.yaml",
		"testdata/does-not-exist.yaml",
	}
	s := LintFiles(files, Options{Concurrency: 3, Config: lint.DefaultConfig()})
	if len(s.Results) != 4 {
		t.Fatalf("got %d results", len(s.Results))
	}
	// Results sorted by file name.
	for i := 1; i < len(s.Results); i++ {
		if s.Results[i-1].File > s.Results[i].File {
			t.Fatalf("results not sorted: %v", s.Results)
		}
	}
	byFile := map[string]DocResult{}
	for _, r := range s.Results {
		byFile[r.File] = r
	}
	if byFile["testdata/invalid.yaml"].Error == "" {
		t.Error("invalid.yaml should have a load error")
	}
	if byFile["testdata/does-not-exist.yaml"].Error == "" {
		t.Error("missing file should have a load error")
	}
	if byFile["testdata/bad.yaml"].Error != "" {
		t.Errorf("bad.yaml should load, got %v", byFile["testdata/bad.yaml"].Error)
	}
	// bad.yaml misses operationId -> error finding.
	found := false
	for _, f := range byFile["testdata/bad.yaml"].Findings {
		if f.RuleID == "operation-id-required" && f.Severity == lint.SeverityError {
			found = true
		}
	}
	if !found {
		t.Error("bad.yaml should produce operation-id-required error")
	}
	if s.OK {
		t.Error("summary should not be OK with errors present")
	}
	if s.LoadErrors != 2 {
		t.Errorf("LoadErrors = %d, want 2", s.LoadErrors)
	}
}

func TestDeterministicAcrossConcurrency(t *testing.T) {
	files := []string{
		"testdata/good.yaml",
		"testdata/bad.yaml",
		"testdata/invalid.yaml",
	}
	base := LintFiles(files, Options{Concurrency: 1, Config: lint.DefaultConfig()})
	for _, conc := range []int{2, 8} {
		got := LintFiles(files, Options{Concurrency: conc, Config: lint.DefaultConfig()})
		if len(got.Results) != len(base.Results) {
			t.Fatalf("conc=%d: result count differs", conc)
		}
		for i := range got.Results {
			a, b := base.Results[i], got.Results[i]
			if a.File != b.File || a.Error != b.Error || len(a.Findings) != len(b.Findings) {
				t.Fatalf("conc=%d result %d differs", conc, i)
			}
			for j := range a.Findings {
				if a.Findings[j] != b.Findings[j] {
					t.Fatalf("conc=%d finding %d differs", conc, j)
				}
			}
		}
	}
}

func TestDiffPairs(t *testing.T) {
	pairs := []FilePair{
		{Old: "testdata/good.yaml", New: "testdata/bad.yaml"},
		{Old: "testdata/good.yaml", New: "testdata/good.yaml"},
		{Old: "testdata/missing.yaml", New: "testdata/good.yaml"},
	}
	results := DiffPairs(pairs, Options{Concurrency: 2})
	if len(results) != 3 {
		t.Fatalf("got %d results", len(results))
	}
	for _, r := range results {
		if r.OldFile == "testdata/missing.yaml" {
			if r.Error == "" {
				t.Error("expected error for missing old file")
			}
			continue
		}
		if r.Error != "" {
			t.Errorf("unexpected error: %v", r.Error)
			continue
		}
		if r.OldFile == r.NewFile && r.Report.Breaking() {
			t.Error("identical pair must not be breaking")
		}
	}
}

func TestManyDocuments(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := 0; i < 100; i++ {
		p := filepath.Join(dir, fmt.Sprintf("doc%03d.yaml", i))
		content := good
		if i%10 == 0 {
			content = bad // every 10th doc has an error-level finding
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	s := LintFiles(files, Options{Concurrency: 8, Config: lint.DefaultConfig()})
	if len(s.Results) != 100 {
		t.Fatalf("results = %d", len(s.Results))
	}
	if s.ErrorCount != 10 {
		t.Errorf("ErrorCount = %d, want 10", s.ErrorCount)
	}
	if s.OK {
		t.Error("summary should not be OK")
	}
}

const good = `openapi: 3.0.0
info: {title: Good, version: 1.0.0}
paths:
  /ok:
    get:
      operationId: getOk
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema: {type: string}
`

const bad = `openapi: 3.0.0
info: {title: Bad, version: 1.0.0}
paths:
  /bad:
    get:
      responses:
        '200':
          description: ok
`
