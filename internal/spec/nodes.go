package spec

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// rootOf unwraps the DocumentNode envelope returned by the YAML decoder.
func rootOf(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return n.Content[0]
	}
	return n
}

// deref follows alias nodes to their anchored target.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// asMap returns the key/value pairs of a mapping node in document order.
func asMap(n *yaml.Node) []kv {
	n = deref(rootOf(n))
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out []kv
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, kv{k: n.Content[i].Value, v: n.Content[i+1]})
	}
	return out
}

type kv struct {
	k string
	v *yaml.Node
}

// mapField looks up a key on a mapping node.
func mapField(n *yaml.Node, key string) *yaml.Node {
	for _, p := range asMap(n) {
		if p.k == key {
			return deref(p.v)
		}
	}
	return nil
}

// mapKeys returns the sorted? no: document-order keys of a mapping.
func mapKeys(n *yaml.Node) []string {
	var out []string
	for _, p := range asMap(n) {
		out = append(out, p.k)
	}
	return out
}

// seqNodes returns the elements of a sequence node.
func seqNodes(n *yaml.Node) []*yaml.Node {
	n = deref(rootOf(n))
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

// scalarString returns the raw value of a scalar node.
func scalarString(n *yaml.Node) string {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// scalarValue decodes a scalar node into a Go value.
func scalarValue(n *yaml.Node) any {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return nil
	}
	switch n.Tag {
	case "!!null":
		return nil
	case "!!bool":
		b, _ := strconv.ParseBool(n.Value)
		return b
	case "!!int":
		// Normalize through float64 so enum comparison is numeric-agnostic.
		f, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return n.Value
		}
		return f
	case "!!float":
		f, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return n.Value
		}
		return f
	default:
		return n.Value
	}
}

// nodeLoc builds a Location from a node's source position.
func nodeLoc(n *yaml.Node, file string) *Location {
	n = deref(n)
	if n == nil {
		return &Location{File: file}
	}
	return &Location{File: file, Line: n.Line, Column: n.Column}
}

// detectFormat determines the description dialect of a root node.
func detectFormat(root *yaml.Node) (Format, string) {
	m := rootOf(root)
	if m == nil || m.Kind != yaml.MappingNode {
		return FormatUnknown, ""
	}
	if v := mapField(m, "openapi"); v != nil {
		s := scalarString(v)
		if strings.HasPrefix(s, "3.") {
			return FormatOpenAPI3, s
		}
		return FormatUnknown, s
	}
	if v := mapField(m, "swagger"); v != nil {
		if scalarString(v) == "2.0" {
			return FormatSwagger2, "2.0"
		}
	}
	return FormatUnknown, ""
}

// Exported node accessors for use by other packages (rules engine).

// MapField looks up a key on a mapping node.
func MapField(n *yaml.Node, key string) *yaml.Node { return mapField(n, key) }

// MapKeys returns document-order keys of a mapping node.
func MapKeys(n *yaml.Node) []string { return mapKeys(n) }

// SeqNodes returns the elements of a sequence node.
func SeqNodes(n *yaml.Node) []*yaml.Node { return seqNodes(n) }

// ScalarString returns the raw scalar value.
func ScalarString(n *yaml.Node) string { return scalarString(n) }

// NodeLoc builds a Location from a node.
func NodeLoc(n *yaml.Node, file string) *Location { return nodeLoc(n, file) }
