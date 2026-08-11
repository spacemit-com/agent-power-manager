package version

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Selector 在可信的规范候选中选择显式版本或 SemVer 最大版本。
type Selector struct{}

// NewSelector 创建无状态版本选择器。
func NewSelector() *Selector { return &Selector{} }

// Select 精确选择 requested；requested 为空时选择最大 SemVer，且不修改输入。
func (s *Selector) Select(candidates []string, requested string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("select power version: no candidate versions")
	}
	if requested != "" {
		for _, candidate := range candidates {
			if candidate == requested {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("select power version: version %q not found", requested)
	}
	latest := candidates[0]
	for _, candidate := range candidates[1:] {
		if semver.MustParse(candidate).GreaterThan(semver.MustParse(latest)) {
			latest = candidate
		}
	}
	return latest, nil
}
