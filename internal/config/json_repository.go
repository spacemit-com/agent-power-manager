package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Masterminds/semver/v3"

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
	groups, err := r.read("List", agent)
	if err != nil {
		return nil, fmt.Errorf("List agent %q: %w", agent, err)
	}
	return clonePowerGroups(groups), nil
}

// Find 返回指定智能体中第一个匹配的能力引用。
func (r *JSONRepository) Find(agent domain.AgentID, powerType domain.PowerType, name string) (domain.PowerRef, bool, error) {
	if !validPowerType(powerType) {
		return domain.PowerRef{}, false, fmt.Errorf("Find agent %q: invalid power type %q: %w", agent, powerType, ErrInvalidPower)
	}
	if name == "" {
		return domain.PowerRef{}, false, fmt.Errorf("Find agent %q: empty power name: %w", agent, ErrInvalidPower)
	}

	groups, err := r.read("Find", agent)
	if err != nil {
		return domain.PowerRef{}, false, fmt.Errorf("Find agent %q: %w", agent, err)
	}
	for _, power := range groups[powerType] {
		if power.Name == name {
			return power, true, nil
		}
	}
	return domain.PowerRef{}, false, nil
}

// Set 新增或替换指定智能体的能力引用。
func (r *JSONRepository) Set(agent domain.AgentID, power domain.PowerRef) error {
	if !validPowerType(power.Type) {
		return fmt.Errorf("Set agent %q: invalid power type %q: %w", agent, power.Type, ErrInvalidPower)
	}
	if power.Name == "" {
		return fmt.Errorf("Set agent %q: empty power name: %w", agent, ErrInvalidPower)
	}
	if err := validateCanonicalVersion(power.Version); err != nil {
		return fmt.Errorf("Set agent %q power %q version %q: %w", agent, power.Name, power.Version, err)
	}

	groups, err := r.read("Set", agent)
	if err != nil {
		return fmt.Errorf("Set agent %q: %w", agent, err)
	}
	powers := groups[power.Type]
	firstMatch := -1
	matchCount := 0
	for index, candidate := range powers {
		if candidate.Name != power.Name {
			continue
		}
		if firstMatch == -1 {
			firstMatch = index
		}
		matchCount++
	}
	if matchCount == 1 && powers[firstMatch] == power {
		slog.Debug(
			"power already set",
			logging.Agent(agent),
			logging.Power(power.Name),
			logging.Version(power.Version),
		)
		return nil
	}

	if firstMatch == -1 {
		groups[power.Type] = append(powers, power)
	} else {
		updated := make([]domain.PowerRef, 0, len(powers)-matchCount+1)
		for index, candidate := range powers {
			switch {
			case index == firstMatch:
				updated = append(updated, power)
			case candidate.Name != power.Name:
				updated = append(updated, candidate)
			}
		}
		groups[power.Type] = updated
	}
	if err := r.write(agent, groups); err != nil {
		return fmt.Errorf("Set agent %q: %w", agent, err)
	}
	return nil
}

// Remove 删除指定智能体目标类别中的全部同名能力引用。
func (r *JSONRepository) Remove(agent domain.AgentID, powerType domain.PowerType, name string) error {
	if !validPowerType(powerType) {
		return fmt.Errorf("Remove agent %q: invalid power type %q: %w", agent, powerType, ErrInvalidPower)
	}
	if name == "" {
		return fmt.Errorf("Remove agent %q: empty power name: %w", agent, ErrInvalidPower)
	}

	groups, err := r.read("Remove", agent)
	if err != nil {
		return fmt.Errorf("Remove agent %q: %w", agent, err)
	}
	powers := groups[powerType]
	filtered := make([]domain.PowerRef, 0, len(powers))
	found := false
	for _, power := range powers {
		if power.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, power)
	}
	if !found {
		slog.Debug(
			"power already absent",
			logging.Agent(agent),
			logging.Power(name),
		)
		return nil
	}

	groups[powerType] = filtered
	if err := r.write(agent, groups); err != nil {
		return fmt.Errorf("Remove agent %q: %w", agent, err)
	}
	return nil
}

var _ Repository = (*JSONRepository)(nil)

var canonicalVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func validateCanonicalVersion(version string) error {
	if !canonicalVersionPattern.MatchString(version) {
		return fmt.Errorf("version %q must match vMAJOR.MINOR.PATCH: %w", version, ErrInvalidPower)
	}
	if _, err := semver.NewVersion(version); err != nil {
		return fmt.Errorf("parse version %q: %w", version, errors.Join(ErrInvalidPower, err))
	}
	return nil
}

