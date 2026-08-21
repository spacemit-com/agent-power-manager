package pluginresource

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"apm/internal/pluginartifact"
	"go.yaml.in/yaml/v3"
)

const (
	hermesManifestPath = ".hermes-plugin/plugin.yaml"
	hermesInitPath     = ".hermes-plugin/__init__.py"
	hermesInitContents = "def register(ctx):\n    return None\n"
)

func writeAgentManifests(pluginPath string, canonical pluginartifact.APCPluginManifest) error {
	nativeVersion := strings.TrimPrefix(canonical.Version, "v")
	codex := pluginartifact.CodexPluginManifest{
		Name: canonical.Name, Version: nativeVersion, Description: canonical.Description, Skills: "./skills/",
	}
	if err := writeJSONFile(filepath.Join(pluginPath, codexManifestPath), codex); err != nil {
		return fmt.Errorf("write Codex Plugin manifest: %w", err)
	}
	claude := pluginartifact.ClaudePluginManifest{
		Name: canonical.Name, Version: nativeVersion, Description: canonical.Description, Skills: []string{"./skills/"},
	}
	if err := writeJSONFile(filepath.Join(pluginPath, claudeManifestPath), claude); err != nil {
		return fmt.Errorf("write Claude Plugin manifest: %w", err)
	}
	hermes := pluginartifact.HermesPluginManifest{
		Name: canonical.Name, Version: nativeVersion, Description: canonical.Description, Author: "APM",
	}
	if err := writeYAMLFile(filepath.Join(pluginPath, hermesManifestPath), hermes); err != nil {
		return fmt.Errorf("write Hermes Plugin manifest: %w", err)
	}
	if err := writePrivateFile(filepath.Join(pluginPath, hermesInitPath), func(writer io.Writer) error {
		_, err := io.WriteString(writer, hermesInitContents)
		return err
	}); err != nil {
		return fmt.Errorf("write Hermes Plugin initializer: %w", err)
	}
	return nil
}

func writeYAMLFile(path string, value any) error {
	return writePrivateFile(path, func(writer io.Writer) error {
		encoder := yaml.NewEncoder(writer)
		encoder.SetIndent(2)
		encodeErr := encoder.Encode(value)
		closeErr := encoder.Close()
		if encodeErr != nil {
			return encodeErr
		}
		return closeErr
	})
}
