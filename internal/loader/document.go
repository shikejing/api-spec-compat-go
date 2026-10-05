package loader

import (
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
	"gopkg.in/yaml.v3"
)

// docBuilder normalizes one parsed document into a spec.Document.
type docBuilder struct {
	r    *resolver
	file string
}

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

func (b *docBuilder) build(root *yaml.Node) (*spec.Document, error) {
	if root.Kind != yaml.MappingNode {
		return nil, errAt(b.file, root, "document root must be a mapping")
	}
	doc := &spec.Document{SourceFile: b.file}
	switch {
	case mappingValue(root, "openapi") != nil:
		v := scalarString(mappingValue(root, "openapi"))
		if !strings.HasPrefix(v, "3.") {
			return nil, errAt(b.file, mappingValue(root, "openapi"), "unsupported OpenAPI version %q", v)
		}
		doc.Version = spec.VersionOpenAPI3
	case mappingValue(root, "swagger") != nil:
		v := scalarString(mappingValue(root, "swagger"))
		if v != "2.0" {
			return nil, errAt(b.file, mappingValue(root, "swagger"), "unsupported Swagger version %q", v)
		}
		doc.Version = spec.VersionSwagger2
	default:
		return nil, errAt(b.file, root, "missing \"openapi\" or \"swagger\" version field")
	}

	if info := mappingValue(root, "info"); info != nil {
		doc.Title = scalarString(mappingValue(info, "title"))
		doc.DocVersion = scalarString(mappingValue(info, "version"))
	}

	var err error
	if doc.Components, err = b.buildComponents(root); err != nil {
		return nil, err
	}
	if doc.Paths, err = b.buildPaths(root); err != nil {
		return nil, err
	}
	return doc, nil
}

func (b *docBuilder) buildComponents(root *yaml.Node) (map[string]*spec.Schema, error) {
	var container *yaml.Node
	pointerPrefix := ""
	if c := mappingValue(root, "components"); c != nil {
		container = mappingValue(c, "schemas")
		pointerPrefix = "#/components/schemas/"
	} else {
		container = mappingValue(root, "definitions")
		pointerPrefix = "#/definitions/"
	}
	if container == nil {
		return nil, nil
	}
	if container.Kind != yaml.MappingNode {
		return nil, errAt(b.file, container, "schemas/definitions must be a mapping")
	}
	out := map[string]*spec.Schema{}
	for i := 0; i+1 < len(container.Content); i += 2 {
		name := container.Content[i].Value
		// Resolve through the memoized ref path so that a component and
		// every $ref pointing at it share one schema instance.
		s, err := b.r.resolveSchemaRef(pointerPrefix+name, b.file, container.Content[i])
		if err != nil {
			return nil, err
		}
		out[name] = spec.Deref(s)
	}
	return out, nil
}

func (b *docBuilder) buildPaths(root *yaml.Node) (map[string]*spec.PathItem, error) {
	pathsNode := mappingValue(root, "paths")
	if pathsNode == nil {
		return nil, nil
	}
	if pathsNode.Kind != yaml.MappingNode {
		return nil, errAt(b.file, pathsNode, "paths must be a mapping")
	}
	out := map[string]*spec.PathItem{}
	for i := 0; i+1 < len(pathsNode.Content); i += 2 {
		pathKey := pathsNode.Content[i].Value
		itemNode := pathsNode.Content[i+1]
		if itemNode.Kind != yaml.MappingNode {
			return nil, errAt(b.file, itemNode, "path item %q must be a mapping", pathKey)
		}
		item, err := b.buildPathItem(pathKey, itemNode)
		if err != nil {
			return nil, err
		}
		out[pathKey] = item
	}
	return out, nil
}

func (b *docBuilder) buildPathItem(path string, node *yaml.Node) (*spec.PathItem, error) {
	item := &spec.PathItem{
		Path:       path,
		Operations: map[string]*spec.Operation{},
		Loc:        spec.Location{File: b.file, Line: node.Line, Col: node.Column},
	}
	// Path-level parameters apply to every operation.
	shared, err := b.buildParameters(mappingValue(node, "parameters"))
	if err != nil {
		return nil, err
	}
	for _, key := range mappingKeys(node) {
		if !httpMethods[key] {
			continue
		}
		opNode := mappingValue(node, key)
		op, err := b.buildOperation(key, path, opNode, shared)
		if err != nil {
			return nil, err
		}
		item.Operations[key] = op
	}
	return item, nil
}

