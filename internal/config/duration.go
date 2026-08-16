package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration so YAML values can use Go duration strings.
type Duration struct {
	time.Duration
}

// NewDuration creates a configuration duration from a time.Duration.
func NewDuration(value time.Duration) Duration {
	return Duration{Duration: value}
}

// UnmarshalYAML decodes values such as "100ms", "5s", and "1m".
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a string")
	}

	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}

	d.Duration = value
	return nil
}
