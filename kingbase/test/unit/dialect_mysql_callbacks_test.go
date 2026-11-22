package kingbase_test

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	kb "github.com/jinzhu/gorm/kingbase/dialects"
)

func TestKingbaseAutoMySQLUsesMySQLCreateCallback(t *testing.T) {
	db, mock, close := openAutoMockGorm(t, "mysql")
	defer close()
	db.LogMode(false)
	type row struct {
		ID   int64 `gorm:"primary_key"`
		Name string
	}

	mock.ExpectBegin()
	mock.ExpectExec("^" + regexp.QuoteMeta("/* kingbase mysql insert ignore */ INSERT IGNORE INTO `events` (`name`) VALUES (?)") + "$").
		WithArgs("auto").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT last_insert_id()") + "$").
		WillReturnRows(sqlmock.NewRows([]string{"last_insert_id"}).AddRow(52))
	mock.ExpectCommit()

	created := row{Name: "auto"}
	if err := db.Table("events").Set("gorm:insert_modifier", "IGNORE").Create(&created).Error; err != nil || created.ID != 52 {
		t.Fatalf("auto MySQL callback: id=%d error=%v", created.ID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKingbaseMySQLInsertIgnoreRetainsErrorsAndRollsBack(t *testing.T) {
	for _, phase := range []string{"insert", "rows_affected", "id_lookup"} {
		t.Run(phase, func(t *testing.T) {
			db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
			defer close()
			db.LogMode(false)
			type row struct {
				ID   int64 `gorm:"primary_key"`
				Name string
			}
			failure := errors.New("original Kingbase failure")
			mock.ExpectBegin()
			insert := mock.ExpectExec("^" + regexp.QuoteMeta("/* kingbase mysql insert ignore */ INSERT IGNORE INTO `events` (`name`) VALUES (?)") + "$").WithArgs("a")
			if phase == "insert" {
				insert.WillReturnError(failure)
			} else if phase == "rows_affected" {
				insert.WillReturnResult(sqlmock.NewErrorResult(failure))
			} else {
				insert.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT last_insert_id()") + "$").WillReturnError(failure)
			}
			mock.ExpectRollback()
			if err := db.Table("events").Set("gorm:insert_modifier", "IGNORE").Create(&row{Name: "a"}).Error; err != failure {
				t.Fatalf("original error was lost: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKingbaseMySQLNormalCreateBackfillsNonFirstPrimaryKey(t *testing.T) {
	db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	db.LogMode(false)
	type row struct {
		Name string
		ID   int64 `gorm:"primary_key"`
	}

	mock.ExpectBegin()
	mock.ExpectExec("^" + regexp.QuoteMeta("INSERT INTO `events` (`name`) VALUES (?)") + "$").
		WithArgs("a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT last_insert_id()") + "$").
		WillReturnRows(sqlmock.NewRows([]string{"last_insert_id"}).AddRow(41))
	mock.ExpectCommit()

	created := row{Name: "a"}
	result := db.Table("events").Create(&created)
	if result.Error != nil || result.RowsAffected != 1 || created.ID != 41 {
		t.Fatalf("normal Create fallback: id=%d rows=%d error=%v", created.ID, result.RowsAffected, result.Error)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKingbaseMySQLNormalCreateRetainsFallbackError(t *testing.T) {
	db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	db.LogMode(false)
	type row struct {
		Name string
		ID   int64 `gorm:"primary_key"`
	}
	failure := errors.New("last_insert_id lookup failed")

	mock.ExpectBegin()
	mock.ExpectExec("^" + regexp.QuoteMeta("INSERT INTO `events` (`name`) VALUES (?)") + "$").
		WithArgs("a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT last_insert_id()") + "$").WillReturnError(failure)
	mock.ExpectRollback()

	created := row{Name: "a"}
	if err := db.Table("events").Create(&created).Error; err != failure {
		t.Fatalf("fallback error was lost: %v", err)
	}
	if created.ID != 0 {
		t.Fatalf("failed fallback changed primary key: %d", created.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKingbaseMySQLNormalCreateKeepsDriverLastInsertID(t *testing.T) {
	db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	db.LogMode(false)
	type row struct {
		ID   int64 `gorm:"primary_key"`
		Name string
	}

	mock.ExpectBegin()
	mock.ExpectExec("^" + regexp.QuoteMeta("INSERT INTO `events` (`name`) VALUES (?)") + "$").
		WithArgs("a").
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()

	created := row{Name: "a"}
	if err := db.Table("events").Create(&created).Error; err != nil || created.ID != 42 {
		t.Fatalf("driver LastInsertId path changed: id=%d error=%v", created.ID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