func (b *docBuilder) buildOperation(method, path string, node *yaml.Node, shared []*spec.Parameter) (*spec.Operation, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errAt(b.file, node, "operation %s %s must be a mapping", method, path)
	}
	op := &spec.Operation{
		Method:    method,
		Path:      path,
		Responses: map[string]*spec.Response{},
		Loc:       spec.Location{File: b.file, Line: node.Line, Col: node.Column},
	}
	op.OperationID = scalarString(mappingValue(node, "operationId"))

	own, err := b.buildParameters(mappingValue(node, "parameters"))
	if err != nil {
		return nil, err
	}
	op.Parameters = mergeParameters(shared, own)

	// Request body: OpenAPI 3 requestBody, or Swagger 2 body parameter.
	if rb := mappingValue(node, "requestBody"); rb != nil {
		content := mappingValue(rb, "content")
		schemaNode := firstMediaSchema(content)
		if schemaNode != nil {
			s, err := b.r.buildSchema(schemaNode, b.file)
			if err != nil {
				return nil, err
			}
			op.RequestBody = s
		}
	}
	for _, p := range op.Parameters {
		if p.In == "body" && p.Schema != nil {
			op.RequestBody = p.Schema
		}
	}
	// Drop body params from the parameter list.
	kept := op.Parameters[:0]
	for _, p := range op.Parameters {
		if p.In != "body" {
			kept = append(kept, p)
		}
	}
	op.Parameters = kept

	responses := mappingValue(node, "responses")
	if responses == nil {
		return nil, errAt(b.file, node, "operation %s %s has no responses", strings.ToUpper(method), path)
	}
	if responses.Kind != yaml.MappingNode {
		return nil, errAt(b.file, responses, "responses must be a mapping")
	}
	for i := 0; i+1 < len(responses.Content); i += 2 {
		status := responses.Content[i].Value
		rnode := responses.Content[i+1]
		resp, err := b.buildResponse(status, rnode)
		if err != nil {
			return nil, err
		}
		op.Responses[status] = resp
	}
	return op, nil
}

func (b *docBuilder) buildResponse(status string, node *yaml.Node) (*spec.Response, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errAt(b.file, node, "response %q must be a mapping", status)
	}
	resp := &spec.Response{
		Status:      status,
		Description: scalarString(mappingValue(node, "description")),
		Loc:         spec.Location{File: b.file, Line: node.Line, Col: node.Column},
	}
	// OpenAPI 3: content.<media>.schema; Swagger 2: schema directly.
	var schemaNode *yaml.Node
	if content := mappingValue(node, "content"); content != nil {
		schemaNode = firstMediaSchema(content)
	} else {
		schemaNode = mappingValue(node, "schema")
	}
	if schemaNode != nil {
		s, err := b.r.buildSchema(schemaNode, b.file)
		if err != nil {
			return nil, err
		}
		resp.Schema = s
	}
	return resp, nil
}

// firstMediaSchema picks the schema of the first media type, preferring
// JSON media types, in deterministic order.
func firstMediaSchema(content *yaml.Node) *yaml.Node {
	if content == nil || content.Kind != yaml.MappingNode {
		return nil
	}
	var fallback *yaml.Node
	for i := 0; i+1 < len(content.Content); i += 2 {
		media := content.Content[i].Value
		schema := mappingValue(content.Content[i+1], "schema")
		if schema == nil {
			continue
		}
		if strings.Contains(media, "json") {
			return schema
		}
		if fallback == nil {
			fallback = schema
		}
	}
	return fallback
}

// mergeParameters overlays operation-level parameters on path-level
// ones, keyed by (name, in).
func mergeParameters(shared, own []*spec.Parameter) []*spec.Parameter {
	type key struct{ name, in string }
	ownKeys := map[key]bool{}
	for _, p := range own {
		ownKeys[key{p.Name, p.In}] = true
	}
	out := append([]*spec.Parameter{}, own...)
	for _, p := range shared {
		if !ownKeys[key{p.Name, p.In}] {
			out = append(out, p)
		}
	}
	return out
}
