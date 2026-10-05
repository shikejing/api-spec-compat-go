package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const mainDoc = `
openapi: 3.0.3
info:
  title: T
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      parameters:
        - $ref: "./params.yaml#/LimitParam"
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: "./models.yaml#/PetList"
        '404':
          description: missing
  /pets/{id}:
    get:
      operationId: getPet
      parameters:
        - name: id
          in: path
          required: true
          schema: { type: string }
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: "./models.yaml#/Pet"
`

const modelsDoc = `
PetList:
  type: object
  properties:
    items:
      type: array
      items:
        $ref: "#/Pet"
Pet:
  type: object
  required: [id, name]
  properties:
    id: { type: string }
    name: { type: string }
    kind:
      $ref: "#/Kind"
Kind:
  type: string
  enum: [cat, dog]
`

const paramsDoc = `
LimitParam:
  name: limit
  in: query
  schema: { type: integer, minimum: 1 }
`

func loadFixture(t *testing.T) (*Loader, *Document) {
	t.Helper()
	dir := filepath.Dir(writeTemp(t, "main.yaml", mainDoc))
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(modelsDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "params.yaml"), []byte(paramsDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLoader()
	doc, err := l.LoadFile(filepath.Join(dir, "main.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	return l, doc
}

func TestLoadAndCrossFileRefs(t *testing.T) {
	_, doc := loadFixture(t)
	if doc.Format != FormatOpenAPI3 {
		t.Fatalf("format = %v", doc.Format)
	}
	if len(doc.Operations) != 2 {
		t.Fatalf("ops = %d", len(doc.Operations))
	}
	getPet := doc.Operations[1]
	r200 := getPet.Responses[0]
	s := unwrap(r200.Body)
	if s.Kind != KindObject {
		t.Fatalf("pet kind = %v", s.Kind)
	}
	id := prop(s, "id")
	if id == nil || !id.Required || unwrap(id.Schema).Kind != KindString {
		t.Fatalf("id property wrong: %+v", id)
	}
	kind := unwrap(prop(s, "kind").Schema)
	if kind.Kind != KindString || len(kind.Enum) != 2 {
		t.Fatalf("kind enum wrong: %+v", kind)
	}
}

func TestOperationSortingStable(t *testing.T) {
	_, doc := loadFixture(t)
	if doc.Operations[0].Path != "/pets" || doc.Operations[0].Method != "GET" {
		t.Fatalf("first op = %s %s", doc.Operations[0].Method, doc.Operations[0].Path)
	}
}

func TestUnresolvableRefHasLocation(t *testing.T) {
	bad := strings.Replace(mainDoc, "./models.yaml#/PetList", "./missing.yaml#/X", 1)
	p := writeTemp(t, "main.yaml", bad)
	l := NewLoader()
	_, err := l.LoadFile(p)
	if err == nil {
		t.Fatal("expected error")
	}
	re, ok := err.(*ResolveError)
	if !ok {
		t.Fatalf("want *ResolveError, got %T", err)
	}
	if re.Location.Line == 0 {
		t.Fatalf("error missing line location: %v", re)
	}
}

func TestBadPointerHasLocation(t *testing.T) {
	bad := strings.Replace(modelsDoc, "#/Pet", "#/Nope", 1)
	dir := filepath.Dir(writeTemp(t, "main.yaml", mainDoc))
	_ = os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(bad), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "params.yaml"), []byte(paramsDoc), 0o644)
	l := NewLoader()
	_, err := l.LoadFile(filepath.Join(dir, "main.yaml"))
	if err == nil {
		t.Fatal("expected unresolvable pointer error")
	}
	if !strings.Contains(err.Error(), "unresolvable reference") {
		t.Fatalf("error = %v", err)
	}
}

func TestCyclicRefDoesNotHang(t *testing.T) {
	main := `
openapi: 3.0.3
info: { title: T, version: "1.0" }
paths:
  /tree:
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
        self:
          $ref: "#/components/schemas/Node"
        children:
          type: array
          items:
            $ref: "#/components/schemas/Node"
        kind:
          $ref: "#/components/schemas/Kind"
    Kind:
      type: string
      enum: [a, b]
`
	p := writeTemp(t, "main.yaml", main)
	l := NewLoader()
	done := make(chan *Document, 1)
	errCh := make(chan error, 1)
	go func() {
		d, err := l.LoadFile(p)
		if err != nil {
			errCh <- err
		} else {
			done <- d
		}
	}()
	select {
	case doc := <-done:
		s := unwrap(doc.Operations[0].Responses[0].Body)
		self := prop(s, "self")
		if self == nil || self.Schema.Kind != KindRef {
			t.Fatalf("cycle back-edge not preserved: %+v", self)
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("cyclic reference resolution hung")
	}
}

func TestCacheReuseSameRef(t *testing.T) {
	l, doc := loadFixture(t)
	petList := unwrap(doc.Operations[0].Responses[0].Body)
	petA := unwrap(petList.Properties[0].Schema.Items)
	petB := unwrap(doc.Operations[1].Responses[0].Body)
	if petA != petB {
		t.Fatal("same reference produced duplicate schema allocations")
	}
	if len(l.schemas) == 0 {
		t.Fatal("schema cache empty")
	}
}

func TestSyntaxErrorHasLocation(t *testing.T) {
	bad := "openapi: 3.0.0\ninfo:\n  title: [unclosed\n"
	p := writeTemp(t, "main.yaml", bad)
	_, err := NewLoader().LoadFile(p)
	if err == nil {
		t.Fatal("expected syntax error")
	}
	re := err.(*ResolveError)
	if re.Location.Line == 0 {
		t.Fatalf("syntax error lacks position: %v", re)
	}
}

func TestUnknownFormatRejected(t *testing.T) {
	p := writeTemp(t, "main.yaml", "hello: world\n")
	_, err := NewLoader().LoadFile(p)
	if err == nil || !strings.Contains(err.Error(), "recognized") {
		t.Fatalf("err = %v", err)
	}
}

func unwrap(s *Schema) *Schema {
	for s != nil && s.Kind == KindRef && s.Base != nil {
		s = s.Base
	}
	return s
}

func prop(s *Schema, name string) *Property {
	for _, p := range s.Properties {
		if p.Name == name {
			return p
		}
	}
	return nil
}
