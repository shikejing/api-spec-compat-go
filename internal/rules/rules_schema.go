package rules

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// walkSchema visits every normalized schema reachable from a start schema.
func walkSchema(s *spec.Schema, path string, seen map[schemaVisit]bool, visit func(*spec.Schema, string)) {
	if s == nil {
		return
	}
	key := schemaVisit{ptr: schemaPtr(s), path: path}
	if seen[key] {
		return
	}
	seen[key] = true
	visit(s, path)

	if s.Kind == spec.KindRef {
		// Terminal cycle placeholders have Base == nil.
		if s.Base != nil {
			walkSchema(s.Base, path+"/$ref", seen, visit)
		}
		return
	}
	if s.Items != nil {
		walkSchema(s.Items, path+"/items", seen, visit)
	}
	if s.Additional != nil {
		walkSchema(s.Additional, path+"/additionalProperties", seen, visit)
	}
	if s.Not != nil {
		walkSchema(s.Not, path+"/not", seen, visit)
	}
	for i, sub := range s.Sub {
		walkSchema(sub, fmt.Sprintf("%s/%s/%d", path, s.Kind, i), seen, visit)
	}
	for _, p := range s.Properties {
		walkSchema(p.Schema, path+"/properties/"+p.Name, seen, visit)
	}
}

type schemaVisit struct {
	ptr  uintptrish
	path string
}

// we avoid unsafe: use a string marker derived from location + kind + ref.
type uintptrish string

func schemaPtr(s *spec.Schema) uintptrish {
	if s.RefLocation != nil {
		return uintptrish("ref:" + s.Ref)
	}
	if s.Location != nil {
		return uintptrish(fmt.Sprintf("node:%s:%d:%d:%s", s.Location.File, s.Location.Line, s.Location.Column, s.Kind))
	}
	return uintptrish(fmt.Sprintf("kind:%s:%p", s.Kind, s))
}

// type-valid: every schema must resolve to a known constructor.
type typeValid struct{}

func (typeValid) ID() string                { return "schema-type-valid" }
func (typeValid) DefaultSeverity() Severity { return SeverityError }
func (typeValid) Check(doc *spec.Document) []Finding {
	var out []Finding
	for _, op := range doc.Operations {
		base := opKey(op)
		for _, p := range op.Parameters {
			walkSchema(p.Schema, base+"/parameters", map[schemaVisit]bool{}, func(s *spec.Schema, path string) {
				if s.Kind == spec.KindInvalid {
					out = append(out, finding(*s.Loc(), "schema uses an invalid or unsupported type", "use one of: object, array, string, integer, number, boolean, null", path))
				}
			})
		}
		if op.RequestBody != nil {
			types := make([]string, 0, len(op.RequestBody.Content))
			for mt := range op.RequestBody.Content {
				types = append(types, mt)
			}
			sort.Strings(types)
			for _, mt := range types {
				walkSchema(op.RequestBody.Content[mt], base+"/requestBody/content/"+mt, map[schemaVisit]bool{}, func(s *spec.Schema, path string) {
					if s.Kind == spec.KindInvalid {
						out = append(out, finding(*s.Loc(), "schema uses an invalid or unsupported type", "use a standard JSON Schema type", path))
					}
				})
			}
		}
		for _, r := range op.Responses {
			if r.Body != nil {
				walkSchema(r.Body, base+"/responses/"+r.Status, map[schemaVisit]bool{}, func(s *spec.Schema, path string) {
					if s.Kind == spec.KindInvalid {
						out = append(out, finding(*s.Loc(), "schema uses an invalid or unsupported type", "use a standard JSON Schema type", path))
					}
				})
			}
		}
	}
	return out
}

// ref-no-cycles: reference cycles are legal JSON but must be explicit; we flag
// cycles so authors confirm they did not intend a finite structure.
type refNoCycles struct{}

