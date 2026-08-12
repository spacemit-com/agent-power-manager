package transaction

import "apm/internal/domain"

// legacyFilesystemJournal 精确描述 schema v1；仅用于只读恢复兼容。
type legacyFilesystemJournal struct {
	SchemaVersion  int              `json:"schema_version"`
	TransactionID  string           `json:"transaction_id"`
	Operation      Operation        `json:"operation"`
	Phase          Phase            `json:"phase"`
	Agent          domain.AgentID   `json:"agent"`
	Power          domain.PowerRef  `json:"power"`
	Previous       *domain.PowerRef `json:"previous,omitempty"`
	StagingRoot    string           `json:"staging_root,omitempty"`
	TargetPath     string           `json:"target_path"`
	TransactionDir string           `json:"transaction_dir"`
	BackupPath     string           `json:"backup_path,omitempty"`
	QuarantinePath string           `json:"quarantine_path,omitempty"`
}

func (legacy legacyFilesystemJournal) normalize() Journal {
	return Journal{
		SchemaVersion: 2,
		TransactionID: legacy.TransactionID,
		Driver:        DriverFilesystem,
		Operation:     legacy.Operation,
		Phase:         legacy.Phase,
		Agent:         legacy.Agent,
		Power:         legacy.Power,
		Previous:      clonePowerRef(legacy.Previous),
		Filesystem: &FilesystemJournalPayload{
			StagingRoot:    legacy.StagingRoot,
			TargetPath:     legacy.TargetPath,
			TransactionDir: legacy.TransactionDir,
			BackupPath:     legacy.BackupPath,
			QuarantinePath: legacy.QuarantinePath,
		},
	}
}
