package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ascheck")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

const cliV1 = `openapi: 3.0.0
info: {title: API, version: "1"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  name: {type: string}
`

const cliV2 = `openapi: 3.0.0
info: {title: API, version: "2"}
paths:
  /a:
    get:
      operationId: getA
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: integer}
`

func TestCLIIntegration(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	v1 := filepath.Join(dir, "v1.yaml")
	v2 := filepath.Join(dir, "v2.yaml")
	os.WriteFile(v1, []byte(cliV1), 0o644)
	os.WriteFile(v2, []byte(cliV2), 0o644)

	// lint passes -> exit 0, parseable JSON.
	cmd := exec.Command(bin, "lint", v1)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("lint exit: %v", err)
	}
	var lint map[string]any
	if err := json.Unmarshal(out, &lint); err != nil {
		t.Fatalf("lint json: %v", err)
	}
	if lint["passed"] != true {
		t.Fatalf("lint not passed: %s", out)
	}

	// diff detects response property removed (breaking) -> exit 1.
	cmd = exec.Command(bin, "diff", "--pair", v1+"="+v2)
	out, err = cmd.Output()
	var exitErr *exec.ExitError
	if err == nil {
		t.Fatal("expected nonzero exit for breaking diff")
	} else if !asExitError(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("want exit 1, got %v", err)
	}
	var diff map[string]any
	if err := json.Unmarshal(out, &diff); err != nil {
		t.Fatalf("diff json: %v: %s", err, out)
	}
	if diff["breaking"] != true {
		t.Fatalf("diff not breaking: %s", out)
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

func TestCLILoadErrorIsolationExit(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "g.yaml")
	os.WriteFile(good, []byte(cliV1), 0o644)
	missing := filepath.Join(dir, "nope.yaml")
	cmd := exec.Command(bin, "lint", good, missing)
	out, _ := cmd.Output()
	var sum map[string]any
	if err := json.Unmarshal(out, &sum); err != nil {
		t.Fatalf("json: %v: %s", err, out)
	}
	results := sum["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("expected both results despite one missing, got %d", len(results))
	}
	if sum["passed"] != false {
		t.Fatal("batch should fail")
	}
}
