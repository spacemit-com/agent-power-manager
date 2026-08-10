package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

type recordingJournalStore struct {
	journal  Journal
	found    bool
	events   *[]string
	fail     map[string]error
	onCreate func(Journal)
	onUpdate func(Journal)
}

func (s *recordingJournalStore) Inspect() (Journal, bool, error) {
	if err := s.fail["inspect"]; err != nil {
		return Journal{}, false, err
	}
	return s.journal, s.found, nil
}
func (s *recordingJournalStore) Create(journal Journal) error {
	*s.events = append(*s.events, "create:"+string(journal.Phase))
	if err := s.fail["create"]; err != nil {
		return err
	}
	if s.found {
		return ErrActiveTransaction
	}
	s.journal, s.found = journal, true
	if s.onCreate != nil {
		s.onCreate(journal)
	}
	return nil
}
func (s *recordingJournalStore) Update(journal Journal) error {
	*s.events = append(*s.events, "update:"+string(journal.Phase))
	if err := s.fail["update:"+string(journal.Phase)]; err != nil {
		return err
	}
	s.journal, s.found = journal, true
	if s.onUpdate != nil {
		s.onUpdate(journal)
	}
	return nil
}
func (s *recordingJournalStore) Delete(id string) error {
	*s.events = append(*s.events, "delete:journal")
	if err := s.fail["delete"]; err != nil {
		return err
	}
	if !s.found || s.journal.TransactionID != id {
		return ErrInvalidJournal
	}
	s.found = false
	return nil
}

type recordingManifestWriter struct {
	events    *[]string
	setErr    error
	removeErr error
	setRef    domain.PowerRef
	removed   domain.PowerRef
}

func (w *recordingManifestWriter) Set(_ domain.AgentID, ref domain.PowerRef) error {
	*w.events = append(*w.events, "manifest:set")
	if w.setErr != nil {
		return w.setErr
	}
	w.setRef = ref
	return nil
}
func (w *recordingManifestWriter) Remove(_ domain.AgentID, powerType domain.PowerType, name string) error {
	*w.events = append(*w.events, "manifest:remove")
	if w.removeErr != nil {
		return w.removeErr
	}
	w.removed = domain.PowerRef{Name: name, Type: powerType}
	return nil
}

type recordingAdapter struct {
	target    string
	events    *[]string
	result    adapter.InstallResult
	err       error
	onInstall func() error
}

func (a *recordingAdapter) DetectAgent() (bool, error)                 { return true, nil }
func (a *recordingAdapter) TargetPath(domain.PowerRef) (string, error) { return a.target, nil }
func (a *recordingAdapter) Install(domain.PowerRef, string) (adapter.InstallResult, error) {
	*a.events = append(*a.events, "adapter:install")
	if a.onInstall != nil {
		if err := a.onInstall(); err != nil {
			return adapter.InstallResult{}, err
		}
	}
	return a.result, a.err
}

type installFixture struct {
	t         *testing.T
	events    []string
	target    string
	staging   string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   *recordingAdapter
	service   *Service
	request   InstallRequest
}

func newInstallFixture(t *testing.T) *installFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "skills", "demo")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := &installFixture{t: t, target: target, staging: staging}
	fixture.journals = &recordingJournalStore{events: &fixture.events, fail: map[string]error{}}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.adapter = &recordingAdapter{target: target, events: &fixture.events, result: adapter.InstallResult{Published: true}}
	fixture.adapter.onInstall = func() error {
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			return errors.New("target is not empty before adapter")
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, "version"), []byte("new"), 0o600)
	}
	fixture.service = newService(fixture.manifests, fixture.journals)
	fixture.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	fixture.request = InstallRequest{
		Agent:       domain.AgentCodex,
		Power:       domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "2.0.0"},
		StagingRoot: staging,
		Adapter:     fixture.adapter,
	}
	return fixture
}

func TestInstallFirstInstallPublishesSetsManifestAndCleansJournal(t *testing.T) {
	fixture := newInstallFixture(t)
	if err := os.MkdirAll(fixture.staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Install(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after successful install")
	}
	if fixture.manifests.setRef != fixture.request.Power {
		t.Fatalf("Set ref = %+v", fixture.manifests.setRef)
	}
	assertEventSubsequence(t, fixture.events, []string{"create:preparing", "update:publishing", "adapter:install", "update:committing", "manifest:set", "delete:journal"})
}

func TestInstallUpgradeBacksUpOldTargetBeforePublishing(t *testing.T) {
	fixture := newInstallFixture(t)
	old := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	fixture.request.Previous = &old
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.target, "version"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Install(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.target, "version"))
	if err != nil || string(contents) != "new" {
		t.Fatalf("target = %q, %v", contents, err)
	}
}

