package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// Rules returns all built-in rules in stable ID order.
func Rules() []Rule {
	rules := []Rule{
		ruleOperationIDRequired{},
		ruleOperationIDUnique{},
		rulePathNoTrailingSlash{},
		rulePathParamDeclared{},
		ruleResponseDescription{},
		ruleSuccessResponseSchema{},
		ruleSchemaTypeRequired{},
		ruleEnumNonempty{},
		ruleParamNameCase{},
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID() < rules[j].ID() })
	return rules
}

// eachOperation calls fn for every operation in deterministic order.
func eachOperation(doc *spec.Document, fn func(op *spec.Operation)) {
	for _, path := range spec.SortedKeys(doc.Paths) {
		item := doc.Paths[path]
		for _, method := range spec.SortedKeys(item.Operations) {
			fn(item.Operations[method])
		}
	}
}

// eachSchema walks every schema reachable from the document, following
// aliases, with cycle protection. fn receives each schema once.
func eachSchema(doc *spec.Document, fn func(s *spec.Schema)) {
	seen := map[*spec.Schema]bool{}
	var walk func(s *spec.Schema)
	walk = func(s *spec.Schema) {
		s = spec.Deref(s)
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		fn(s)
		for _, name := range spec.SortedKeys(s.Properties) {
			walk(s.Properties[name])
		}
		walk(s.Items)
		walk(s.AdditionalProperties)
		for _, sub := range s.AllOf {
			walk(sub)
		}
		for _, sub := range s.OneOf {
			walk(sub)
		}
		for _, sub := range s.AnyOf {
			walk(sub)
		}
	}
	for _, name := range spec.SortedKeys(doc.Components) {
		walk(doc.Components[name])
	}
	eachOperation(doc, func(op *spec.Operation) {
		for _, p := range op.Parameters {
			walk(p.Schema)
		}
		walk(op.RequestBody)
		for _, status := range spec.SortedKeys(op.Responses) {
			walk(op.Responses[status].Schema)
		}
	})
}

// --- operation-id-required ---

type ruleOperationIDRequired struct{}

func (ruleOperationIDRequired) ID() string                { return "operation-id-required" }
func (ruleOperationIDRequired) DefaultSeverity() Severity { return SeverityError }
func (ruleOperationIDRequired) Description() string {
	return "every operation must declare an operationId"
}
func (r ruleOperationIDRequired) Check(doc *spec.Document, report func(Finding)) {
	eachOperation(doc, func(op *spec.Operation) {
		if op.OperationID == "" {
			report(Finding{
				Message:    fmt.Sprintf("operation %s %s has no operationId", strings.ToUpper(op.Method), op.Path),
				Suggestion: "add a unique operationId so generated clients have stable method names",
				Location:   op.Loc,
			})
		}
	})
}

// --- operation-id-unique ---

type ruleOperationIDUnique struct{}

func (ruleOperationIDUnique) ID() string                { return "operation-id-unique" }
func (ruleOperationIDUnique) DefaultSeverity() Severity { return SeverityError }
func (ruleOperationIDUnique) Description() string {
	return "operationId values must be unique across the document"
}
func (r ruleOperationIDUnique) Check(doc *spec.Document, report func(Finding)) {
	seen := map[string]string{}
	eachOperation(doc, func(op *spec.Operation) {
		if op.OperationID == "" {
			return
		}
		where := fmt.Sprintf("%s %s", strings.ToUpper(op.Method), op.Path)
		if prev, dup := seen[op.OperationID]; dup {
			report(Finding{
				Message:    fmt.Sprintf("operationId %q used by both %s and %s", op.OperationID, prev, where),
				Suggestion: "rename one of the operations so operationId values are unique",
				Location:   op.Loc,
			})
		} else {
			seen[op.OperationID] = where
		}
	})
}

// --- path-no-trailing-slash ---

type rulePathNoTrailingSlash struct{}

