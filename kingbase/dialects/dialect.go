package kingbase

import (
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
)

// KbMode defines compatibility mode for Kingbase.
type KbMode string

const (
	KbModeMySQL  KbMode = "mysql"
	KbModeOracle KbMode = "oracle"
)

const DialectKingbase = "kingbase"

func NewKingbaseDialect(mode KbMode) gorm.Dialect {
	base := KingbaseDialect{mode: mode}
	switch mode {
	case KbModeMySQL:
		return &kingbaseMySQLDialect{KingbaseDialect: base}
	case KbModeOracle:
		return &kingbaseOracleDialect{}
	default:
		panic(fmt.Sprintf("unsupported Kingbase mode: %s", mode))
	}
}

// kingbaseAutoDialect is the public KingbaseES entry point. It detects the
// connected instance mode once during gorm.Open and then delegates every
// dialect operation to the mode-specific implementation.
type kingbaseAutoDialect struct {
	mode     KbMode
	delegate gorm.Dialect
	initErr  error
}

var _ gorm.Dialect = (*kingbaseAutoDialect)(nil)

func (d *kingbaseAutoDialect) GetName() string {
	if d.delegate != nil {
		return d.delegate.GetName()
	}
	return DialectKingbase
}

func (d *kingbaseAutoDialect) SetDB(db gorm.SQLCommon) {
	d.mode = ""
	d.delegate = nil
	d.initErr = nil
	if db == nil {
		d.initErr = fmt.Errorf("kingbase: database connection is nil")
		return
	}

	var databaseMode string
	if err := db.QueryRow("SHOW database_mode").Scan(&databaseMode); err != nil {
		d.initErr = fmt.Errorf("kingbase: read database_mode: %w", err)
		return
	}

	switch strings.ToLower(strings.TrimSpace(databaseMode)) {
	case string(KbModeMySQL):
		d.mode = KbModeMySQL
	case string(KbModeOracle):
		d.mode = KbModeOracle
	default:
		d.initErr = fmt.Errorf("kingbase: unsupported database_mode %q", databaseMode)
		return
	}

	d.delegate = NewKingbaseDialect(d.mode)
	d.delegate.SetDB(db)
}

// DialectInitializationError exposes a mode-detection failure without adding
// an initialization hook to the original GORM v1 Open implementation.
func (d *kingbaseAutoDialect) DialectInitializationError() error { return d.initErr }

func (d *kingbaseAutoDialect) selected() gorm.Dialect {
	if d.delegate != nil {
		return d.delegate
	}
	if d.initErr != nil {
		panic(d.initErr)
	}
	panic("kingbase: database mode has not been initialized")
}

func (d *kingbaseAutoDialect) BindVar(i int) string { return d.selected().BindVar(i) }

func (d *kingbaseAutoDialect) Quote(key string) string { return d.selected().Quote(key) }

func (d *kingbaseAutoDialect) DataTypeOf(field *gorm.StructField) string {
	return d.selected().DataTypeOf(field)
}

func (d *kingbaseAutoDialect) HasIndex(tableName, indexName string) bool {
	return d.selected().HasIndex(tableName, indexName)
}

func (d *kingbaseAutoDialect) HasForeignKey(tableName, foreignKeyName string) bool {
	return d.selected().HasForeignKey(tableName, foreignKeyName)
}

func (d *kingbaseAutoDialect) RemoveIndex(tableName, indexName string) error {
	return d.selected().RemoveIndex(tableName, indexName)
}

func (d *kingbaseAutoDialect) HasTable(tableName string) bool {
	return d.selected().HasTable(tableName)
}

func (d *kingbaseAutoDialect) HasColumn(tableName, columnName string) bool {
	return d.selected().HasColumn(tableName, columnName)
}

func (d *kingbaseAutoDialect) ModifyColumn(tableName, columnName, typ string) error {
	return d.selected().ModifyColumn(tableName, columnName, typ)
}

func (d *kingbaseAutoDialect) LimitAndOffsetSQL(limit, offset interface{}) (string, error) {
	return d.selected().LimitAndOffsetSQL(limit, offset)
}

func (d *kingbaseAutoDialect) SelectFromDummyTable() string {
	return d.selected().SelectFromDummyTable()
}

func (d *kingbaseAutoDialect) LastInsertIDOutputInterstitial(tableName, columnName string, columns []string) string {
	return d.selected().LastInsertIDOutputInterstitial(tableName, columnName, columns)
}

func (d *kingbaseAutoDialect) LastInsertIDReturningSuffix(tableName, columnName string) string {
	return d.selected().LastInsertIDReturningSuffix(tableName, columnName)
}

func (d *kingbaseAutoDialect) DefaultValueStr() string {
	return d.selected().DefaultValueStr()
}

func (d *kingbaseAutoDialect) BuildKeyName(kind, tableName string, fields ...string) string {
	return d.selected().BuildKeyName(kind, tableName, fields...)
}

func (d *kingbaseAutoDialect) NormalizeIndexAndColumn(indexName, columnName string) (string, string) {
	return d.selected().NormalizeIndexAndColumn(indexName, columnName)
}

func (d *kingbaseAutoDialect) CurrentDatabase() string {
	return d.selected().CurrentDatabase()
}

func Register() {
	gorm.RegisterDialect(DialectKingbase, &kingbaseAutoDialect{})
	registerKingbaseModeDialects()
	registerMySQLCallbacks()
	registerQueryCallbacks()
}

func init() {
	Register()
}

func cleanIdentifier(name string) string {
	name = strings.TrimSpace(name)
	if len(name) >= 2 {
		switch {
		case name[0] == '"' && name[len(name)-1] == '"':
			return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
		case name[0] == '`' && name[len(name)-1] == '`':
			return strings.ReplaceAll(name[1:len(name)-1], "``", "`")
		case name[0] == '[' && name[len(name)-1] == ']':
			return strings.ReplaceAll(name[1:len(name)-1], "]]", "]")
		}
	}
	return name
}

func splitSchemaTable(table string) (schema, name string) {
	table = strings.TrimSpace(table)
	var quote byte
	componentStart := true
	for i := 0; i < len(table); i++ {
		c := table[i]
		if quote != 0 {
			if c == quote {
				if i+1 < len(table) && table[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if componentStart && (c == '`' || c == '"' || c == '[') {
			quote = c
			if c == '[' {
				quote = ']'
			}
			componentStart = false
			continue
		}
		if c == '.' {
			return cleanIdentifier(table[:i]), cleanIdentifier(table[i+1:])
		}
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			componentStart = false
		}
	}
	return "", cleanIdentifier(table)
}
