package transaction

// JournalStore 定义唯一活动事务日志的原子存储操作；Inspect 必须严格解码并校验，Service 仍会校验注入的替代 Store 返回值。
type JournalStore interface {
	Inspect() (Journal, bool, error)
	Create(Journal) error
	Update(Journal) error
	Delete(transactionID string) error
}