func (rulePathNoTrailingSlash) ID() string                { return "path-no-trailing-slash" }
func (rulePathNoTrailingSlash) DefaultSeverity() Severity { return SeverityWarn }
func (rulePathNoTrailingSlash) Description() string {
	return "paths must not end with a trailing slash"
}
func (r rulePathNoTrailingSlash) Check(doc *spec.Document, report func(Finding)) {
	for _, path := range spec.SortedKeys(doc.Paths) {
		if len(path) > 1 && strings.HasSuffix(path, "/") {
			report(Finding{
				Message:    fmt.Sprintf("path %q ends with a trailing slash", path),
				Suggestion: fmt.Sprintf("use %q instead", strings.TrimSuffix(path, "/")),
				Location:   doc.Paths[path].Loc,
			})
		}
	}
}

// --- path-param-declared ---

var pathParamRE = regexp.MustCompile(`\{([^}/]+)\}`)

type rulePathParamDeclared struct{}

func (rulePathParamDeclared) ID() string                { return "path-param-declared" }
func (rulePathParamDeclared) DefaultSeverity() Severity { return SeverityError }
func (rulePathParamDeclared) Description() string {
	return "every {placeholder} in a path must be declared as a path parameter, and vice versa"
}
func (r rulePathParamDeclared) Check(doc *spec.Document, report func(Finding)) {
	for _, path := range spec.SortedKeys(doc.Paths) {
		item := doc.Paths[path]
		templated := map[string]bool{}
		for _, m := range pathParamRE.FindAllStringSubmatch(path, -1) {
			templated[m[1]] = true
		}
		for _, method := range spec.SortedKeys(item.Operations) {
			op := item.Operations[method]
			declared := map[string]*spec.Parameter{}
			for _, p := range op.Parameters {
				if p.In == "path" {
					declared[p.Name] = p
				}
			}
			for name := range templated {
				if _, ok := declared[name]; !ok {
					report(Finding{
						Message:    fmt.Sprintf("path placeholder {%s} of %s %s is not declared as a path parameter", name, strings.ToUpper(method), path),
						Suggestion: fmt.Sprintf("add a parameter with name %q and in: path", name),
						Location:   op.Loc,
					})
				}
			}
			for name, p := range declared {
				if !templated[name] {
					report(Finding{
						Message:    fmt.Sprintf("path parameter %q of %s %s does not appear in the path template", name, strings.ToUpper(method), path),
						Suggestion: fmt.Sprintf("rename the parameter to match a {%s} placeholder or remove it", name),
						Location:   p.Loc,
					})
				}
			}
		}
	}
}

// --- response-description-required ---

type ruleResponseDescription struct{}

func (ruleResponseDescription) ID() string                { return "response-description-required" }
func (ruleResponseDescription) DefaultSeverity() Severity { return SeverityWarn }
func (ruleResponseDescription) Description() string {
	return "every response must have a non-empty description"
}
func (r ruleResponseDescription) Check(doc *spec.Document, report func(Finding)) {
	eachOperation(doc, func(op *spec.Operation) {
		for _, status := range spec.SortedKeys(op.Responses) {
			resp := op.Responses[status]
			if strings.TrimSpace(resp.Description) == "" {
				report(Finding{
					Message:    fmt.Sprintf("response %s of %s %s has no description", status, strings.ToUpper(op.Method), op.Path),
					Suggestion: "add a description explaining when this response is returned",
					Location:   resp.Loc,
				})
			}
		}
	})
}

// --- success-response-schema ---

type ruleSuccessResponseSchema struct{}

func (ruleSuccessResponseSchema) ID() string                { return "success-response-schema" }
func (ruleSuccessResponseSchema) DefaultSeverity() Severity { return SeverityWarn }
func (ruleSuccessResponseSchema) Description() string {
	return "2xx responses other than 204 should declare a response schema"
}
func (r ruleSuccessResponseSchema) Check(doc *spec.Document, report func(Finding)) {
	eachOperation(doc, func(op *spec.Operation) {
		for _, status := range spec.SortedKeys(op.Responses) {
			resp := op.Responses[status]
			if !strings.HasPrefix(status, "2") || status == "204" {
				continue
			}
			if resp.Schema == nil {
				report(Finding{
					Message:    fmt.Sprintf("success response %s of %s %s declares no schema", status, strings.ToUpper(op.Method), op.Path),
					Suggestion: "declare a response schema, or use 204 if the response has no body",
					Location:   resp.Loc,
				})
			}
		}
	})
}

