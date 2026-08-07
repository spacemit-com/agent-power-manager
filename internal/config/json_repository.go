package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/paths"
)

// JSONRepository 从本地 JSON 清单查询能力。
type JSONRepository struct{}

// NewJSONRepository 创建 JSON 清单仓库。
func NewJSONRepository() *JSONRepository {
	return &JSONRepository{}
}

// List 返回指定智能体按类别组织的能力引用。
func (r *JSONRepository) List(agent domain.AgentID) (domain.PowerGroups, error) {
	groups, err := r.read(agent)
	if err != nil {
		return nil, err
	}
	return clonePowerGroups(groups), nil
}

// Find 返回指定智能体中第一个匹配的能力引用。
func (r *JSONRepository) Find(agent domain.AgentID, powerType domain.PowerType, name string) (domain.PowerRef, bool, error) {
	if !validPowerType(powerType) || name == "" {
		return domain.PowerRef{}, false, ErrInvalidPower
	}

	groups, err := r.read(agent)
	if err != nil {
		return domain.PowerRef{}, false, err
	}
	for _, power := range groups[powerType] {
		if power.Name == name {
			return power, true, nil
		}
	}
	return domain.PowerRef{}, false, nil
}

func (r *JSONRepository) read(agent domain.AgentID) (domain.PowerGroups, error) {
	path, err := paths.ManifestPath(agent)
	if err != nil {
		return nil, fmt.Errorf("resolve manifest path: %w", err)
	}
	slog.Debug("read manifest", logging.Agent(agent), slog.String("path", path))

	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyPowerGroups(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", path, err)
	}

	var document manifestDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("decode manifest %q: %w", path, err)
	}
	if document.Agent == "" {
		return nil, fmt.Errorf("decode manifest %q: missing agent", path)
	}
	if document.Agent != agent {
		return nil, fmt.Errorf("decode manifest %q: agent %q does not match %q", path, document.Agent, agent)
	}
	return manifestToPowerGroups(document), nil
}

func validPowerType(powerType domain.PowerType) bool {
	switch powerType {
	case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP:
		return true
	default:
		return false
	}
}