func (refNoCycles) ID() string                { return "ref-no-cycles" }
func (refNoCycles) DefaultSeverity() Severity { return SeverityInfo }
func (refNoCycles) Check(doc *spec.Document) []Finding {
	out := []Finding{}
	for _, op := range doc.Operations {
		checkRoot := func(s *spec.Schema, path string) {
			walkRefs(s, path, map[string]*spec.Schema{}, map[string]bool{}, &out)
		}
		for _, p := range op.Parameters {
			checkRoot(p.Schema, opKey(op)+"/parameters")
		}
		if op.RequestBody != nil {
			for mt, s := range op.RequestBody.Content {
				checkRoot(s, opKey(op)+"/requestBody/content/"+mt)
			}
		}
		for _, r := range op.Responses {
			checkRoot(r.Body, opKey(op)+"/responses/"+r.Status)
		}
	}
	return out
}

func walkRefs(s *spec.Schema, path string, stack map[string]*spec.Schema, reported map[string]bool, out *[]Finding) {
	if s == nil {
		return
	}
	if s.Kind == spec.KindRef {
		if s.Base == nil {
			// cycle back-edge
			key := s.Ref
			if !reported[key] {
				reported[key] = true
				*out = append(*out, finding(*s.Loc(),
					fmt.Sprintf("reference cycle detected at %s", s.Ref),
					"self-referential schemas are allowed, but must be intentional; document the recursion",
					path+"/$ref"))
			}
			return
		}
		walkRefs(s.Base, path+"/$ref", stack, reported, out)
		return
	}
	if s.Items != nil {
		walkRefs(s.Items, path+"/items", stack, reported, out)
	}
	if s.Additional != nil {
		walkRefs(s.Additional, path+"/additionalProperties", stack, reported, out)
	}
	if s.Not != nil {
		walkRefs(s.Not, path+"/not", stack, reported, out)
	}
	for i, sub := range s.Sub {
		walkRefs(sub, fmt.Sprintf("%s/%s/%d", path, s.Kind, i), stack, reported, out)
	}
	for _, p := range s.Properties {
		walkRefs(p.Schema, path+"/properties/"+p.Name, stack, reported, out)
	}
}

// array-items-defined: array schemas must define items.
type arrayItemsDefined struct{}

func (arrayItemsDefined) ID() string                { return "array-items-defined" }
func (arrayItemsDefined) DefaultSeverity() Severity { return SeverityError }
func (arrayItemsDefined) Check(doc *spec.Document) []Finding {
	var out []Finding
	for _, op := range doc.Operations {
		check := func(s *spec.Schema, path string) {
			walkSchema(s, path, map[schemaVisit]bool{}, func(sc *spec.Schema, p string) {
				if sc.Kind == spec.KindArray && sc.Items == nil {
					out = append(out, finding(*sc.Loc(), "array schema is missing 'items'", "declare the element schema via items", p))
				}
			})
		}
		for _, p := range op.Parameters {
			check(p.Schema, opKey(op)+"/parameters")
		}
		if op.RequestBody != nil {
			for mt, s := range op.RequestBody.Content {
				check(s, opKey(op)+"/requestBody/content/"+mt)
			}
		}
		for _, r := range op.Responses {
			check(r.Body, opKey(op)+"/responses/"+r.Status)
		}
	}
	return out
}

// enum-not-empty: enum must list at least one value.
type enumNotEmpty struct{}

func (enumNotEmpty) ID() string                { return "enum-not-empty" }
func (enumNotEmpty) DefaultSeverity() Severity { return SeverityError }
func (enumNotEmpty) Check(doc *spec.Document) []Finding {
	var out []Finding
	visitEnums(doc, func(s *spec.Schema, path string) {
		if len(s.Enum) == 0 {
			out = append(out, finding(*s.Loc(), "enum declares no values", "list allowed values or remove the enum", path))
		}
	})
	return out
}

// enum-duplicate-values: enum values must be unique.
type enumDuplicateValues struct{}