func TestInstallRejectsUnmanagedTargetBeforeJournal(t *testing.T) {
	fixture := newInstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, adapter.ErrTargetConflict) {
		t.Fatalf("Install() error = %v", err)
	}
	if fixture.journals.found || len(fixture.events) != 0 {
		t.Fatalf("journal/events = %v, %v", fixture.journals.found, fixture.events)
	}
}

func TestInstallPrePublicationFailureRestoresBackup(t *testing.T) {
	fixture := newInstallFixture(t)
	old := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	fixture.request.Previous = &old
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.target, "version"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	installErr := errors.New("adapter failed")
	fixture.adapter.result = adapter.InstallResult{}
	fixture.adapter.err = installErr
	fixture.adapter.onInstall = nil

	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, installErr) {
		t.Fatalf("Install() error = %v", err)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "version"))
	if readErr != nil || string(contents) != "old" {
		t.Fatalf("restored target = %q, %v", contents, readErr)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after successful rollback")
	}
	assertEventSubsequence(t, fixture.events, []string{"update:publishing", "adapter:install", "update:rolling-back", "delete:journal"})
}

func TestInstallPublishedErrorPreservesJournalAndNewTarget(t *testing.T) {
	fixture := newInstallFixture(t)
	cleanupErr := errors.New("staging cleanup failed")
	fixture.adapter.err = cleanupErr
	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("Install() error = %v", err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
	if _, statErr := os.Stat(fixture.target); statErr != nil {
		t.Fatalf("published target removed: %v", statErr)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
}

func TestInstallManifestFailurePreservesCommittingJournal(t *testing.T) {
	fixture := newInstallFixture(t)
	fixture.manifests.setErr = errors.New("set failed")
	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, fixture.manifests.setErr) {
		t.Fatalf("Install() error = %v", err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
}

func TestInstallRejectsAdapterSuccessWithoutPublication(t *testing.T) {
	fixture := newInstallFixture(t)
	fixture.adapter.result = adapter.InstallResult{}
	fixture.adapter.onInstall = nil
	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Install() error = %v, want ErrInvalidRequest", err)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after contract-error rollback")
	}
}

func TestInstallRollbackDirectionFailureDoesNotRestoreUnderPublishing(t *testing.T) {
	fixture := newInstallFixture(t)
	old := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	fixture.request.Previous = &old
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.adapter.result = adapter.InstallResult{}
	fixture.adapter.err = errors.New("adapter failed")
	fixture.adapter.onInstall = nil
	phaseErr := errors.New("rolling-back update failed")
	fixture.journals.fail["update:rolling-back"] = phaseErr

	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, phaseErr) {
		t.Fatalf("Install() error = %v, want phase error", err)
	}
	if fixture.journals.journal.Phase != PhasePublishing {
		t.Fatalf("persisted phase = %q, want publishing", fixture.journals.journal.Phase)
	}
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target restored under stale phase: %v", statErr)
	}
	if _, statErr := os.Stat(fixture.journals.journal.BackupPath); statErr != nil {
		t.Fatalf("backup not preserved: %v", statErr)
	}
}

func TestInstallCommittingUpdateFailurePreservesPublishingEvidence(t *testing.T) {
	fixture := newInstallFixture(t)
	phaseErr := errors.New("committing update failed")
	fixture.journals.fail["update:committing"] = phaseErr
	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, phaseErr) {
		t.Fatalf("Install() error = %v", err)
	}
	if fixture.journals.journal.Phase != PhasePublishing {
		t.Fatalf("persisted phase = %q, want publishing", fixture.journals.journal.Phase)
	}
	if _, statErr := os.Stat(fixture.target); statErr != nil {
		t.Fatalf("published target missing: %v", statErr)
	}
}

func TestInstallActiveJournalPreventsNewTransaction(t *testing.T) {
	fixture := newInstallFixture(t)
	fixture.journals.found = true
	fixture.journals.journal = Journal{TransactionID: "ffeeddccbbaa99887766554433221100"}
	if err := fixture.service.Install(context.Background(), fixture.request); !errors.Is(err, ErrActiveTransaction) {
		t.Fatalf("Install() error = %v, want ErrActiveTransaction", err)
	}
}

func assertEventSubsequence(t *testing.T, got, want []string) {
	t.Helper()
	index := 0
	for _, event := range got {
		if index < len(want) && event == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("events = %v, want subsequence %v", got, want)
	}
}

func TestRecordingManifestWriterShape(t *testing.T) {
	var _ ManifestWriter = (*recordingManifestWriter)(nil)
	var _ JournalStore = (*recordingJournalStore)(nil)
	var _ adapter.Adapter = (*recordingAdapter)(nil)
	if reflect.TypeOf((*Manager)(nil)).Elem().NumMethod() != 3 {
		t.Fatal("Manager method count changed")
	}
}
