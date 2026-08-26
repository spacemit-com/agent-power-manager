package local

import (
	"context"
	"io"
	"io/fs"
	"path/filepath"
)

const sourceCopyChunkSize = 32 << 10

// localTreeContentPolicy 对物理树快照中的每个普通文件执行内容策略：
// relative 为快照内相对路径（平台分隔符），body 为该文件已读取并固定的正文；
// 返回非 nil 即拒绝该文件并中止快照。Agent 用自定义策略允许 assets/ 下二进制，
// Skill/Plugin 保持默认文本-only 语义。
type localTreeContentPolicy func(relative string, body []byte) error

// defaultLocalTreeContentPolicy 是 Skill/Plugin 的默认策略：任何包含二进制或
// 归档魔数（contentpolicy.IsUnsafeText）的文件都被拒绝。
func defaultLocalTreeContentPolicy(relative string, body []byte) error {
	if isUnsafePayload(body) {
		return unsafeContent("source file %q contains binary or archive content", relative)
	}
	return nil
}

type treeNodeSnapshot struct {
	relative  string
	mode      fs.FileMode
	directory bool
	contents  []byte
}

type treeSnapshot struct {
	nodes []treeNodeSnapshot
}

func (snapshot *treeSnapshot) requireRegularFile(path string) error {
	required := filepath.Clean(path)
	for _, node := range snapshot.nodes {
		if filepath.Clean(node.relative) == required {
			if !node.directory {
				return nil
			}
			break
		}
	}
	return unsafeContent("captured tree requires physical regular file %q", path)
}

func writeAllContext(ctx context.Context, destination io.Writer, contents []byte) error {
	for len(contents) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := contents
		if len(chunk) > sourceCopyChunkSize {
			chunk = chunk[:sourceCopyChunkSize]
		}
		written, err := destination.Write(chunk)
		if err != nil {
			return err
		}
		if written != len(chunk) {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return ctx.Err()
}