func validatePowerGroups(operation string, agent domain.AgentID, groups domain.PowerGroups) error {
	for _, powerType := range []domain.PowerType{domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP} {
		for index, power := range groups[powerType] {
			if err := validateCanonicalVersion(power.Version); err != nil {
				return fmt.Errorf("%s agent %q type %q entry %d power %q version %q: %w",
					operation, agent, powerType, index, power.Name, power.Version, err)
			}
		}
	}
	return nil
}

func (r *JSONRepository) read(operation string, agent domain.AgentID) (domain.PowerGroups, error) {
	path, err := paths.ManifestPath(agent)
	if err != nil {
		return nil, fmt.Errorf("resolve manifest path for agent %q: %w", agent, err)
	}
	slog.Debug("read manifest", logging.Agent(agent), slog.String("path", path))

	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyPowerGroups(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest %q for agent %q: %w", path, agent, err)
	}

	var document manifestDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("decode manifest %q for agent %q: %w", path, agent, err)
	}
	if document.Agent == "" {
		return nil, fmt.Errorf("decode manifest %q for agent %q: missing agent", path, agent)
	}
	if document.Agent != agent {
		return nil, fmt.Errorf("decode manifest %q for agent %q: document agent %q does not match", path, agent, document.Agent)
	}
	groups := manifestToPowerGroups(document)
	if err := validatePowerGroups(operation, agent, groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (r *JSONRepository) write(agent domain.AgentID, groups domain.PowerGroups) (resultErr error) {
	if err := validatePowerGroups("write", agent, groups); err != nil {
		return err
	}
	document := powerGroupsToManifest(agent, groups)
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest for agent %q: %w", agent, err)
	}
	data = append(data, '\n')

	target, err := paths.ManifestPath(agent)
	if err != nil {
		return fmt.Errorf("resolve manifest path for agent %q: %w", agent, err)
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create manifest directory %q for agent %q: %w", parent, agent, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("set manifest directory permissions %q for agent %q: %w", parent, agent, err)
	}

	temp, err := os.CreateTemp(parent, "."+filepath.Base(target)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary manifest for agent %q at %q: %w", agent, target, err)
	}
	tempName := temp.Name()
	defer func() {
		cleanupErr := os.Remove(tempName)
		if cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("remove temporary manifest %q for agent %q: %w", tempName, agent, cleanupErr),
			)
		}
	}()
	closeAfterError := func(operationErr error) error {
		if closeErr := temp.Close(); closeErr != nil {
			return errors.Join(
				operationErr,
				fmt.Errorf("close temporary manifest %q for agent %q: %w", tempName, agent, closeErr),
			)
		}
		return operationErr
	}

	if err := temp.Chmod(0o600); err != nil {
		return closeAfterError(fmt.Errorf("set temporary manifest permissions %q for agent %q: %w", tempName, agent, err))
	}
	if _, err := temp.Write(data); err != nil {
		return closeAfterError(fmt.Errorf("write temporary manifest %q for agent %q: %w", tempName, agent, err))
	}
	if err := temp.Sync(); err != nil {
		return closeAfterError(fmt.Errorf("sync temporary manifest %q for agent %q: %w", tempName, agent, err))
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary manifest %q for agent %q: %w", tempName, agent, err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("replace manifest %q for agent %q: %w", target, agent, err)
	}

	parentDirectory, err := os.Open(parent)
	if err != nil {
		return fmt.Errorf("open manifest directory %q for agent %q: %w", parent, agent, err)
	}
	if err := parentDirectory.Sync(); err != nil {
		syncErr := fmt.Errorf("sync manifest directory %q for agent %q: %w", parent, agent, err)
		if closeErr := parentDirectory.Close(); closeErr != nil {
			return errors.Join(
				syncErr,
				fmt.Errorf("close manifest directory %q for agent %q: %w", parent, agent, closeErr),
			)
		}
		return syncErr
	}
	if err := parentDirectory.Close(); err != nil {
		return fmt.Errorf("close manifest directory %q for agent %q: %w", parent, agent, err)
	}

	slog.Debug("wrote manifest", logging.Agent(agent), slog.String("path", target))
	return nil
}

func validPowerType(powerType domain.PowerType) bool {
	switch powerType {
	case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP:
		return true
	default:
		return false
	}
}
