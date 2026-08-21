package local

import (
	"context"
	"io"
	"io/fs"
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
