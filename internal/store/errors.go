package store

import (
	"strings"
)

// isUniqueViolation 判断是否为唯一约束冲突（modernc.org/sqlite 驱动以错误文本暴露）。
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
