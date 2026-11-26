package kingbase

import (
	"strings"

	"github.com/jinzhu/gorm"
)

// rewriteMysqlType 保留 GORM 生成的 MySQL 类型定义。KingbaseES R3 原生支持 MySQL
// 无符号整数族和 AUTO_INCREMENT，因此无需扩展成有符号存储再额外增加 CHECK 约束。
func (k *KingbaseDialect) rewriteMysqlType(f *gorm.StructField) string {
	k.ensureRaw()
	return strings.TrimSpace(k.raw.DataTypeOf(f))
}
