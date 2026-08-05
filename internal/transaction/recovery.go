package transaction

import "context"

// Recovery 表示事务恢复器。
type Recovery struct{}

// Recover 恢复指定事务日志对应的状态。
func (r *Recovery) Recover(context.Context, Journal) error {
	return nil
}
