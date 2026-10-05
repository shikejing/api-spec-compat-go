package main

import (
	"os"
	"path/filepath"
	"testing"
)

const goodDoc = `openapi: 3.0.0
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

const badDoc = `openapi: 3.0.0
info: {title: Bad, version: 1.0.0}
paths:
  /bad:
    get:
      responses:
        '200':
          description: ok
`

func writeDoc(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLintExitCodes(t *testing.T) {
	good := writeDoc(t, "good.yaml", goodDoc)
	bad := writeDoc(t, "bad.yaml", badDoc)
	missing := filepath.Join(t.TempDir(), "missing.yaml")

	if code := run([]string{"lint", good}); code != 0 {
		t.Errorf("lint good: exit %d, want 0", code)
	}
	if code := run([]string{"lint", bad}); code != 1 {
		t.Errorf("lint bad: exit %d, want 1", code)
	}
	// One bad file must not prevent the good one from being processed;
	// the exit code stays non-zero.
	if code := run([]string{"lint", good, bad, missing}); code != 1 {
		t.Errorf("lint mixed: exit %d, want 1", code)
	}
	if code := run([]string{"lint"}); code != 2 {
		t.Errorf("lint no args: exit %d, want 2", code)
	}
}

func TestDiffExitCodes(t *testing.T) {
	v1 := writeDoc(t, "v1.yaml", goodDoc)
	v2 := writeDoc(t, "v2.yaml", badDoc) // path removed -> breaking
	if code := run([]string{"diff", v1, v1}); code != 0 {
		t.Errorf("diff identical: exit %d, want 0", code)
	}
	if code := run([]string{"diff", v1, v2}); code != 1 {
		t.Errorf("diff breaking: exit %d, want 1", code)
	}
	if code := run([]string{"diff", v1}); code != 2 {
		t.Errorf("diff one arg: exit %d, want 2", code)
	}
}

func TestRulesCommand(t *testing.T) {
	if code := run([]string{"rules"}); code != 0 {
		t.Errorf("rules: exit %d", code)
	}
	if code := run([]string{"nope"}); code != 2 {
		t.Errorf("unknown command: exit %d, want 2", code)
	}
}
