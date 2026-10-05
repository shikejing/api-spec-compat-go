package loader

import (
	"fmt"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
	"gopkg.in/yaml.v3"
)

// buildSchema converts a YAML schema node into a normalized
// spec.Schema, resolving $ref with memoization and cycle detection.
// allOf entries that describe objects are merged into the parent.
func (r *resolver) buildSchema(node *yaml.Node, file string) (*spec.Schema, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, errAt(file, node, "schema must be a mapping, got %s", nodeKind(node))
	}
	if refNode := mappingValue(node, "$ref"); refNode != nil {
		return r.resolveSchemaRef(refNode.Value, file, refNode)
	}
	s := &spec.Schema{Loc: spec.Location{File: file, Line: node.Line, Col: node.Column}}

	// type may be a scalar or (OpenAPI 3.1) a sequence.
	if t := mappingValue(node, "type"); t != nil {
		switch t.Kind {
		case yaml.SequenceNode:
			for _, item := range t.Content {
				if item.Value == "null" {
					s.Nullable = true
					continue
				}
				if s.Type == "" {
					s.Type = item.Value
				}
			}
		default:
			s.Type = t.Value
		}
	}
	s.Format = scalarString(mappingValue(node, "format"))
	if mappingValue(node, "nullable") != nil || mappingValue(node, "x-nullable") != nil {
		s.Nullable = scalarBool(mappingValue(node, "nullable")) || scalarBool(mappingValue(node, "x-nullable"))
	}

	if req := mappingValue(node, "required"); req != nil && req.Kind == yaml.SequenceNode {
		for _, item := range req.Content {
			s.Required = append(s.Required, item.Value)
		}
	}
	if enum := mappingValue(node, "enum"); enum != nil && enum.Kind == yaml.SequenceNode {
		s.Enum = make([]any, 0, len(enum.Content))
		for _, item := range enum.Content {
			var v any
			if err := item.Decode(&v); err != nil {
				return nil, errAt(file, item, "decoding enum value: %v", err)
			}
			s.Enum = append(s.Enum, v)
		}
	}

	if props := mappingValue(node, "properties"); props != nil {
		if props.Kind != yaml.MappingNode {
			return nil, errAt(file, props, "properties must be a mapping")
		}
		s.Properties = map[string]*spec.Schema{}
		for i := 0; i+1 < len(props.Content); i += 2 {
			sub, err := r.buildSchema(props.Content[i+1], file)
			if err != nil {
				return nil, err
			}
			s.Properties[props.Content[i].Value] = sub
		}
	}
	if items := mappingValue(node, "items"); items != nil {
		sub, err := r.buildSchema(items, file)
		if err != nil {
			return nil, err
		}
		s.Items = sub
	}
	if ap := mappingValue(node, "additionalProperties"); ap != nil {
		s.AdditionalPropertiesSet = true
		if ap.Kind == yaml.MappingNode {
			sub, err := r.buildSchema(ap, file)
			if err != nil {
				return nil, err
			}
			s.AdditionalProperties = sub
		}
		// boolean false leaves AdditionalProperties nil with Set=true,
		// meaning "no additional properties allowed".
	}
	for _, key := range []string{"allOf", "oneOf", "anyOf"} {
		list := mappingValue(node, key)
		if list == nil {
			continue
		}
		if list.Kind != yaml.SequenceNode {
			return nil, errAt(file, list, "%s must be a sequence", key)
		}
		for _, item := range list.Content {
			sub, err := r.buildSchema(item, file)
			if err != nil {
				return nil, err
			}
			switch key {
			case "allOf":
				s.AllOf = append(s.AllOf, sub)
			case "oneOf":
				s.OneOf = append(s.OneOf, sub)
			case "anyOf":
				s.AnyOf = append(s.AnyOf, sub)
			}
		}
	}
	mergeAllOf(s)
	return s, nil
}

// resolveSchemaRef resolves a $ref to a schema, memoized per target so
// repeated references share one resolution. A reference to a target
// currently being built yields a cyclic placeholder instead of
// recursing forever.
func (r *resolver) resolveSchemaRef(ref, fromFile string, refNode *yaml.Node) (*spec.Schema, error) {
	t, err := r.resolveRef(ref, fromFile, refNode)
	if err != nil {
		return nil, err
	}
	if s, ok := r.schemaMemo[t.key]; ok {
		return &spec.Schema{Ref: t.name, Loc: spec.Location{File: fromFile, Line: refNode.Line, Col: refNode.Column}, Alias: s}, nil
	}
	if r.inProgress[t.key] {
		return &spec.Schema{Ref: t.name, Cyclic: true, Loc: spec.Location{File: fromFile, Line: refNode.Line, Col: refNode.Column}}, nil
	}
	r.inProgress[t.key] = true
	s, err := r.buildSchema(t.node, t.file)
	delete(r.inProgress, t.key)
	if err != nil {
		return nil, err
	}
	s.Ref = t.name
	r.schemaMemo[t.key] = s
	return &spec.Schema{Ref: t.name, Loc: spec.Location{File: fromFile, Line: refNode.Line, Col: refNode.Column}, Alias: s}, nil
}

// mergeAllOf folds object-like allOf subschemas into the parent:
// properties and required are unioned. Non-object allOf entries are
// kept in AllOf so downstream passes can judge them conservatively.
func mergeAllOf(s *spec.Schema) {
	if len(s.AllOf) == 0 {
		return
	}
	kept := s.AllOf[:0]
	reqSeen := map[string]bool{}
	for _, r := range s.Required {
		reqSeen[r] = true
	}
	for _, sub := range s.AllOf {
		sub = spec.Deref(sub)
		objectLike := sub.Type == "" || sub.Type == "object" || len(sub.Properties) > 0
		if !objectLike || len(sub.OneOf) > 0 || len(sub.AnyOf) > 0 || len(sub.AllOf) > 0 {
			kept = append(kept, sub)
			continue
		}
		if len(sub.Properties) > 0 && s.Properties == nil {
			s.Properties = map[string]*spec.Schema{}
		}
		for name, ps := range sub.Properties {
			if _, exists := s.Properties[name]; !exists {
				s.Properties[name] = ps
			}
		}
		for _, r := range sub.Required {
			if !reqSeen[r] {
				reqSeen[r] = true
				s.Required = append(s.Required, r)
			}
		}
		if sub.AdditionalPropertiesSet && !s.AdditionalPropertiesSet {
			s.AdditionalPropertiesSet = true
			s.AdditionalProperties = sub.AdditionalProperties
		}
	}
	s.AllOf = kept
}

func nodeKind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.ScalarNode:
		return fmt.Sprintf("scalar %q", n.Value)
	}
	return "unknown"
}
