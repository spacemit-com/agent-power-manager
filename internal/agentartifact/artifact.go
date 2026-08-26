// Package agentartifact 提供 Agent 目录的权威只读校验器。
// 提交、下载、缓存、安装与事务恢复等所有内容边界必须复用本包，
// 不得各自维护字段或 avatar 规则；本包只读取与校验，
// 不清洗、重排或重序列化 agent.yaml。
package agentartifact

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"apm/internal/domain"
)

// Entry 表示 Agent 快照中的单个条目：规范相对路径（/ 分隔）、目录标记、
// 文件模式与正文。目录条目必须携带 nil Data；文件正文由调用方固定。
type Entry struct {
	Path      string
	Directory bool
	Mode      fs.FileMode
	Data      []byte
}

// Snapshot 表示已通过校验的不可变 Agent 快照：解码后的稳定字段、
// 保持输入顺序的条目（Data 已防御性复制）、文件数与正文总大小。
type Snapshot struct {
	Config      domain.AgentConfig
	Entries     []Entry
	FileCount   int
	ContentSize int64
}

// Validate 校验逻辑快照：路径规范化与歧义/重复拒绝、根闭集、
// 可执行文件、嵌套归档、agent.yaml 解码与可选 PowerRef 身份比较、
// avatar PNG 规则；语义失败统一包裹 domain.ErrInvalidAgent。
// ref 为 nil 时跳过身份比较。返回的 Snapshot 不与输入缓冲别名。
func Validate(entries []Entry, ref *domain.PowerRef) (Snapshot, error) {
	index := make(map[string]Entry, len(entries))
	var failures []error
	var agentEntry *Entry

	for indexIn := range entries {
		entry := entries[indexIn]
		if err := canonicalEntryPath(entry.Path); err != nil {
			failures = append(failures, err)
			continue
		}
		if _, duplicate := index[entry.Path]; duplicate {
			failures = append(failures, agentError("snapshot repeats path %q", entry.Path))
			continue
		}
		if !entry.Directory {
			if entry.Mode&0o111 != 0 {
				failures = append(failures, agentError("file %q is executable", entry.Path))
			}
			if hasArchiveExtension(entry.Path) {
				failures = append(failures, agentError("file %q is a nested archive", entry.Path))
			}
			if hasArchiveSignature(entry.Data) {
				failures = append(failures, agentError("file %q has an archive signature", entry.Path))
			}
		}
		if !strings.Contains(entry.Path, "/") {
			switch entry.Path {
			case "agent.yaml":
				if entry.Directory {
					failures = append(failures, agentError("root entry agent.yaml must be a regular file"))
				} else {
					agentEntry = &entries[indexIn]
				}
			case "assets":
				if !entry.Directory {
					failures = append(failures, agentError("root entry assets must be a directory"))
				}
			default:
				failures = append(failures, agentError("root entry %q is outside the closed set", entry.Path))
			}
		}
		index[entry.Path] = entry
	}
	if len(failures) > 0 {
		return Snapshot{}, joinAgentErrors(failures)
	}
	if agentEntry == nil {
		return Snapshot{}, agentError("agent artifact requires agent.yaml at root")
	}
	config, err := domain.DecodeAgentConfigYAML(agentEntry.Data)
	if err != nil {
		return Snapshot{}, err
	}
	if ref != nil {
		if err := config.ValidatePowerRef(*ref); err != nil {
			return Snapshot{}, err
		}
	}
	if err := validateAvatar(config, index); err != nil {
		return Snapshot{}, err
	}

	snapshotEntries := make([]Entry, len(entries))
	fileCount := 0
	var contentSize int64
	for indexIn, entry := range entries {
		cloned := entry
		if entry.Directory {
			cloned.Data = nil
		} else if entry.Data != nil {
			cloned.Data = bytes.Clone(entry.Data)
			fileCount++
			contentSize += int64(len(entry.Data))
		}
		snapshotEntries[indexIn] = cloned
	}
	return Snapshot{Config: config, Entries: snapshotEntries, FileCount: fileCount, ContentSize: contentSize}, nil
}

// agentError 生成包裹 domain.ErrInvalidAgent 的单个语义错误。
func agentError(format string, args ...any) error {
	return errors.Join(domain.ErrInvalidAgent, fmt.Errorf(format, args...))
}

// joinAgentErrors 把多个语义失败合并为一个仍包裹 domain.ErrInvalidAgent 的错误。
func joinAgentErrors(failures []error) error {
	return errors.Join(append([]error{domain.ErrInvalidAgent}, failures...)...)
}

// canonicalEntryPath 要求路径为规范相对路径：非空、非绝对、不含反斜杠，
// 且 path.Clean 后不变（拒绝 ./、../、//、尾斜杠等歧义写法）。
func canonicalEntryPath(value string) error {
	if value == "" || value == "." || path.IsAbs(value) || strings.Contains(value, `\`) || path.Clean(value) != value {
		return agentError("snapshot path %q is not canonical", value)
	}
	return nil
}

var archiveExtensions = []string{".zip", ".tar", ".tgz", ".tar.gz", ".gz", ".bz2", ".xz", ".7z"}

// hasArchiveExtension 按小写后缀识别嵌套归档扩展名。
func hasArchiveExtension(name string) bool {
	lower := strings.ToLower(name)
	for _, extension := range archiveExtensions {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

var archiveSignatures = [][]byte{
	{'P', 'K', 0x03, 0x04},             // zip 本地文件头
	{'P', 'K', 0x05, 0x06},             // zip 空归档
	{'P', 'K', 0x07, 0x08},             // zip 分卷归档
	{0x1f, 0x8b},                       // gzip / tgz / tar.gz
	{'B', 'Z', 'h'},                    // bzip2
	{0xfd, '7', 'z', 'X', 'Z', 0x00},   // xz
	{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}, // 7z
}

// hasArchiveSignature 按已知归档魔数识别伪装文件；tar 检查 257 偏移的 ustar。
func hasArchiveSignature(data []byte) bool {
	for _, signature := range archiveSignatures {
		if bytes.HasPrefix(data, signature) {
			return true
		}
	}
	return len(data) >= 262 && bytes.Equal(data[257:262], []byte("ustar"))
}
