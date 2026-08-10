package transaction

// JournalStore 定义唯一活动事务日志的原子存储操作。
type JournalStore interface {
	Inspect() (Journal, bool, error)
	Create(Journal) error
	Update(Journal) error
	Delete(transactionID string) error
}
