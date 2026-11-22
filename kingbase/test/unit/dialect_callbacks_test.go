package kingbase_test

import (
	"errors"
	"regexp"
	"testing"
	"time"

	kb "github.com/jinzhu/gorm/kingbase/dialects"
)

func TestKingbaseDerivedCountKeepsFiltersAndOriginalError(t *testing.T) {
	for _, dialect := range []string{kb.DialectKingbaseMySQL, kb.DialectKingbaseOracle} {
		t.Run(dialect, func(t *testing.T) {
			db, mock, close := openMockGorm(t, dialect)
			defer close()
			db.LogMode(false)
			type row struct {
				ID        int64
				Category  string
				DeletedAt *time.Time
			}
			failure := errors.New("database rejected grouped count")
			table := db.Dialect().Quote("events")
			deleted := db.Dialect().Quote("deleted_at")
			query := "^" + regexp.QuoteMeta("SELECT count(*) FROM ( SELECT category FROM "+table) + ".*" + regexp.QuoteMeta(table+"."+deleted+" IS NULL") + ".*" + regexp.QuoteMeta("GROUP BY category HAVING (COUNT(*) > ?) ) AS count_table") + "$"
			mock.ExpectQuery(query).WithArgs("a", 1).WillReturnError(failure)
			var count int
			err := db.Table("events").Model(&row{}).Where("category = ?", "a").Select("category").Group("category").Having("COUNT(*) > ?", 1).Count(&count).Error
			if err != failure {
				t.Fatalf("original count error was lost: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
