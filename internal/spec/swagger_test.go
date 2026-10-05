package spec

import "testing"

const swagger2 = `{
  "swagger": "2.0",
  "info": {"title": "Legacy", "version": "1.0"},
  "paths": {
    "/things": {
      "post": {
        "parameters": [
          {"name": "body", "in": "body", "required": true, "schema": {"$ref": "#/definitions/Thing"}}
        ],
        "responses": {"201": {"description": "c", "schema": {"$ref": "#/definitions/Thing"}}}
      }
    }
  },
  "definitions": {
    "Thing": {"type": "object", "required": ["id"], "properties": {"id": {"type": "string"}}}
  }
}`

func TestSwagger2Load(t *testing.T) {
	p := writeTemp(t, "api.json", swagger2)
	doc, err := NewLoader().LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != FormatSwagger2 {
		t.Fatalf("format=%v", doc.Format)
	}
	op := doc.Operations[0]
	if op.RequestBody == nil || !op.RequestBody.Required {
		t.Fatal("body parameter not normalized to required request body")
	}
	s := unwrap(op.RequestBody.Content["application/json"])
	if s.Kind != KindObject || !s.Properties[0].Required {
		t.Fatalf("thing schema wrong: %+v", s)
	}
	if unwrap(op.Responses[0].Body).Kind != KindObject {
		t.Fatal("response body not normalized")
	}
}
