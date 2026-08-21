package pluginresource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestLookupRejectsSymlinkedArtifactRootWithoutChangingOutsideSentinel(t *testing.T) {
	// 捕获错误变更：使用 os.Stat 或跟随根目录 symlink 读取 artifact。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	outside := filepath.Join(t.TempDir(), "outside-artifact")
	writeValidTestArtifact(t, outside)
	sentinel := writeOutsideSentinel(t, outside)
	root := testArtifactRoot(roots.cache, ref)
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
	}
	assertOutsideSentinel(t, sentinel)
}

func TestLookupRejectsSymlinkedPluginLeafWithoutChangingOutsideSentinel(t *testing.T) {
	// 捕获错误变更：只校验 artifact 根目录却允许子路径 symlink 逃逸。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	root := testArtifactRoot(roots.cache, ref)
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "builder.txt"), []byte("built"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-plugin")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "payload.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := writeOutsideSentinel(t, outside)
	if err := os.Symlink(outside, filepath.Join(root, "plugins", "demo")); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
	}
	assertOutsideSentinel(t, sentinel)
}

func TestLookupRejectsSymlinkedArtifactAncestorWithoutChangingTargetSentinel(t *testing.T) {
	// 捕获错误变更：只校验最终 root，允许 cache 根以下的祖先 symlink 重定向 artifact。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	ancestor := filepath.Join(roots.cache, "plugins", "demo")
	if err := os.MkdirAll(filepath.Dir(ancestor), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "artifact-ancestor-target")
	writeValidTestArtifact(t, filepath.Join(target, "v0.1.0"))
	sentinel := writeOutsideSentinel(t, target)
	if err := os.Symlink(target, ancestor); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
	}
	assertOutsideSentinel(t, sentinel)
}

func TestLookupRejectsRegularFileArtifactRootWithoutChangingOutsideSentinel(t *testing.T) {
	// 捕获错误变更：把同名普通文件误认为可读取的 artifact 根目录。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	root := testArtifactRoot(roots.cache, ref)
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := writeOutsideSentinel(t, t.TempDir())

	if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
	}
	assertOutsideSentinel(t, sentinel)
}

func TestEnsureRejectsUnsafePluginNameBeforeConstructingCachePath(t *testing.T) {
	// 捕获错误变更：在身份校验前把 Plugin 名称拼接到共享缓存路径，导致路径逃逸。
	roots := newTestRoots(t)
	service := newService(
		roots.cache,
		roots.cacheTemp,
		roots.staging,
		roots.stagingTemp,
		&testDownloader{},
		testBuilder{},
	)
	outsideRoot := filepath.Join(filepath.Dir(roots.cache), "outside")
	if err := os.MkdirAll(outsideRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := writeOutsideSentinel(t, outsideRoot)

	unsafeRef := testPluginRef()
	unsafeRef.Name = "../../outside"
	if _, err := service.Ensure(context.Background(), unsafeRef); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
	}
	assertOutsideSentinel(t, sentinel)
	if _, err := os.Stat(filepath.Join(outsideRoot, "v0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaped artifact path stat error = %v, want not exist", err)
	}
}

func TestValidatePhysicalTreeRejectsSymlinkWithoutTrustingDirectoryType(t *testing.T) {
	// 捕获错误变更：仅凭 DirEntry.Type 判定节点，文件系统未提供 d_type 时允许路径逃逸。
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := validatePhysicalTree(directory)
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("validatePhysicalTree() error = %v, want ErrInvalidArtifact", err)
	}
}

func testArtifactRoot(cacheRoot string, ref domain.PowerRef) string {
	return filepath.Join(cacheRoot, "plugins", ref.Name, ref.Version)
}

func writeValidTestArtifact(t *testing.T, root string) {
	t.Helper()
	skillRoot := filepath.Join(root, "plugins", "demo", "skills", "demo")
	if err := os.MkdirAll(skillRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "payload.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "builder.txt"), []byte("built"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeOutsideSentinel(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "outside-sentinel.txt")
	if err := os.WriteFile(path, []byte("outside data must remain unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertOutsideSentinel(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if want := "outside data must remain unchanged"; string(got) != want {
		t.Fatalf("outside sentinel = %q, want %q", got, want)
	}
}
