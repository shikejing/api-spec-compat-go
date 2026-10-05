package spec

import "fmt"

// Location pinpoints a position inside a source document.
type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Path   string `json:"path,omitempty"`
}

// String renders file:line:column.
func (l Location) String() string {
	if l.File == "" && l.Line == 0 {
		if l.Path != "" {
			return l.Path
		}
		return "<unknown>"
	}
	return fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Column)
}

// WithPath returns a copy carrying a JSON-pointer-like path.
func (l Location) WithPath(p string) Location {
	l.Path = p
	return l
}
