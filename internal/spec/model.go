package spec

import "gopkg.in/yaml.v3"

// Node is the raw annotated syntax node (works for YAML and JSON).
type Node = yaml.Node

// Format is the detected description dialect.
type Format string

const (
	FormatSwagger2 Format = "swagger-2.0"
	FormatOpenAPI3 Format = "openapi-3"
	FormatUnknown  Format = "unknown"
)

// SchemaKind enumerates the normalized schema constructors.
type SchemaKind string

const (
	KindInvalid SchemaKind = "invalid"
	KindAny     SchemaKind = "any"
	KindNull    SchemaKind = "null"
	KindBoolean SchemaKind = "boolean"
	KindInteger SchemaKind = "integer"
	KindNumber  SchemaKind = "number"
	KindString  SchemaKind = "string"
	KindArray   SchemaKind = "array"
	KindObject  SchemaKind = "object"
	KindEnum    SchemaKind = "enum"
	KindOneOf   SchemaKind = "oneOf"
	KindAnyOf   SchemaKind = "anyOf"
	KindAllOf   SchemaKind = "allOf"
	KindNot     SchemaKind = "not"
	KindRef     SchemaKind = "ref"
)

// Schema is the normalized, reference-resolved schema model.
type Schema struct {
	Kind              SchemaKind
	Ref               string
	RefLocation       *Location
	Base              *Schema
	Items             *Schema
	Properties        []*Property
	Additional        *Schema
	AdditionalSet     bool
	AdditionalAllowed bool
	Sub               []*Schema
	Not               *Schema
	Enum              []any
	Format            string
	Nullable          bool
	Deprecated        bool
	MinLength         *int
	MaxLength         *int
	Pattern           string
	Minimum           *float64
	Maximum           *float64
	ExclusiveMin      bool
	ExclusiveMax      bool
	MinItems          *int
	MaxItems          *int
	UniqueItems       bool
	MinProps          *int
	MaxProps          *int
	RequiredSet       map[string]bool
	Location          *Location
}

// Property is one named object property.
type Property struct {
	Name     string
	Required bool
	Schema   *Schema
	Location *Location
}

// In denotes the parameter location.
type In string

const (
	InPath   In = "path"
	InQuery  In = "query"
	InHeader In = "header"
	InCookie In = "cookie"
	InBody   In = "body"
	InForm   In = "formData"
)

// Parameter is a normalized operation parameter.
type Parameter struct {
	Name       string
	In         In
	Required   bool
	Deprecated bool
	Schema     *Schema
	Location   *Location
}

// Response is one response entry keyed by status code.
type Response struct {
	Status     string
	StatusCode int
	Headers    map[string]*Schema
	Body       *Schema
	Location   *Location
}

// RequestBody is the normalized request body.
type RequestBody struct {
	Required bool
	Content  map[string]*Schema
	Location *Location
}

// Operation is a normalized API operation.
type Operation struct {
	Method      string
	Path        string
	OperationID string
	Deprecated  bool
	Parameters  []*Parameter
	RequestBody *RequestBody
	Responses   []*Response
	Location    *Location
}

// Document is the normalized interface description document.
type Document struct {
	Path       string
	Format     Format
	Version    string
	Title      string
	Operations []*Operation
	Root       *Node
	Loader     *Loader
}

// Loc returns the schema's location, or an unknown location.
func (s *Schema) Loc() *Location {
	if s != nil && s.Location != nil {
		return s.Location
	}
	return &Location{File: "", Line: 0, Column: 0}
}
