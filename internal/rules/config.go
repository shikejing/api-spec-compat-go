package rules

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadConfig reads a YAML configuration file of the form:
//
//	rules:
//	  operation-operationId: error
//	  path-params-defined: off
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var raw struct {
		Rules map[string]string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg := Config{Rules: map[string]Severity{}}
	for id, sevStr := range raw.Rules {
		sev, ok := ParseSeverity(sevStr)
		if !ok {
			return Config{}, fmt.Errorf("config %s: rule %q has invalid severity %q", path, id, sevStr)
		}
		cfg.Rules[id] = sev
	}
	return cfg, nil
}

// SetRule returns a copy of cfg with one rule severity switched.
func (c Config) SetRule(id string, sev Severity) Config {
	out := Config{Rules: map[string]Severity{}}
	for k, v := range c.Rules {
		out.Rules[k] = v
	}
	out.Rules[id] = sev
	return out
}
