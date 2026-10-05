package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	os.WriteFile(p, []byte("rules:\n  info-title-required: error\n  path-params-defined: off\n"), 0o644)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules["info-title-required"] != SeverityError {
		t.Fatal("title severity wrong")
	}
	if cfg.Rules["path-params-defined"] != SeverityOff {
		t.Fatal("path severity wrong")
	}
}

func TestLoadConfigInvalidSeverity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	os.WriteFile(p, []byte("rules:\n  x: catastrophic\n"), 0o644)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("expected invalid severity error")
	}
}

func TestParseSeverity(t *testing.T) {
	for _, ok := range []string{"off", "info", "warning", "error", " ERROR "} {
		if _, good := ParseSeverity(ok); !good {
			t.Fatalf("severity %q rejected", ok)
		}
	}
	if _, good := ParseSeverity("nope"); good {
		t.Fatal("invalid severity accepted")
	}
}
