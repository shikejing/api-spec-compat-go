package rules

import "github.com/shikejing/api-spec-compat-go/internal/spec"

// builtinCheckers returns the ordered list of built-in rules. The order is the
// default presentation order; output is additionally sorted deterministically.
func builtinCheckers() []Checker {
	return []Checker{
		infoTitleRequired{},
		operationOperationID{},
		operationOperationIDUnique{},
		pathParamDefined{},
		pathNoOperations{},
		response2xxDefined{},
		responseNotEmpty{},
		typeValid{},
		refNoCycles{},
		arrayItemsDefined{},
		enumNotEmpty{},
		enumDuplicateValues{},
		propertyRequiredDefined{},
		propertyNamingConvention{},
		operationDeprecated{},
	}
}

// finding builds a Finding with rule id/severity filled later by the engine.
func finding(loc spec.Location, msg, fix string, path string) Finding {
	return Finding{Location: loc, Message: msg, Fix: fix, Path: path}
}

// ListBuiltin exposes the built-in checkers (id and metadata) to callers.
func ListBuiltin() []Checker { return builtinCheckers() }
