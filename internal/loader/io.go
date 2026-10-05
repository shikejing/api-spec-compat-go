package loader

import (
	"fmt"
	"os"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
	"gopkg.in/yaml.v3"
)

// readYAMLFile reads and parses a YAML file.
func readYAMLFile(abs string, out *yaml.Node) error {
	data, err := os.ReadFile(abs)
	if err != nil {
		return &Error{Loc: spec.Location{File: abs}, Msg: fmt.Sprintf("reading file: %v", err)}
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return &Error{Loc: spec.Location{File: abs}, Msg: fmt.Sprintf("parsing YAML: %v", err)}
	}
	return nil
}
