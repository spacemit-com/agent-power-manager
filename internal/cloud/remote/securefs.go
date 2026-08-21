package remote

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"apm/internal/cloud"
)

const secureNameAttempts = 32

func randomSecureName(prefix string) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(token[:]), nil
}

func validSecureEntryName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

func unsafeRemoteFilesystem(format string, arguments ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, arguments...), cloud.ErrUnsafeContent)
}

type linkName func(source, destination string) error
type unlinkName func(name string) error

// linkThenUnlinkNoReplace 先以不覆盖语义创建目标硬链接，再只删除临时源；
// 源删除失败时不得回滚目标，避免误删已被替换的目标名称。
func linkThenUnlinkNoReplace(source, destination string, link linkName, unlink unlinkName) error {
	if err := link(source, destination); err != nil {
		return err
	}
	if err := unlink(source); err != nil {
		return fmt.Errorf("remove published staged source %q: %w", source, err)
	}
	return nil
}

func classifyNoReplaceError(destination string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errSecureDestinationExists) {
		return fmt.Errorf("remote Agent destination %q already exists: %w", destination, errors.Join(cloud.ErrUnsafeContent, err))
	}
	return err
}
