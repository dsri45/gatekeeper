package infra

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInfrastructureYAMLFilesAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"bootstrap.yaml",
		"codebuild.yaml",
		"application.yaml",
		"../buildspec.aws.yml",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			var document yaml.Node
			if err := yaml.Unmarshal(contents, &document); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
				t.Fatalf("%s must contain one YAML mapping document", path)
			}
		})
	}
}
