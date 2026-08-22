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
			mode := int64(info.Mode().Perm() & 0o755)
			if mode&0o111 == 0 {
				mode = 0o644
			}
			entries = append(entries, archiveEntry{name: name, mode: mode, typeflag: tar.TypeReg, body: body})
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
