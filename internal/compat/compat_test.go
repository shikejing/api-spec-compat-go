package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

func writeSpec(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const header = `openapi: 3.0.0
info: { title: T, version: "1" }
paths:
  /things:
`

// buildSpec embeds a get/post operation using the supplied response/request schema.
func specWith(getSchemaLine, postSchema string) string {
	return header + `    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
` + indent(getSchemaLine, 16) + `
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
` + indent(postSchema, 14) + `
      responses:
        '201':
          description: created
`
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if ln == "" {
			out = append(out, "")
		} else {
			out = append(out, pad+ln)
		}
	}
	return strings.Join(out, "\n")
}

func compareSchemas(t *testing.T, oldBody, newBody string) *Report {
	t.Helper()
	dir := t.TempDir()
	op := writeSpec(t, dir, "old.yaml", specWith(oldBody, oldBody))
	np := writeSpec(t, dir, "new.yaml", specWith(newBody, newBody))
	l := spec.NewLoader()
	r := NewComparator().CompareFiles(l, op, np)
	if r.Status != "compared" {
		t.Fatalf("compare failed: %s", r.Error)
	}
	return r
}

func changeAt(r *Report, pathSuffix string) *Change {
	for i := range r.Changes {
		if strings.HasSuffix(r.Changes[i].Path, pathSuffix) {
			return &r.Changes[i]
		}
	}
	return nil
}

func changesAt(r *Report, pathSuffix string) []Change {
	var out []Change
	for _, c := range r.Changes {
		if strings.HasSuffix(c.Path, pathSuffix) {
			out = append(out, c)
		}
	}
	return out
}

func TestRequiredPropertyRequestBreaks(t *testing.T) {
	r := compareSchemas(t,
		"type: object\nproperties:\n  a: {type: string}",
		"type: object\nrequired: [a]\nproperties:\n  a: {type: string}")
	c := changeAt(r, "requestBody/content/properties/a/required")
	if c == nil || c.Direction != DirectionRequest || c.Level != LevelBreaking {
		t.Fatalf("expected breaking required request change, got %+v", c)
	}
}