// --- schema-type-required ---

type ruleSchemaTypeRequired struct{}

func (ruleSchemaTypeRequired) ID() string                { return "schema-type-required" }
func (ruleSchemaTypeRequired) DefaultSeverity() Severity { return SeverityWarn }
func (ruleSchemaTypeRequired) Description() string {
	return "every schema must declare a type, a composition (oneOf/anyOf/allOf) or an enum"
}
func (r ruleSchemaTypeRequired) Check(doc *spec.Document, report func(Finding)) {
	eachSchema(doc, func(s *spec.Schema) {
		if s.Type == "" && len(s.OneOf) == 0 && len(s.AnyOf) == 0 && len(s.AllOf) == 0 && len(s.Enum) == 0 {
			report(Finding{
				Message:    fmt.Sprintf("schema %s declares no type or composition", schemaName(s)),
				Suggestion: "add an explicit type so consumers do not have to guess",
				Location:   s.Loc,
			})
		}
	})
}

func schemaName(s *spec.Schema) string {
	if s.Ref != "" {
		return fmt.Sprintf("%q", s.Ref)
	}
	return fmt.Sprintf("at %s", s.Loc)
}

// --- enum-nonempty ---

type ruleEnumNonempty struct{}

func (ruleEnumNonempty) ID() string                { return "enum-values-valid" }
func (ruleEnumNonempty) DefaultSeverity() Severity { return SeverityError }
func (ruleEnumNonempty) Description() string {
	return "enum values must be non-empty and free of duplicates"
}
func (r ruleEnumNonempty) Check(doc *spec.Document, report func(Finding)) {
	eachSchema(doc, func(s *spec.Schema) {
		if s.Enum == nil {
			return
		}
		if len(s.Enum) == 0 {
			report(Finding{
				Message:    fmt.Sprintf("schema %s declares an empty enum", schemaName(s)),
				Suggestion: "remove the enum or add at least one allowed value",
				Location:   s.Loc,
			})
			return
		}
		seen := map[string]bool{}
		for _, v := range s.Enum {
			key := fmt.Sprintf("%#v", v)
			if seen[key] {
				report(Finding{
					Message:    fmt.Sprintf("schema %s has duplicate enum value %v", schemaName(s), v),
					Suggestion: "remove the duplicate enum value",
					Location:   s.Loc,
				})
			}
			seen[key] = true
		}
	})
}

// --- param-name-case ---

var (
	camelCaseRE = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	kebabCaseRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
)

type ruleParamNameCase struct{}

func (ruleParamNameCase) ID() string                { return "param-name-case" }
func (ruleParamNameCase) DefaultSeverity() Severity { return SeverityWarn }
func (ruleParamNameCase) Description() string {
	return "query/cookie/path parameter names should be lowerCamelCase, header names kebab-case"
}
func (r ruleParamNameCase) Check(doc *spec.Document, report func(Finding)) {
	eachOperation(doc, func(op *spec.Operation) {
		for _, p := range op.Parameters {
			var ok bool
			var want string
			switch p.In {
			case "header":
				ok = kebabCaseRE.MatchString(p.Name)
				want = "kebab-case"
			case "query", "cookie", "path":
				ok = camelCaseRE.MatchString(p.Name)
				want = "lowerCamelCase"
			default:
				continue
			}
			if !ok {
				report(Finding{
					Message:    fmt.Sprintf("parameter %q (in: %s) of %s %s is not %s", p.Name, p.In, strings.ToUpper(op.Method), op.Path, want),
					Suggestion: fmt.Sprintf("rename the parameter using %s for consistency", want),
					Location:   p.Loc,
				})
			}
		}
	})
}
