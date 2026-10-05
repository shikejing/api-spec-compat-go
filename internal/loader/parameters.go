package loader

import (
	"github.com/shikejing/api-spec-compat-go/internal/spec"
	"gopkg.in/yaml.v3"
)

// buildParameters normalizes a parameters sequence. Swagger 2 body
// parameters keep their schema in Parameter.Schema with In=="body";
// the caller lifts them into Operation.RequestBody.
func (b *docBuilder) buildParameters(node *yaml.Node) ([]*spec.Parameter, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, errAt(b.file, node, "parameters must be a sequence")
	}
	var out []*spec.Parameter
	for _, item := range node.Content {
		p, err := b.buildParameter(item)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (b *docBuilder) buildParameter(node *yaml.Node) (*spec.Parameter, error) {
	// Parameters may themselves be references.
	if node.Kind == yaml.MappingNode {
		if refNode := mappingValue(node, "$ref"); refNode != nil {
			t, err := b.r.resolveRef(refNode.Value, b.file, refNode)
			if err != nil {
				return nil, err
			}
			return b.buildParameter(t.node)
		}
	}
	if node.Kind != yaml.MappingNode {
		return nil, errAt(b.file, node, "parameter must be a mapping")
	}
	p := &spec.Parameter{
		Name:     scalarString(mappingValue(node, "name")),
		In:       scalarString(mappingValue(node, "in")),
		Required: scalarBool(mappingValue(node, "required")),
		Loc:      spec.Location{File: b.file, Line: node.Line, Col: node.Column},
	}
	if p.In == "path" {
		p.Required = true // mandated by the specification
	}
	if sn := mappingValue(node, "schema"); sn != nil {
		s, err := b.r.buildSchema(sn, b.file)
		if err != nil {
			return nil, err
		}
		p.Schema = s
	} else if t := mappingValue(node, "type"); t != nil {
		// Swagger 2 non-body parameters carry type info inline.
		s := &spec.Schema{
			Type:   t.Value,
			Format: scalarString(mappingValue(node, "format")),
			Loc:    spec.Location{File: b.file, Line: node.Line, Col: node.Column},
		}
		if items := mappingValue(node, "items"); items != nil {
			sub, err := b.r.buildSchema(items, b.file)
			if err != nil {
				return nil, err
			}
			s.Items = sub
		}
		if enum := mappingValue(node, "enum"); enum != nil && enum.Kind == yaml.SequenceNode {
			for _, item := range enum.Content {
				var v any
				if err := item.Decode(&v); err != nil {
					return nil, errAt(b.file, item, "decoding enum value: %v", err)
				}
				s.Enum = append(s.Enum, v)
			}
		}
		p.Schema = s
	}
	return p, nil
}
