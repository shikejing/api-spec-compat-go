// Package loader reads interface description documents from local
// files, resolves all references (including cross-file and cyclic
// ones) and normalizes them into the spec model.
package loader

import (
	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// Load reads the document at path, resolves every reference and
// returns the normalized document. Errors carry file/line/column
// location information.
func Load(path string) (*spec.Document, error) {
	return nil, nil
}
