package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type filesystemJournalStore struct {
	path     string
	rename   func(string, string) error
	remove   func(string) error
	syncFile func(*os.File) error
	syncDir  func(string) error
}

// newFilesystemJournalStore 创建使用指定集中日志路径的文件系统 Store。
func newFilesystemJournalStore(path string) *filesystemJournalStore {
	return &filesystemJournalStore{
		path:     path,
		rename:   os.Rename,
		remove:   os.Remove,
		syncFile: func(file *os.File) error { return file.Sync() },
		syncDir:  syncDirectory,
	}
}

// Inspect 读取并严格校验唯一活动事务日志。
func (s *filesystemJournalStore) Inspect() (Journal, bool, error) {
	contents, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Journal{}, false, nil
	}
	if err != nil {
		return Journal{}, false, fmt.Errorf("read journal %q: %w", s.path, err)
	}
	journal, err := decodeJournal(contents)
	if err != nil {
		return Journal{}, true, fmt.Errorf("decode journal %q: %w", s.path, err)
	}
	return journal, true, nil
}

// Create 原子创建不存在的活动事务日志。
func (s *filesystemJournalStore) Create(journal Journal) error {
	_, found, err := s.Inspect()
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("create journal %q: %w", s.path, ErrActiveTransaction)
	}
	if err := s.writeAtomic(journal); err != nil {
		return fmt.Errorf("create journal %q: %w", s.path, err)
	}
	return nil
}

// Update 原子更新 transaction ID 匹配的活动日志。
func (s *filesystemJournalStore) Update(journal Journal) error {
	current, found, err := s.Inspect()
	if err != nil {
		return err
	}
	if !found || current.TransactionID != journal.TransactionID {
		return fmt.Errorf("update journal transaction %q: %w", journal.TransactionID, ErrInvalidJournal)
	}
	if err := s.writeAtomic(journal); err != nil {
		return fmt.Errorf("update journal transaction %q: %w", journal.TransactionID, err)
	}
	return nil
}

// Delete 删除 transaction ID 匹配的活动日志并同步目录。
func (s *filesystemJournalStore) Delete(transactionID string) error {
	journal, found, err := s.Inspect()
	if err != nil {
		return err
	}
	if !found || journal.TransactionID != transactionID {
		return fmt.Errorf("delete journal transaction %q: %w", transactionID, ErrInvalidJournal)
	}
	if err := s.remove(s.path); err != nil {
		return fmt.Errorf("delete journal %q: %w", s.path, err)
	}
	if err := s.syncDir(filepath.Dir(s.path)); err != nil {
		restoreErr := s.writeAtomic(journal)
		if restoreErr != nil {
			return errors.Join(
				fmt.Errorf("sync journal directory after delete %q: %w", s.path, err),
				fmt.Errorf("restore journal after delete sync failure: %w", restoreErr),
			)
		}
		return fmt.Errorf("sync journal directory after delete %q: %w", s.path, err)
	}
	return nil
}

func (s *filesystemJournalStore) writeAtomic(journal Journal) (resultErr error) {
	if err := validateJournal(journal); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("encode journal transaction %q: %w", journal.TransactionID, err)
	}
	contents = append(contents, '\n')

	parent := filepath.Dir(s.path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create journal directory %q: %w", parent, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("set journal directory permissions %q: %w", parent, err)
	}
	temporary, err := os.CreateTemp(parent, ".active-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary journal in %q: %w", parent, err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary journal %q: %w", temporaryPath, err))
		}
	}()
	closeWithError := func(operationErr error) error {
		if closeErr := temporary.Close(); closeErr != nil {
			return errors.Join(operationErr, fmt.Errorf("close temporary journal %q: %w", temporaryPath, closeErr))
		}
		return operationErr
	}

	if err := temporary.Chmod(0o600); err != nil {
		return closeWithError(fmt.Errorf("set temporary journal permissions %q: %w", temporaryPath, err))
	}
	if _, err := temporary.Write(contents); err != nil {
		return closeWithError(fmt.Errorf("write temporary journal %q: %w", temporaryPath, err))
	}
	if err := s.syncFile(temporary); err != nil {
		return closeWithError(fmt.Errorf("sync temporary journal %q: %w", temporaryPath, err))
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary journal %q: %w", temporaryPath, err)
	}
	if err := s.rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace journal %q: %w", s.path, err)
	}
	if err := s.syncDir(parent); err != nil {
		return fmt.Errorf("sync journal directory %q: %w", parent, err)
	}
	return nil
}

func decodeJournal(contents []byte) (Journal, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var journal Journal
	if err := decoder.Decode(&journal); err != nil {
		return Journal{}, fmt.Errorf("decode JSON: %w: %w", err, ErrInvalidJournal)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return Journal{}, fmt.Errorf("decode trailing JSON: %w: %w", err, ErrInvalidJournal)
	}
	if err := validateJournal(journal); err != nil {
		return Journal{}, err
	}
	return journal, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		closeErr := directory.Close()
		return errors.Join(err, closeErr)
	}
	return directory.Close()
}

var _ JournalStore = (*filesystemJournalStore)(nil)