func (enumDuplicateValues) ID() string                { return "enum-unique-values" }
func (enumDuplicateValues) DefaultSeverity() Severity { return SeverityWarning }
func (enumDuplicateValues) Check(doc *spec.Document) []Finding {
	var out []Finding
	visitEnums(doc, func(s *spec.Schema, path string) {
		seen := map[string]int{}
		var dup []string
		for _, v := range s.Enum {
			k := fmt.Sprintf("%v", v)
			seen[k]++
			if seen[k] == 2 {
				dup = append(dup, k)
			}
		}
		sort.Strings(dup)
		if len(dup) > 0 {
			out = append(out, finding(*s.Loc(),
				fmt.Sprintf("enum contains duplicate values: %s", strings.Join(dup, ", ")),
				"remove duplicate enum entries",
				path+"/enum"))
		}
	})
	return out
}

func visitEnums(doc *spec.Document, fn func(*spec.Schema, string)) {
	for _, op := range doc.Operations {
		run := func(s *spec.Schema, path string) {
			walkSchema(s, path, map[schemaVisit]bool{}, func(sc *spec.Schema, p string) {
				if sc.Enum != nil {
					fn(sc, p)
				}
			})
		}
		for _, p := range op.Parameters {
			run(p.Schema, opKey(op)+"/parameters")
		}
		if op.RequestBody != nil {
			for mt, s := range op.RequestBody.Content {
				run(s, opKey(op)+"/requestBody/content/"+mt)
			}
		}
		for _, r := range op.Responses {
			run(r.Body, opKey(op)+"/responses/"+r.Status)
		}
	}
}

// property-required-defined: properties listed in required must exist.
type propertyRequiredDefined struct{}

func (propertyRequiredDefined) ID() string                { return "object-required-defined" }
func (propertyRequiredDefined) DefaultSeverity() Severity { return SeverityError }
func (propertyRequiredDefined) Check(doc *spec.Document) []Finding {
	var out []Finding
	visitObjects(doc, func(s *spec.Schema, path string) {
		if s.RequiredSet == nil {
			return
		}
		have := map[string]bool{}
		for _, p := range s.Properties {
			have[p.Name] = true
		}
		var missing []string
		for name := range s.RequiredSet {
			if !have[name] {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		for _, name := range missing {
			out = append(out, finding(*s.Loc(),
				fmt.Sprintf("required property %q is not declared in properties", name),
				fmt.Sprintf("define property %q or remove it from required", name),
				path+"/required/"+name))
		}
	})
	return out
}

func visitObjects(doc *spec.Document, fn func(*spec.Schema, string)) {
	for _, op := range doc.Operations {
		run := func(s *spec.Schema, path string) {
			walkSchema(s, path, map[schemaVisit]bool{}, func(sc *spec.Schema, p string) {
				if sc.Kind == spec.KindObject {
					fn(sc, p)
				}
			})
		}
		for _, p := range op.Parameters {
			run(p.Schema, opKey(op)+"/parameters")
		}
		if op.RequestBody != nil {
			for mt, s := range op.RequestBody.Content {
				run(s, opKey(op)+"/requestBody/content/"+mt)
			}
		}
		for _, r := range op.Responses {
			run(r.Body, opKey(op)+"/responses/"+r.Status)
		}
	}
}

var snakeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// property-naming-convention (default off): snake_case property names.
type propertyNamingConvention struct{}

func (propertyNamingConvention) ID() string                { return "property-naming-snake-case" }
func (propertyNamingConvention) DefaultSeverity() Severity { return SeverityOff }
func (propertyNamingConvention) Check(doc *spec.Document) []Finding {
	var out []Finding
	visitObjects(doc, func(s *spec.Schema, path string) {
		for _, p := range s.Properties {
			if !snakeRe.MatchString(p.Name) {
				out = append(out, finding(*p.Location,
					fmt.Sprintf("property %q is not snake_case", p.Name),
					"rename to snake_case or configure this rule off if another convention is used",
					path+"/properties/"+p.Name))
			}
		}
	})
	return out
}
