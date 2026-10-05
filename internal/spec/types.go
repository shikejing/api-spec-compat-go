// Package spec defines the normalized, version-agnostic model that
// interface description documents (OpenAPI 3.x / Swagger 2.0) are
// loaded into before linting or diffing.
package spec

import "fmt"

// Location points at a position in a source file.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

func (l Location) String() string {
	if l.File == "" {
		return "<unknown>"
	}
	return fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Col)
}

// Version identifies the description language version of a document.
type Version string

const (
	VersionOpenAPI3 Version = "openapi3"
	VersionSwagger2 Version = "swagger2"
)

// Document is a normalized interface description document.
type Document struct {
	Version    Version              `json:"version"`
	SourceFile string               `json:"sourceFile"`
	Title      string               `json:"title,omitempty"`
	DocVersion string               `json:"docVersion,omitempty"`
	Paths      map[string]*PathItem `json:"paths,omitempty"`
	// Components holds named reusable schemas (components/schemas in
	// OpenAPI 3, definitions in Swagger 2), fully dereferenced.
	Components map[string]*Schema `json:"components,omitempty"`
}

// PathItem groups the operations available on one path.
type PathItem struct {
	Path       string                `json:"path"`
	Operations map[string]*Operation `json:"operations,omitempty"` // keyed by lowercase HTTP method
	Loc        Location              `json:"-"`
}

// Operation is a single HTTP method on a path.
type Operation struct {
	Method      string               `json:"method"`
	Path        string               `json:"path"`
	OperationID string               `json:"operationId,omitempty"`
	Parameters  []*Parameter         `json:"parameters,omitempty"`
	RequestBody *Schema              `json:"requestBody,omitempty"`
	Responses   map[string]*Response `json:"responses,omitempty"` // keyed by status code or "default"
	Loc         Location             `json:"-"`
}

// Parameter is a normalized non-body parameter.
type Parameter struct {
	Name     string   `json:"name"`
	In       string   `json:"in"` // query, header, path, cookie
	Required bool     `json:"required"`
	Schema   *Schema  `json:"schema,omitempty"`
	Loc      Location `json:"-"`
}

// Response is a normalized response for one status code.
type Response struct {
	Status      string   `json:"status"`
	Description string   `json:"description,omitempty"`
	Schema      *Schema  `json:"schema,omitempty"`
	Loc         Location `json:"-"`
}

// Schema is a normalized schema node. References are resolved during
// loading; a cycle is represented by a node with Cyclic set and Ref
// naming the component it points back to.
type Schema struct {
	Ref                  string             `json:"ref,omitempty"` // component name this node was resolved from, if any
	Cyclic               bool               `json:"cyclic,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Nullable             bool               `json:"nullable,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	AllOf                []*Schema          `json:"allOf,omitempty"`
	OneOf                []*Schema          `json:"oneOf,omitempty"`
	AnyOf                []*Schema          `json:"anyOf,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
	// AdditionalPropertiesSet records whether additionalProperties was
	// explicitly present in the source (false vs absent differ).
	AdditionalPropertiesSet bool     `json:"additionalPropertiesSet,omitempty"`
	Loc                     Location `json:"-"`
}

// RequiredSet returns the required list as a set for lookup.
func (s *Schema) RequiredSet() map[string]bool {
	m := make(map[string]bool, len(s.Required))
	for _, r := range s.Required {
		m[r] = true
	}
	return m
}
