package lint

import (
	"testing"

	"github.com/shikejing/api-spec-compat-go/internal/loader"
)

func TestRulesFire(t *testing.T) {
	doc, err := loader.Load("testdata/violations.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	findings := NewEngine(DefaultConfig()).Run(doc)
	byRule := map[string]int{}
	for _, f := range findings {
		byRule[f.RuleID]++
		if f.Location.File == "" {
			t.Errorf("finding %v lacks location", f)
		}
		if f.Severity != SeverityWarn && f.Severity != SeverityError {
			t.Errorf("finding %v has bad severity", f)
		}
	}
	for _, want := range []string{
		"operation-id-unique",
		"path-no-trailing-slash",
		"path-param-declared",
		"response-description-required",
		"success-response-schema",
		"schema-type-required",
		"enum-values-valid",
		"param-name-case",
	} {
		if byRule[want] == 0 {
			t.Errorf("expected rule %q to fire, got findings %v", want, byRule)
		}
	}
	// operation-id-required must NOT fire here (all ops have ids).
	if byRule["operation-id-required"] != 0 {
		t.Errorf("operation-id-required fired unexpectedly")
	}
}

func TestDeterministic(t *testing.T) {
	doc, err := loader.Load("testdata/violations.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	first := NewEngine(DefaultConfig()).Run(doc)
	for i := 0; i < 5; i++ {
		got := NewEngine(DefaultConfig()).Run(doc)
		if len(got) != len(first) {
			t.Fatalf("run %d: %d findings vs %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d finding %d differs: %+v vs %+v", i, j, got[j], first[j])
			}
		}
	}
}

func TestConfigOffAndSeverity(t *testing.T) {
	doc, err := loader.Load("testdata/violations.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := DefaultConfig()
	cfg["path-no-trailing-slash"] = SeverityOff
	cfg["param-name-case"] = SeverityError
	findings := NewEngine(cfg).Run(doc)
	for _, f := range findings {
		if f.RuleID == "path-no-trailing-slash" {
			t.Errorf("disabled rule fired: %+v", f)
		}
		if f.RuleID == "param-name-case" && f.Severity != SeverityError {
			t.Errorf("severity override not applied: %+v", f)
		}
	}
}

func TestValidateConfig(t *testing.T) {
	problems := ValidateConfig(Config{"no-such-rule": SeverityWarn})
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if problems := ValidateConfig(DefaultConfig()); len(problems) != 0 {
		t.Fatalf("default config invalid: %v", problems)
	}
}
