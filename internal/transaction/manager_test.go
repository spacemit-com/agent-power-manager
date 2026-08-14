package transaction

import (
	"context"
	"errors"
	"testing"
)

func TestNewManagerUsesCentralJournal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	events := []string{}
	resolver := &recordingMCPResolver{events: &events, err: errors.New("unused")}
	manager, err := NewManager(&recordingManifestWriter{events: &events}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := any(manager).(Manager); !ok {
		t.Fatal("NewManager result does not implement Manager")
	}
}

func TestNewManagerRejectsNilManifestWriter(t *testing.T) {
	if _, err := NewManager(nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("NewManager(nil) error = %v", err)
	}
}
