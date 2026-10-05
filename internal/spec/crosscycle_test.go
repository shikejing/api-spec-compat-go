package spec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCrossFileCycleDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.yaml")
	b := filepath.Join(dir, "b.yaml")
	main := filepath.Join(dir, "main.yaml")
	os.WriteFile(a, []byte(`
A:
  type: object
  properties:
    b:
      $ref: "b.yaml#/B"
`), 0o644)
	os.WriteFile(b, []byte(`
B:
  type: object
  properties:
    a:
      $ref: "a.yaml#/A"
`), 0o644)
	os.WriteFile(main, []byte(`
openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: "a.yaml#/A"
`), 0o644)
	done := make(chan error, 1)
	go func() {
		_, err := NewLoader().LoadFile(main)
		done <- err
	}()
	select {
	case err := <-done:
		// Loading must terminate (cycles terminate at back-edges, not hard errors).
		if err != nil {
			t.Fatalf("cross-file cycle caused error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cross-file reference cycle hung")
	}
}

func TestOpenAPI31TypeArrayNullable(t *testing.T) {
	doc := `
openapi: 3.1.0
info: {title: T, version: "1"}
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: [string, "null"]
                enum: [a, b, null]
`
	p := writeTemp(t, "api.yaml", doc)
	d, err := NewLoader().LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := d.Operations[0].Responses[0].Body
	if !s.Nullable {
		t.Fatalf("type:[string,null] should be nullable: %+v", s)
	}
	if len(s.Enum) != 3 {
		t.Fatalf("enum len=%d", len(s.Enum))
	}
}