func TestRequiredPropertyResponseBreaksOnRemoval(t *testing.T) {
	r := compareSchemas(t,
		"type: object\nrequired: [a]\nproperties:\n  a: {type: string}",
		"type: object\nproperties:\n  a: {type: string}")
	// response: required -> optional = breaking
	var found bool
	for _, c := range r.Changes {
		if c.Direction == DirectionResponse && c.Kind == KindRequiredRemoved && c.Level == LevelBreaking {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected response required removal breaking; changes=%+v", r.Changes)
	}
}

func TestOptionalResponsePropertyAddedCompat(t *testing.T) {
	r := compareSchemas(t,
		"type: object\nproperties:\n  a: {type: string}",
		"type: object\nproperties:\n  a: {type: string}\n  b: {type: string}")
	cs := changesAt(r, "/properties/b")
	var respCompat bool
	for _, c := range cs {
		if c.Direction == DirectionResponse && c.Level == LevelCompat {
			respCompat = true
		}
	}
	if !respCompat {
		t.Fatalf("expected compatible response add; %+v", r.Changes)
	}
}

func TestResponsePropertyRemovedBreaks(t *testing.T) {
	r := compareSchemas(t,
		"type: object\nproperties:\n  a: {type: string}\n  b: {type: string}",
		"type: object\nproperties:\n  a: {type: string}")
	var found bool
	for _, c := range r.Changes {
		if c.Direction == DirectionResponse && c.Kind == KindRemoved && strings.Contains(c.Description, "b") {
			if c.Level != LevelBreaking {
				t.Fatalf("level = %s", c.Level)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected response property removal breaking: %+v", r.Changes)
	}
}

func TestEnumNarrowRequestBreaksResponseWarns(t *testing.T) {
	r := compareSchemas(t,
		"type: string\nenum: [a, b, c]",
		"type: string\nenum: [a, b]")
	var reqBreak, respWarn bool
	for _, c := range r.Changes {
		if c.Kind != KindEnumNarrowed {
			continue
		}
		if c.Direction == DirectionRequest && c.Level == LevelBreaking {
			reqBreak = true
		}
		if c.Direction == DirectionResponse && c.Level == LevelWarning {
			respWarn = true
		}
		if c.Reason == "" {
			t.Fatal("enum change must explain the conservative reason")
		}
	}
	if !reqBreak || !respWarn {
		t.Fatalf("reqBreak=%v respWarn=%v changes=%+v", reqBreak, respWarn, r.Changes)
	}
}

func TestEnumWidenResponseConservative(t *testing.T) {
	r := compareSchemas(t,
		"type: string\nenum: [a]",
		"type: string\nenum: [a, b]")
	for _, c := range r.Changes {
		if c.Direction == DirectionResponse && c.Kind == KindEnumWidened && c.Level == LevelCompat {
			t.Fatalf("response enum widening must not be auto-compatible: %+v", c)
		}
	}
}

func TestUnionReplacementConservative(t *testing.T) {
	r := compareSchemas(t,
		"type: string",
		"oneOf:\n  - {type: string}\n  - {type: integer}")
	var found bool
	for _, c := range r.Changes {
		if c.Kind == KindTypeChanged && c.Level == LevelWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("union replacement must be potentially-breaking: %+v", r.Changes)
	}
}

func TestFundamentalTypeChangeBreaks(t *testing.T) {
	r := compareSchemas(t, "type: string", "type: integer")
	var n int
	for _, c := range r.Changes {
		if c.Kind == KindTypeChanged && c.Level == LevelBreaking {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("expected breaking type changes: %+v", r.Changes)
	}
}

func TestRequestConstraintTightenBreaks(t *testing.T) {
	r := compareSchemas(t,
		"type: string\nminLength: 1",
		"type: string\nminLength: 5")
	c := changeAt(r, "requestBody/content/minLength")
	if c == nil || c.Level != LevelBreaking {
		t.Fatalf("got %+v", c)
	}
}

func TestOperationRemovalBreaksAndAdditionCompat(t *testing.T) {
	dir := t.TempDir()
	old := writeSpec(t, dir, "old.yaml", header+`    get:
      responses:
        '200': {description: ok}
    delete:
      responses:
        '204': {description: ok}
`)
	neu := writeSpec(t, dir, "new.yaml", header+`    get:
      responses:
        '200': {description: ok}
    post:
      responses:
        '201': {description: created}
`)
	r := NewComparator().CompareFiles(spec.NewLoader(), old, neu)
	var removed, added bool
	for _, c := range r.Changes {
		if c.Kind == KindRemoved && c.Level == LevelBreaking {
			removed = true
		}
		if c.Kind == KindAdded && c.Level == LevelCompat {
			added = true
		}
	}
	if !removed || !added {
		t.Fatalf("removed=%v added=%v %+v", removed, added, r.Changes)
	}
}

func TestCyclicSchemaDiffTerminates(t *testing.T) {
	doc := func(refTarget string) string {
		return `openapi: 3.0.0
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
        leaf: { type: ` + refTarget + ` }
`
	}
	dir := t.TempDir()
	op := writeSpec(t, dir, "o.yaml", doc("string"))
	np := writeSpec(t, dir, "n.yaml", doc("integer"))
	r := NewComparator().CompareFiles(spec.NewLoader(), op, np)
	if r.Status != "compared" {
		t.Fatalf("status: %s %s", r.Status, r.Error)
	}
	c := changeAt(r, "properties/leaf/format")
	_ = c
}

func TestDeterministicDiff(t *testing.T) {
	dir := t.TempDir()
	old := writeSpec(t, dir, "old.yaml", specWith(
		"type: object\nproperties:\n  a: {type: string}",
		"type: object\nproperties:\n  a: {type: string}"))
	neu := writeSpec(t, dir, "new.yaml", specWith(
		"type: object\nrequired: [a]\nproperties:\n  a: {type: string}\n  b: {type: integer}",
		"type: object\nrequired: [a]\nproperties:\n  a: {type: string}\n  b: {type: integer}"))
	l := spec.NewLoader()
	c := NewComparator()
	r1 := c.CompareFiles(l, old, neu)
	r2 := c.CompareFiles(l, old, neu)
	if len(r1.Changes) != len(r2.Changes) {
		t.Fatal("change count mismatch")
	}
	for i := range r1.Changes {
		if r1.Changes[i].Path != r2.Changes[i].Path ||
			r1.Changes[i].Level != r2.Changes[i].Level ||
			r1.Changes[i].Kind != r2.Changes[i].Kind {
			t.Fatalf("nondeterministic change %d", i)
		}
	}
}

func TestNullableChangeConservative(t *testing.T) {
	r := compareSchemas(t,
		"type: string",
		"type: string\nnullable: true")
	var n int
	for _, c := range r.Changes {
		if c.Kind == KindTypeWidened && c.Level == LevelWarning {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("nullable widening must be potentially-breaking, not auto-compatible: %+v", r.Changes)
	}
}

func TestAllOfChangeConservative(t *testing.T) {
	r := compareSchemas(t,
		"type: string",
		"allOf:\n  - {type: string}\n  - {minLength: 2}")
	var safe bool
	for _, c := range r.Changes {
		// Must never auto-declare an unknown/composed change fully compatible.
		if c.Level == LevelCompat && c.Kind == KindTypeChanged {
			safe = true
		}
	}
	if safe {
		t.Fatalf("composed change auto-compatible: %+v", r.Changes)
	}
}

func TestArrayItemsNarrowedConservative(t *testing.T) {
	r := compareSchemas(t,
		"type: array\nitems: {}",
		"type: array\nitems: {type: string}")
	var found bool
	for _, c := range r.Changes {
		if strings.HasSuffix(c.Path, "/items") {
			if c.Level != LevelWarning {
				t.Fatalf("adding items constraint should be potentially-breaking, got %s", c.Level)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected conservative array-items narrowing: %+v", r.Changes)
	}
}

func TestLoadErrorReported(t *testing.T) {
	r := NewComparator().CompareFiles(spec.NewLoader(), "/nonexistent/a.yaml", "/nonexistent/b.yaml")
	if r.Status != "load-error" || r.Error == "" {
		t.Fatalf("expected load error, got %+v", r)
	}
}
