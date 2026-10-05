package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// info-title-required: document must have a non-empty title.
type infoTitleRequired struct{}

func (infoTitleRequired) ID() string                { return "info-title-required" }
func (infoTitleRequired) DefaultSeverity() Severity { return SeverityWarning }
func (infoTitleRequired) Check(doc *spec.Document) []Finding {
	if strings.TrimSpace(doc.Title) != "" {
		return nil
	}
	loc := spec.Location{File: doc.Path, Line: 1, Column: 1}
	if info := spec.MapField(doc.Root, "info"); info != nil {
		loc = *spec.NodeLoc(info, doc.Path)
	}
	return []Finding{finding(loc, "missing or empty info.title", "add info.title with a human-readable API name", "")}
}

// operation-operationId: every operation should declare an operationId.
type operationOperationID struct{}

func (operationOperationID) ID() string                { return "operation-operationId" }
func (operationOperationID) DefaultSeverity() Severity { return SeverityWarning }
func (operationOperationID) Check(doc *spec.Document) []Finding {
	var out []Finding
	for _, op := range doc.Operations {
		if strings.TrimSpace(op.OperationID) == "" {
			out = append(out, finding(*op.Location,
				fmt.Sprintf("operation %s %s has no operationId", op.Method, op.Path),
				"add a unique, stable operationId",
				opKey(op)))
		}
	}
	return out
}

// operation-operationId-unique: operationId values must be unique.
type operationOperationIDUnique struct{}

func (operationOperationIDUnique) ID() string                { return "operation-operationId-unique" }
func (operationOperationIDUnique) DefaultSeverity() Severity { return SeverityError }
func (operationOperationIDUnique) Check(doc *spec.Document) []Finding {
	seen := map[string][]*spec.Operation{}
	for _, op := range doc.Operations {
		if op.OperationID != "" {
			seen[op.OperationID] = append(seen[op.OperationID], op)
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []Finding
	for _, id := range ids {
		ops := seen[id]
		if len(ops) < 2 {
			continue
		}
		for _, op := range ops {
			out = append(out, finding(*op.Location,
				fmt.Sprintf("duplicate operationId %q", id),
				"give every operation a unique operationId",
				opKey(op)))
		}
	}
	return out
}

// path-params-defined: every {placeholder} in the path must be a path parameter.
type pathParamDefined struct{}

func (pathParamDefined) ID() string                { return "path-params-defined" }
func (pathParamDefined) DefaultSeverity() Severity { return SeverityError }
func (pathParamDefined) Check(doc *spec.Document) []Finding {
	var out []Finding
	for _, op := range doc.Operations {
		have := map[string]bool{}
		for _, p := range op.Parameters {
			if p.In == spec.InPath {
				have[p.Name] = true
			}
		}
		for _, ph := range pathPlaceholders(op.Path) {
			if !have[ph] {
				out = append(out, finding(*op.Location,
					fmt.Sprintf("path placeholder {%s} in %s %s has no matching path parameter", ph, op.Method, op.Path),
					fmt.Sprintf("add a required parameter named %q with in: path", ph),
					opKey(op)))
			}
		}
	}
	return out
}

// path-no-operations: a path item that declares no HTTP operations.
type pathNoOperations struct{}

func (pathNoOperations) ID() string                { return "path-no-operations" }
func (pathNoOperations) DefaultSeverity() Severity { return SeverityWarning }
func (pathNoOperations) Check(doc *spec.Document) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, op := range doc.Operations {
		seen[op.Path] = true
	}
	paths := spec.MapKeys(spec.MapField(doc.Root, "paths"))
	sort.Strings(paths)
	for _, p := range paths {
		if strings.HasPrefix(p, "x-") || seen[p] {
			continue
		}
		item := spec.MapField(spec.MapField(doc.Root, "paths"), p)
		out = append(out, finding(*spec.NodeLoc(item, doc.Path),
			fmt.Sprintf("path %s declares no operations", p),
			"add at least one HTTP operation or remove the path",
			"paths"+escapePointer(p)))
	}
	return out
}

// response-2xx-defined: success operations should document a 2xx response.
type response2xxDefined struct{}

func (response2xxDefined) ID() string                { return "response-success-defined" }
func (response2xxDefined) DefaultSeverity() Severity { return SeverityWarning }
func (response2xxDefined) Check(doc *spec.Document) []Finding {
	var out []Finding
	for _, op := range doc.Operations {
		if op.Method == "HEAD" {
			continue
		}
		ok := false
		for _, r := range op.Responses {
			if strings.HasPrefix(r.Status, "2") {
				ok = true
			}
		}
		if !ok {
			out = append(out, finding(*op.Location,
				fmt.Sprintf("operation %s %s documents no 2xx success response", op.Method, op.Path),
				"declare at least one 2xx response",
				opKey(op)+"/responses"))
		}
	}
	return out
}

// response-not-empty: documented responses must have a description or schema.
type responseNotEmpty struct{}

func (responseNotEmpty) ID() string                { return "response-description" }
func (responseNotEmpty) DefaultSeverity() Severity { return SeverityInfo }
func (responseNotEmpty) Check(doc *spec.Document) []Finding {
	var out []Finding
	for _, op := range doc.Operations {
		for _, r := range op.Responses {
			if opHasNoRawDescription(doc, op, r.Status) {
				out = append(out, finding(*r.Location,
					fmt.Sprintf("response %s of %s %s has no description", r.Status, op.Method, op.Path),
					"add a description explaining the response",
					fmt.Sprintf("%s/responses/%s", opKey(op), r.Status)))
			}
		}
	}
	return out
}

func opHasNoRawDescription(doc *spec.Document, op *spec.Operation, status string) bool {
	method := spec.MapField(spec.MapField(spec.MapField(doc.Root, "paths"), op.Path), strings.ToLower(op.Method))
	resp := spec.MapField(spec.MapField(method, "responses"), status)
	if resp == nil {
		return false
	}
	d := spec.MapField(resp, "description")
	return d == nil || strings.TrimSpace(spec.ScalarString(d)) == ""
}

// operation-deprecated: flags deprecated operations.
type operationDeprecated struct{}

func (operationDeprecated) ID() string                { return "operation-deprecated" }
func (operationDeprecated) DefaultSeverity() Severity { return SeverityInfo }
func (operationDeprecated) Check(doc *spec.Document) []Finding {
	var out []Finding
	for _, op := range doc.Operations {
		if op.Deprecated {
			out = append(out, finding(*op.Location,
				fmt.Sprintf("operation %s %s is marked deprecated", op.Method, op.Path),
				"remove the operation or document a replacement/sunset plan",
				opKey(op)))
		}
	}
	return out
}

// helpers

func opKey(op *spec.Operation) string {
	return "paths" + escapePointer(op.Path) + "/" + strings.ToLower(op.Method)
}

func escapePointer(tok string) string {
	return "/" + strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
}

func pathPlaceholders(path string) []string {
	var out []string
	for {
		i := strings.IndexByte(path, '{')
		if i < 0 {
			return out
		}
		path = path[i+1:]
		j := strings.IndexByte(path, '}')
		if j < 0 {
			return out
		}
		out = append(out, path[:j])
		path = path[j+1:]
	}
}
