package transaction

// JournalStore 定义事务日志的存储操作。
type JournalStore interface {
	Inspect() (Journal, error)
	Write(Journal) error
	Delete() error
}
