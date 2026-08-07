package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
	"apm/internal/paths"
)

func TestHomeDirUsesAPMHomeWhenNonEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := t.TempDir()
	t.Setenv("APM_HOME", want)

	got, err := paths.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestHomeDirFallsBackWhenAPMHomeIsEmpty(t *testing.T) {
	want := t.TempDir()
	t.Setenv("HOME", want)
	t.Setenv("APM_HOME", "")

	got, err := paths.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestHomeDirFallsBackWhenAPMHomeIsUnset(t *testing.T) {
	want := t.TempDir()
	t.Setenv("HOME", want)
	unsetEnv(t, "APM_HOME")

	got, err := paths.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestHomeDirReturnsErrorWhenHomeIsUnavailable(t *testing.T) {
	t.Setenv("APM_HOME", "")
	t.Setenv("HOME", "")

	if _, err := paths.HomeDir(); err == nil {
		t.Fatal("HomeDir() error = nil, want home resolution error")
	}
}

func TestStateAndLockUseAPMHomeWhileCacheUsesXDG(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	state, err := paths.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".apm"); state != want {
		t.Fatalf("StateDir() = %q, want %q", state, want)
	}

	lockPath, err := paths.LockPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".apm", "apm.lock"); lockPath != want {
		t.Fatalf("LockPath() = %q, want %q", lockPath, want)
	}

	cache, err := paths.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "apm"); cache != want {
		t.Fatalf("CachePath() = %q, want %q", cache, want)
	}
}

func TestManifestPathUsesAPMHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)

	got, err := paths.ManifestPath(domain.AgentID("codex"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".apm", "manifests", "codex.json")
	if got != want {
		t.Fatalf("ManifestPath() = %q, want %q", got, want)
	}
}

func TestManifestPathFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", "")
	t.Setenv("HOME", home)

	got, err := paths.ManifestPath(domain.AgentID("claude"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".apm", "manifests", "claude.json")
	if got != want {
		t.Fatalf("ManifestPath() = %q, want %q", got, want)
	}
}

func TestManifestPathPropagatesHomeResolutionError(t *testing.T) {
	t.Setenv("APM_HOME", "")
	t.Setenv("HOME", "")

	if _, err := paths.ManifestPath(domain.AgentID("claude")); err == nil {
		t.Fatal("ManifestPath() error = nil, want home resolution error")
	}
}

func TestCachePathUsesAPMHomeWhenXDGCacheHomeIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")

	got, err := paths.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".cache", "apm"); got != want {
		t.Fatalf("CachePath() = %q, want %q", got, want)
	}
}

func TestCachePathUsesXDGWithoutAPMHome(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	got, err := paths.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "apm"); got != want {
		t.Fatalf("CachePath() = %q, want %q", got, want)
	}
}

func TestCachePathUsesDefaultWithoutOverrides(t *testing.T) {
	t.Setenv("APM_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")

	cacheHome, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	got, err := paths.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "apm"); got != want {
		t.Fatalf("CachePath() = %q, want %q", got, want)
	}
}

func TestCacheAndStagingTemporaryPathsStayWithTheirTargets(t *testing.T) {
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	cacheTemp, err := paths.CacheTempPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "apm", ".tmp"); cacheTemp != want {
		t.Fatalf("CacheTempPath() = %q, want %q", cacheTemp, want)
	}

	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateHome, ".apm", "staging"); staging != want {
		t.Fatalf("StagingPath() = %q, want %q", staging, want)
	}

	stagingTemp, err := paths.StagingTempPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateHome, ".apm", ".tmp", "staging"); stagingTemp != want {
		t.Fatalf("StagingTempPath() = %q, want %q", stagingTemp, want)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	old, existed := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			if err := os.Setenv(name, old); err != nil {
				t.Errorf("restore %s: %v", name, err)
			}
			return
		}
		if err := os.Unsetenv(name); err != nil {
			t.Errorf("unset %s: %v", name, err)
		}
	})
}
