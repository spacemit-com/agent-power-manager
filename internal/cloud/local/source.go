package local

import (
	"context"
	"io"
	"io/fs"
	"path/filepath"
)

const sourceCopyChunkSize = 32 << 10

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
