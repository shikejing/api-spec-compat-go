package loader

import (
	"strings"
	"testing"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

func loadTestDoc(t *testing.T, path string) *spec.Document {
	t.Helper()
	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	return doc
}

func TestLoadOpenAPI3(t *testing.T) {
	doc := loadTestDoc(t, "testdata/petstore.yaml")
	if doc.Version != spec.VersionOpenAPI3 {
		t.Fatalf("version = %v", doc.Version)
	}
	if doc.Title != "Pet Store" {
		t.Fatalf("title = %q", doc.Title)
	}
	pets := doc.Paths["/pets"]
	if pets == nil {
		t.Fatal("missing /pets path")
	}
	get := pets.Operations["get"]
	if get == nil || get.OperationID != "listPets" {
		t.Fatalf("get op = %+v", get)
	}
	if len(get.Parameters) != 1 || get.Parameters[0].Name != "limit" {
		t.Fatalf("params = %+v", get.Parameters)
	}
	resp := get.Responses["200"]
	if resp == nil || spec.Deref(resp.Schema).Type != "array" {
		t.Fatalf("response 200 = %+v", resp)
	}
}

func TestCrossFileRef(t *testing.T) {
	doc := loadTestDoc(t, "testdata/petstore.yaml")
	post := doc.Paths["/pets"].Operations["post"]
	s := spec.Deref(post.Responses["201"].Schema)
	if s.Type != "object" || s.Properties["message"] == nil {
		t.Fatalf("cross-file schema = %+v", s)
	}
	if s.Loc.File == "" || !strings.HasSuffix(s.Loc.File, "common.yaml") {
		t.Fatalf("expected location in common.yaml, got %v", s.Loc)
	}
}

func TestCyclicRef(t *testing.T) {
	doc := loadTestDoc(t, "testdata/petstore.yaml")
	pet := doc.Components["Pet"]
	friends := spec.Deref(pet.Properties["friends"])
	item := friends.Items
	if !item.Cyclic && spec.Deref(item) != pet {
		t.Fatalf("expected cyclic or aliased ref back to Pet, got %+v", item)
	}
}

func TestRefMemoization(t *testing.T) {
	doc := loadTestDoc(t, "testdata/petstore.yaml")
	// The same $ref target must resolve to the same shared schema.
	a := spec.Deref(doc.Paths["/pets"].Operations["post"].RequestBody)
	b := doc.Components["Pet"]
	if a != b {
		t.Fatalf("expected shared pointer for memoized ref, got %p vs %p", a, b)
	}
}

func TestLoadErrorLocation(t *testing.T) {
	_, err := Load("testdata/broken.yaml")
	if err == nil {
		t.Fatal("expected error")
	}
	le, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if le.Loc.File == "" || le.Loc.Line == 0 {
		t.Fatalf("error lacks location: %v", le)
	}
}

func TestDeterministicRepeatedLoad(t *testing.T) {
	d1 := loadTestDoc(t, "testdata/petstore.yaml")
	d2 := loadTestDoc(t, "testdata/petstore.yaml")
	if d1.Title != d2.Title || len(d1.Paths) != len(d2.Paths) {
		t.Fatal("repeated loads diverge")
	}
}

func TestLoadSwagger2(t *testing.T) {
	doc := loadTestDoc(t, "testdata/swagger2.yaml")
	if doc.Version != spec.VersionSwagger2 {
		t.Fatalf("version = %v", doc.Version)
	}
	item := doc.Paths["/users/{id}"]
	if item == nil {
		t.Fatal("missing path")
	}
	get := item.Operations["get"]
	// path-level param merged into operation.
	var idParam, verboseParam *spec.Parameter
	for _, p := range get.Parameters {
		switch p.Name {
		case "id":
			idParam = p
		case "verbose":
			verboseParam = p
		}
	}
	if idParam == nil || !idParam.Required || idParam.Schema.Type != "integer" {
		t.Errorf("id param = %+v", idParam)
	}
	if verboseParam == nil || verboseParam.Schema.Type != "boolean" {
		t.Errorf("verbose param = %+v", verboseParam)
	}
	if spec.Deref(get.Responses["200"].Schema).Properties["name"] == nil {
		t.Error("response schema not resolved")
	}
	put := item.Operations["put"]
	if put.RequestBody == nil {
		t.Fatal("body parameter not lifted into RequestBody")
	}
	if spec.Deref(put.RequestBody).Ref != "User" {
		t.Errorf("request body = %+v", put.RequestBody)
	}
	if doc.Components["User"] == nil {
		t.Error("definitions not normalized into Components")
	}
}
