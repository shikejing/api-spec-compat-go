package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// writeDeepChainDoc generates a document with a deep chain of
// component references (N0 -> N1 -> ... -> Nd) and `refs` operations
// all referencing the chain head.
func writeDeepChainDoc(tb testing.TB, dir string, depth, refs int) string {
	tb.Helper()
	var b strings.Builder
	b.WriteString("openapi: 3.0.0\ninfo: {title: Perf, version: 1.0.0}\npaths:\n")
	for i := 0; i < refs; i++ {
		fmt.Fprintf(&b, "  /p%d:\n    get:\n      operationId: op%d\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema:\n                $ref: '#/components/schemas/N0'\n", i, i)
	}
	b.WriteString("components:\n  schemas:\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "    N%d:\n      type: object\n      properties:\n        next:\n          $ref: '#/components/schemas/N%d'\n", i, i+1)
	}
	fmt.Fprintf(&b, "    N%d:\n      type: object\n      properties:\n        leaf: {type: string}\n", depth)
	path := filepath.Join(dir, "perf.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}

func responseSchema(doc *spec.Document, path string) *spec.Schema {
	return spec.Deref(doc.Paths[path].Operations["get"].Responses["200"].Schema)
}

func TestDeepChainAndRepeatedRefs(t *testing.T) {
	dir := t.TempDir()
	path := writeDeepChainDoc(t, dir, 200, 2000)
	start := time.Now()
	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	elapsed := time.Since(start)
	if len(doc.Paths) != 2000 {
		t.Fatalf("paths = %d", len(doc.Paths))
	}
	// All 2000 references must share one resolved schema instance:
	// resolution cost does not grow with occurrence count.
	first := responseSchema(doc, "/p0")
	for i := 1; i < 2000; i++ {
		if got := responseSchema(doc, fmt.Sprintf("/p%d", i)); got != first {
			t.Fatalf("path %d resolved to a different schema instance", i)
		}
	}
	if elapsed > 10*time.Second {
		t.Fatalf("load took %v; resolution cost appears superlinear", elapsed)
	}
	t.Logf("loaded 2000 refs over depth-200 chain in %v", elapsed)
}

func BenchmarkRepeatedRefs(b *testing.B) {
	dir := b.TempDir()
	path := writeDeepChainDoc(b, dir, 50, 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load(path); err != nil {
			b.Fatal(err)
		}
	}
}
