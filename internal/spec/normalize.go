package spec

import (
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// buildSchema converts a raw schema node into the normalized Schema model.
// It resolves $ref chains, detects cycles, and caches by reference/identity so
// that repeated uses of the same definition pay the construction cost once.
func (l *Loader) buildSchema(n *yaml.Node, file string, seen map[string]bool) (*Schema, error) {
	n = deref(n)
	if n == nil {
		return &Schema{Kind: KindAny, Location: &Location{File: file}}, nil
	}

	// $ref handling: the target definition is cached by canonical key, while a
	// lightweight wrapper carrying sibling annotations is produced per usage.
	if refNode := mapField(n, "$ref"); refNode != nil {
		ref := scalarString(refNode)
		key, err := l.canonicalRef(ref, file)
		if err != nil {
			return nil, err
		}
		if seen[key] {
			// Back-edge of a reference cycle: terminal placeholder.
			return &Schema{Kind: KindRef, Ref: ref, RefLocation: nodeLoc(refNode, file), Location: nodeLoc(n, file)}, nil
		}
		seen[key] = true
		var base *Schema
		if cached, ok := l.lookupSchema(key); ok {
			base = cached
		} else {
			target, targetFile, err := l.resolveRef(ref, file, refNode)
			if err != nil {
				return nil, err
			}
			base, err = l.buildSchema(target, targetFile, seen)
			if err != nil {
				return nil, err
			}
			l.storeSchema(key, base)
		}
		delete(seen, key)
		w := &Schema{Kind: KindRef, Ref: ref, Base: base, RefLocation: nodeLoc(refNode, file), Location: nodeLoc(n, file)}
		fillSiblings(w, n)
		return w, nil
	}

	// Non-ref nodes are cached by source identity (anchors resolve to one node).
	k := nodeKey{file, n.Line, n.Column}
	if s, ok := l.lookupInline(k); ok {
		return s, nil
	}

	s := &Schema{Kind: KindAny, Location: nodeLoc(n, file)}
	l.storeInline(k, s)
	if err := l.fillSchema(s, n, file, seen); err != nil {
		return nil, err
	}
	return s, nil
}

func (l *Loader) lookupSchema(key string) (*Schema, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.schemas[key]
	return s, ok
}

func (l *Loader) storeSchema(key string, s *Schema) {
	l.mu.Lock()
	l.schemas[key] = s
	l.mu.Unlock()
}

func (l *Loader) lookupInline(k nodeKey) (*Schema, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.inline[k]
	return s, ok
}

func (l *Loader) storeInline(k nodeKey, s *Schema) {
	l.mu.Lock()
	l.inline[k] = s
	l.mu.Unlock()
}

func fillSiblings(s *Schema, n *yaml.Node) {
	if v := mapField(n, "nullable"); v != nil {
		if b, ok := scalarValue(v).(bool); ok {
			s.Nullable = b
		}
	}
	if v := mapField(n, "deprecated"); v != nil {
		if b, ok := scalarValue(v).(bool); ok {
			s.Deprecated = b
		}
	}
	if v := mapField(n, "description"); v != nil {
		_ = scalarString(v)
	}
}

func (l *Loader) fillSchema(s *Schema, n *yaml.Node, file string, seen map[string]bool) error {
	// Compositions take structural precedence.
	if v := mapField(n, "allOf"); v != nil {
		s.Kind = KindAllOf
		for _, c := range seqNodes(v) {
			cs, err := l.buildSchema(c, file, seen)
			if err != nil {
				return err
			}
			s.Sub = append(s.Sub, cs)
		}
	}
	if v := mapField(n, "oneOf"); v != nil {
		s.Kind = KindOneOf
		for _, c := range seqNodes(v) {
			cs, err := l.buildSchema(c, file, seen)
			if err != nil {
				return err
			}
			s.Sub = append(s.Sub, cs)
		}
	}
	if v := mapField(n, "anyOf"); v != nil {
		s.Kind = KindAnyOf
		for _, c := range seqNodes(v) {
			cs, err := l.buildSchema(c, file, seen)
			if err != nil {
				return err
			}
			s.Sub = append(s.Sub, cs)
		}
	}
	if v := mapField(n, "not"); v != nil {
		s.Kind = KindNot
		cs, err := l.buildSchema(v, file, seen)
		if err != nil {
			return err
		}
		s.Not = cs
	}
	if s.Kind != KindAny {
		fillConstraints(s, n)
		fillSiblings(s, n)
		// allOf/oneOf/anyOf may still declare an enum (rare); keep it.
		if v := mapField(n, "enum"); v != nil {
			s.Enum = enumValues(v)
		}
		return nil
	}

	// Type inference.
	var typeVal string
	if v := mapField(n, "type"); v != nil {
		typeVal = scalarString(v)
	}
	if v := mapField(n, "enum"); v != nil {
		s.Enum = enumValues(v)
	}
	switch typeVal {
	case "integer":
		s.Kind = KindInteger
	case "number":
		s.Kind = KindNumber
	case "string":
		s.Kind = KindString
	case "boolean":
		s.Kind = KindBoolean
	case "array":
		s.Kind = KindArray
	case "object":
		s.Kind = KindObject
	case "null":
		s.Kind = KindNull
	case "":
		switch {
		case mapField(n, "properties") != nil || mapField(n, "additionalProperties") != nil:
			s.Kind = KindObject
		case mapField(n, "items") != nil:
			s.Kind = KindArray
		case len(s.Enum) > 0:
			s.Kind = KindEnum
		default:
			s.Kind = KindAny
		}
	default:
		if strings.HasPrefix(typeVal, "[") {
			s.Kind = KindAnyOf
		} else {
			s.Kind = KindInvalid
		}
	}

	if v := mapField(n, "format"); v != nil {
		s.Format = scalarString(v)
	}
	fillConstraints(s, n)
	fillSiblings(s, n)

	// OpenAPI 3.1: type may be a sequence (e.g. [string, null]).
	if v := mapField(n, "type"); v != nil && v.Kind == yaml.SequenceNode {
		var subs []*Schema
		var nullable bool
		for _, t := range seqNodes(v) {
			switch scalarString(t) {
			case "null":
				nullable = true
			default:
				subs = appendSubType(subs, scalarString(t), n, file)
			}
		}
		s.Nullable = s.Nullable || nullable
		if len(subs) > 1 || (len(subs) == 1 && nullable) {
			s.Kind = KindAnyOf
			s.Sub = subs
		} else if len(subs) == 1 {
			// single non-null type already set above; keep constraints.
		}
	}
	// 3.1 "type: null" companion or enum-only handling already covered.

	switch s.Kind {
	case KindArray:
		if v := mapField(n, "items"); v != nil {
			is, err := l.buildSchema(v, file, seen)
			if err != nil {
				return err
			}
			s.Items = is
		}
	case KindObject:
		if err := l.fillObject(s, n, file, seen); err != nil {
			return err
		}
	}

	// enum on a primitive: record for compatibility, kind stays primitive.
	return nil
}

func appendSubType(subs []*Schema, t string, parent *yaml.Node, file string) []*Schema {
	k := KindString
	switch t {
	case "integer":
		k = KindInteger
	case "number":
		k = KindNumber
	case "boolean":
		k = KindBoolean
	case "array":
		k = KindArray
	case "object":
		k = KindObject
	case "null":
		k = KindNull
	}
	return append(subs, &Schema{Kind: k, Location: nodeLoc(parent, file)})
}

func (l *Loader) fillObject(s *Schema, n *yaml.Node, file string, seen map[string]bool) error {
	req := map[string]bool{}
	if v := mapField(n, "required"); v != nil {
		for _, r := range seqNodes(v) {
			req[scalarString(r)] = true
		}
	}
	s.RequiredSet = req
	props := mapField(n, "properties")
	names := mapKeys(props)
	sort.Strings(names)
	for _, name := range names {
		pn := mapField(props, name)
		ps, err := l.buildSchema(pn, file, seen)
		if err != nil {
			return err
		}
		s.Properties = append(s.Properties, &Property{
			Name:     name,
			Required: req[name],
			Schema:   ps,
			Location: nodeLoc(pn, file),
		})
	}
	if ap := mapField(n, "additionalProperties"); ap != nil {
		s.AdditionalSet = true
		if b, ok := scalarValue(ap).(bool); ok {
			s.AdditionalAllowed = b
		} else {
			s.AdditionalAllowed = true
			as, err := l.buildSchema(ap, file, seen)
			if err != nil {
				return err
			}
			s.Additional = as
		}
	}
	return nil
}

func enumValues(n *yaml.Node) []any {
	var out []any
	for _, e := range seqNodes(n) {
		out = append(out, scalarValue(e))
	}
	return out
}

func fillConstraints(s *Schema, n *yaml.Node) {
	if v := mapField(n, "minLength"); v != nil {
		if i, err := strconv.Atoi(scalarString(v)); err == nil {
			s.MinLength = &i
		}
	}
	if v := mapField(n, "maxLength"); v != nil {
		if i, err := strconv.Atoi(scalarString(v)); err == nil {
			s.MaxLength = &i
		}
	}
	if v := mapField(n, "pattern"); v != nil {
		s.Pattern = scalarString(v)
	}
	if v := mapField(n, "minimum"); v != nil {
		if f, err := strconv.ParseFloat(scalarString(v), 64); err == nil {
			s.Minimum = &f
		}
	}
	if v := mapField(n, "maximum"); v != nil {
		if f, err := strconv.ParseFloat(scalarString(v), 64); err == nil {
			s.Maximum = &f
		}
	}
	if v := mapField(n, "exclusiveMinimum"); v != nil {
		if b, ok := scalarValue(v).(bool); ok {
			s.ExclusiveMin = b
		} else if _, err := strconv.ParseFloat(scalarString(v), 64); err == nil {
			s.ExclusiveMin = true
		}
	}
	if v := mapField(n, "exclusiveMaximum"); v != nil {
		if b, ok := scalarValue(v).(bool); ok {
			s.ExclusiveMax = b
		} else if _, err := strconv.ParseFloat(scalarString(v), 64); err == nil {
			s.ExclusiveMax = true
		}
	}
	if v := mapField(n, "minItems"); v != nil {
		if i, err := strconv.Atoi(scalarString(v)); err == nil {
			s.MinItems = &i
		}
	}
	if v := mapField(n, "maxItems"); v != nil {
		if i, err := strconv.Atoi(scalarString(v)); err == nil {
			s.MaxItems = &i
		}
	}
	if v := mapField(n, "uniqueItems"); v != nil {
		if b, ok := scalarValue(v).(bool); ok {
			s.UniqueItems = b
		}
	}
	if v := mapField(n, "minProperties"); v != nil {
		if i, err := strconv.Atoi(scalarString(v)); err == nil {
			s.MinProps = &i
		}
	}
	if v := mapField(n, "maxProperties"); v != nil {
		if i, err := strconv.Atoi(scalarString(v)); err == nil {
			s.MaxProps = &i
		}
	}
	if v := mapField(n, "deprecated"); v != nil {
		if b, ok := scalarValue(v).(bool); ok {
			s.Deprecated = b
		}
	}
	if v := mapField(n, "nullable"); v != nil {
		if b, ok := scalarValue(v).(bool); ok {
			s.Nullable = b
		}
	}
}
