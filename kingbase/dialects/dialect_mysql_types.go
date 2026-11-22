package kingbase

import (
	"strings"

	"github.com/jinzhu/gorm"
)

// rewriteMysqlType keeps the MySQL definition emitted by GORM. KingbaseES R3
// supports the native MySQL unsigned integer families and AUTO_INCREMENT, so
// new schemas should not be widened to signed storage plus adapter CHECKs.
func (k *KingbaseDialect) rewriteMysqlType(f *gorm.StructField) string {
	k.ensureRaw()
	return strings.TrimSpace(k.raw.DataTypeOf(f))
}
