package pluginresource

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/utils/filesystem"
	"go.yaml.in/yaml/v3"
)

const (
	// plugin.yaml 与 __init__.py 位于各自 Agent 的落盘插件叶子根：hermes CLI 从插件目录读取二者，
	// Bibit 只读取同一 plugin.yaml。二者不再共存于同一目录，因此 Bibit 安装目录不会出现 __init__.py。
	pluginManifestPath = "plugin.yaml"
	pluginInitPath     = "__init__.py"
	pluginInitContents = "def register(ctx):\n    return None\n"
)

// writeAgentLandings 在 cache-miss 的单次 pass 内为四个正式适配 Agent 派生互不污染的落盘插件叶子。
// 每个叶子只包含目标 Agent 消费的文件与 canonical Skill 副本：Codex/Claude 为其原生 manifest，
// Hermes 为 plugin.yaml 与注册器，Bibit 仅 plugin.yaml。派生结果与本次安装目标无关；
// 后续 Agent 安装只会命中已发布的共享 artifact，既不重新下载也不重写这些文件。
func writeAgentLandings(root, pluginPath string, canonical pluginartifact.APCPluginManifest) error {
	nativeVersion := strings.TrimPrefix(canonical.Version, "v")
	for _, agent := range pluginartifact.PluginLandingAgents() {
		landingPlugin, err := pluginartifact.LandingPluginPath(root, agent, canonical.Name)
		if err != nil {
			return err
		}
		if err := copyCanonicalSkills(pluginPath, landingPlugin); err != nil {
			return fmt.Errorf("copy canonical Skills into %q landing: %w", agent, err)
		}
		switch agent {
		case domain.AgentCodex:
			manifest := pluginartifact.CodexPluginManifest{
				Name: canonical.Name, Version: nativeVersion, Description: canonical.Description, Skills: "./skills/",
			}
			if err := writeJSONFile(filepath.Join(landingPlugin, codexManifestPath), manifest); err != nil {
				return fmt.Errorf("write Codex Plugin manifest: %w", err)
			}
		case domain.AgentClaudeCode:
			manifest := pluginartifact.ClaudePluginManifest{
				Name: canonical.Name, Version: nativeVersion, Description: canonical.Description, Skills: []string{"./skills/"},
			}
			if err := writeJSONFile(filepath.Join(landingPlugin, claudeManifestPath), manifest); err != nil {
				return fmt.Errorf("write Claude Plugin manifest: %w", err)
			}
		case domain.AgentHermes, domain.AgentBibit:
			manifest := pluginartifact.FilesystemPluginManifest{
				SchemaVersion: pluginartifact.PluginManifestSchemaVersion,
				Name:          canonical.Name,
				Version:       nativeVersion,
				Description:   canonical.Description,
			}
			if err := writeYAMLFile(filepath.Join(landingPlugin, pluginManifestPath), manifest); err != nil {
				return fmt.Errorf("write %s filesystem Plugin manifest: %w", agent, err)
			}
			if agent != domain.AgentHermes {
				continue
			}
			if err := writePrivateFile(filepath.Join(landingPlugin, pluginInitPath), func(writer io.Writer) error {
				_, err := io.WriteString(writer, pluginInitContents)
				return err
			}); err != nil {
				return fmt.Errorf("write Hermes Plugin initializer: %w", err)
			}
		default:
			return fmt.Errorf("derive Plugin landing for agent %q: %w", agent, ErrInvalidArtifact)
		}
	}
	return nil
}

// copyCanonicalSkills 把 canonical 包内已声明的 Skill 子树原样复制到落盘插件叶子。
// canonical 的 skills/ 只含已声明 Skill，落盘叶子的唯一内容来源是 canonical 包，
// 因此不会引入提交方提供的 Agent 私有文件。
func copyCanonicalSkills(pluginPath, landingPlugin string) error {
	return filesystem.CopyDir(filepath.Join(pluginPath, "skills"), filepath.Join(landingPlugin, "skills"))
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
