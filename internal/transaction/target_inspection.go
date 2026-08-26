package transaction

import (
	"fmt"

	"apm/internal/domain"
)

// InspectFilesystemTarget 只读判断文件型 Power 的目标路径是否存在。
// 该探针不创建 journal、不写入 manifest，也不执行任何文件变更。
func (s *Service) InspectFilesystemTarget(ref domain.PowerRef, target FilesystemTarget) (TargetPresence, error) {
	path, err := target.TargetPath(ref)
	if err != nil {
		return "", fmt.Errorf("resolve inspect target: %w", err)
	}
	if err := validateSafeAbsolutePath("target", path); err != nil {
		return "", err
	}
	exists, err := s.pathExists(path)
	if err != nil {
		return "", fmt.Errorf("inspect target %q: %w", path, err)
	}
	if exists {
		return TargetPresent, nil
	}
	return TargetAbsent, nil
}
