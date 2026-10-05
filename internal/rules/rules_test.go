package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

const specBad = `
openapi: 3.0.0
info:
  title: ""
paths:
  /pets:
    get:
      operationId: dup
      responses:
        '200':
          content:
            application/json:
              schema:
                type: array
    post:
      operationId: dup
      parameters:
        - name: body
          in: query
          schema:
            type: object
            required: [missing]
            properties:
              badName:
                type: string
                enum: [a, a]
      responses:
        '500':
          description: err
  /pets/{id}:
    get:
      responses:
        '200':
          description: ok
`

func lintFixture(t *testing.T, cfg Config) (FileResult, *spec.Loader) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(p, []byte(specBad), 0o644); err != nil {
		t.Fatal(err)
	}
	l := spec.NewLoader()
	e := NewEngine(cfg)
	return e.LintFile(l, p), l
}

func ruleIDs(fs []Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.RuleID]++
	}
	return m
}

func TestBuiltinRulesFire(t *testing.T) {
	res, _ := lintFixture(t, DefaultConfig())
	if res.Status != "failed" {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	ids := ruleIDs(res.Findings)
	for _, id := range []string{
		"info-title-required",
		"operation-operationId-unique",
		"path-params-defined",
		"response-success-defined",
		"array-items-defined",
		"enum-unique-values",
		"object-required-defined",
	} {
		if ids[id] == 0 {
			t.Errorf("rule %s did not fire; got ids=%v", id, ids)
		}
	}
	for _, f := range res.Findings {
		if f.RuleID == "" || f.Location.Line == 0 {
			t.Errorf("finding lacks id or location: %+v", f)
		}
		if f.Fix == "" {
			t.Errorf("finding %s lacks fix suggestion", f.RuleID)
		}
	}
}

func TestDeterministicRepeat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(p, []byte(specBad), 0o644); err != nil {
		t.Fatal(err)
	}
	l := spec.NewLoader()
	e := NewEngine(DefaultConfig())
	r1 := e.LintFile(l, p)
	r2 := e.LintFile(l, p)
	if len(r1.Findings) != len(r2.Findings) {
		t.Fatal("inconsistent finding counts")
	}
	for i := range r1.Findings {
		a, b := r1.Findings[i], r2.Findings[i]
		if a != b {
			t.Fatalf("finding %d differs:\n%+v\n%+v", i, a, b)
		}
	}
}

func TestSeverityConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg = cfg.SetRule("path-params-defined", SeverityOff)
	cfg = cfg.SetRule("info-title-required", SeverityError)
	res, _ := lintFixture(t, cfg)
	ids := ruleIDs(res.Findings)
	if ids["path-params-defined"] != 0 {
		t.Fatal("disabled rule still fired")
	}
	var titleSev Severity
	for _, f := range res.Findings {
		if f.RuleID == "info-title-required" {
			titleSev = f.Severity
		}
	}
	if titleSev != SeverityError {
		t.Fatalf("title severity = %q", titleSev)
	}
}

func TestNamingRuleOffByDefault(t *testing.T) {
	res, _ := lintFixture(t, DefaultConfig())
	if ruleIDs(res.Findings)["property-naming-snake-case"] != 0 {
		t.Fatal("opt-in naming rule fired by default")
	}
	cfg := DefaultConfig().SetRule("property-naming-snake-case", SeverityWarning)
	res2, _ := lintFixture(t, cfg)
	if ruleIDs(res2.Findings)["property-naming-snake-case"] == 0 {
		t.Fatal("naming rule did not fire when enabled")
	}
}

func TestFindingsSorted(t *testing.T) {
	res, _ := lintFixture(t, DefaultConfig())
	for i := 1; i < len(res.Findings); i++ {
		a, b := res.Findings[i-1].Location, res.Findings[i].Location
		if a.Line > b.Line {
			t.Fatalf("findings not line-sorted at %d: %d > %d", i, a.Line, b.Line)
		}
	}
}

func TestCycleRuleFires(t *testing.T) {
	doc := `
openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /t:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Node"
components:
  schemas:
    Node:
      type: object
      properties:
        next:
          $ref: "#/components/schemas/Node"
`
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte(doc), 0o644)
	res := NewEngine(DefaultConfig()).LintFile(spec.NewLoader(), p)
	var found bool
	for _, f := range res.Findings {
		if f.RuleID == "ref-no-cycles" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ref-no-cycles did not fire; findings=%+v", res.Findings)
	}
}

func TestLoadErrorFileResult(t *testing.T) {
	res := NewEngine(DefaultConfig()).LintFile(spec.NewLoader(), "/no/such/file.yaml")
	if res.Status != "load-error" || res.Error == "" {
		t.Fatalf("result=%+v", res)
	}
}
