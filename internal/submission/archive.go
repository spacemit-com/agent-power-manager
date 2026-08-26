package submission

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// BuildTarGz 将资源根目录确定性打包为 .tar.gz。根目录即归档根；条目按路径
// 升序写入，uid/gid/时间戳固定，确保相同内容产出字节级一致的归档。
func BuildTarGz(root string) ([]byte, error) {
	entries, err := collectEntries(root)
	if err != nil {
		return nil, err
	}
	return buildTarGz(entries)
}

// buildTarGz 只消费调用方已经固定的条目快照，不再访问源路径。
func buildTarGz(entries []archiveEntry) ([]byte, error) {
	entries = append([]archiveEntry(nil), entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Header.ModTime = time.Time{}
	gz.Header.Name = ""
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Uid:      0,
			Gid:      0,
			ModTime:  time.Unix(0, 0),
			Typeflag: entry.typeflag,
			Size:     int64(len(entry.body)),
		}
		if err := tw.WriteHeader(header); err != nil {
			return nil, err
		}
		if entry.typeflag == tar.TypeReg {
			if _, err := tw.Write(entry.body); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type archiveEntry struct {
	name     string
	mode     int64
	typeflag byte
	body     []byte
}

// agentTarGz 将已冻结的 Agent 快照确定性打包：普通文件 mode 归一为 0644、目录为 0755。
// 执行位归一必须在 agentartifact.Validate 拒绝之后进行，避免把可执行文件静默降权为普通文件。
func agentTarGz(snapshot sourceTreeSnapshot) ([]byte, error) {
	entries := make([]archiveEntry, 0, len(snapshot.entries))
	for _, entry := range snapshot.entries {
		if entry.typeflag == tar.TypeReg {
			entry.mode = 0o644
		} else {
			entry.mode = 0o755
		}
		entries = append(entries, entry)
	}
	return buildTarGz(entries)
}

// collectEntries 递归收集目录与普通文件；目录 mode 固定 0755，文件保留执行位
// 并清除 setuid/setgid/sticky，无执行位则归一为 0644。
func collectEntries(root string) ([]archiveEntry, error) {
	var entries []archiveEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			entries = append(entries, archiveEntry{name: name + "/", mode: 0o755, typeflag: tar.TypeDir})
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entries = append(entries, archiveEntry{name: name, mode: archiveFileMode(info.Mode()), typeflag: tar.TypeReg, body: body})
		default:
			return fmt.Errorf("unsupported entry type %s: %s", d.Type(), name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk resource root: %w", err)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}

func archiveFileMode(mode fs.FileMode) int64 {
	normalized := int64(mode.Perm() & 0o755)
	if normalized&0o111 == 0 {
		normalized = 0o644
	}
	return normalized
}
