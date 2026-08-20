package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/paths"
)

// skillAdaptFunc 定义 Agent 对 staging 中 Skill 的原地适配操作。
type skillAdaptFunc func(domain.PowerRef, string) error

// agentProfile 描述内置 Agent 的根目录与 Skill 适配规则；安装探测仅依据 profile 根目录。
type agentProfile struct {
	id          domain.AgentID
	homeEnv     string
	defaultRoot string
	adaptSkill  skillAdaptFunc
}

func noOpSkillAdapt(domain.PowerRef, string) error { return nil }

// rootPath 返回环境变量覆盖或有效 HOME 下的 Agent 根目录。
func (c agentProfile) rootPath() (string, error) {
	if root := strings.TrimSpace(os.Getenv(c.homeEnv)); root != "" {
		return root, nil
	}

	home, err := paths.HomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve agent %q root: %w", c.id, err)
	}
	return filepath.Join(home, c.defaultRoot), nil
}

// detectInstalled 检查 Agent 根目录是否存在且为目录，不以 CLI 是否位于 PATH 作为支持条件。
func (c agentProfile) detectInstalled() (bool, error) {
	root, err := c.rootPath()
	if err != nil {
		return false, err
	}

	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("detect agent %q root %q: %w", c.id, root, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("detect agent %q root %q: %w", c.id, root, ErrAgentNotInstalled)
	}
	return true, nil
}

// skillTargetPath 返回指定 Skill 在 Agent 全局目录中的安装路径。
func (c agentProfile) skillTargetPath(ref domain.PowerRef) (string, error) {
	if ref.Type != domain.PowerTypeSkill || !isSafeSkillName(ref.Name) {
		return "", fmt.Errorf("target path for agent %q skill %q: %w", c.id, ref.Name, ErrInvalidSkill)
	}

	root, err := c.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "skills", ref.Name), nil
}

// pluginTargetPath 返回文件系统型 Plugin 的受控安装路径，不创建目录。
func (c agentProfile) pluginTargetPath(actualName string) (string, error) {
	if !isSafePluginActualName(actualName) {
		return "", fmt.Errorf("target path for agent %q plugin %q: %w", c.id, actualName, ErrInvalidPlugin)
	}
	root, err := c.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "plugins", actualName), nil
}

// isSafeSkillName 报告名称是否为安全且未经清洗的单个路径段。
func isSafeSkillName(name string) bool {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return false
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	return filepath.Clean(name) == name
}
