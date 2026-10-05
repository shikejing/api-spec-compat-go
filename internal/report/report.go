package report

import (
	"encoding/json"
	"io"
)

// JSON writes a stable, indented machine-readable JSON document.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
