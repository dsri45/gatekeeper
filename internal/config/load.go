package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads, decodes, normalizes, and validates a Gatekeeper YAML file.
func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	cfg, err := decode(file)
	if err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}

	return cfg, nil
}

func decode(reader io.Reader) (Config, error) {
	cfg := defaults()
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)

	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode YAML: %w", err)
	}

	var extra any
	err := decoder.Decode(&extra)
	if err == nil {
		return Config{}, errors.New("multiple YAML documents are not supported")
	}
	if !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode trailing YAML: %w", err)
	}

	normalize(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate: %w", err)
	}

	return cfg, nil
}

func normalize(cfg *Config) {
	cfg.Server.Address = strings.TrimSpace(cfg.Server.Address)
	cfg.Redis.Address = strings.TrimSpace(cfg.Redis.Address)
	cfg.Redis.FailurePolicy = strings.ToLower(strings.TrimSpace(cfg.Redis.FailurePolicy))

	for index := range cfg.Routes {
		route := &cfg.Routes[index]
		route.Name = strings.TrimSpace(route.Name)
		route.Method = strings.ToUpper(strings.TrimSpace(route.Method))
		route.PathPrefix = strings.TrimSpace(route.PathPrefix)
		route.Backend = strings.TrimSpace(route.Backend)
	}
}
