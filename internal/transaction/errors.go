package transaction

import "errors"

var (
	// ErrActiveTransaction 表示已有尚未完成的活动事务。
	ErrActiveTransaction = errors.New("active transaction exists")
	// ErrInvalidRequest 表示事务请求不符合调用契约。
	ErrInvalidRequest = errors.New("invalid transaction request")
	// ErrInvalidJournal 表示持久化事务日志无效。
	ErrInvalidJournal = errors.New("invalid transaction journal")
	// ErrUnsafePath 表示事务路径不满足安全约束。
	ErrUnsafePath = errors.New("unsafe transaction path")
	// ErrIndeterminateState 表示路径状态无法安全判定事务方向。
	ErrIndeterminateState = errors.New("indeterminate transaction state")
	// ErrRecoveryFailed 表示活动事务恢复失败。
	ErrRecoveryFailed = errors.New("transaction recovery failed")
)
