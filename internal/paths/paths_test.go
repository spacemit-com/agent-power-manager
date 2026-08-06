package paths_test

import (
	"os"
	"path/filepath"
	"testing"

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
