package gorm

import (
	"errors"
	"strings"
)

var (
	// ErrNoRecordsInResultSetSQL sql native error on querying with .row() function or similar
	ErrNoRecordsInResultSetSQL = errors.New("sql: no rows in result set")
	// ErrRecordNotFound returns a "record not found error". Occurs only when attempting to query the database with a struct; querying with a slice won't return this error
	ErrRecordNotFound = errors.New("record not found")
	// ErrInvalidSQL occurs when you attempt a query with invalid SQL
	ErrInvalidSQL = errors.New("invalid SQL")
	// ErrInvalidTransaction occurs when you are trying to `Commit` or `Rollback`
	ErrInvalidTransaction = errors.New("no valid transaction")
	// ErrCantStartTransaction can't start transaction when you are trying to start one with `Begin`
	ErrCantStartTransaction = errors.New("can't start transaction")
	// ErrUnaddressable unaddressable value
	ErrUnaddressable = errors.New("using unaddressable value")
)

// Errors contains all happened errors
type Errors []error

// IsRecordNotFoundError returns true if error contains a RecordNotFound error
func IsRecordNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errs, ok := err.(Errors); ok {
		for _, err := range errs {
			if err.Error() == ErrRecordNotFound.Error() || err.Error() == ErrNoRecordsInResultSetSQL.Error() {
				return true
			}
		}
	}
	return err.Error() == ErrRecordNotFound.Error() || err.Error() == ErrNoRecordsInResultSetSQL.Error()
}

// GetErrors gets all errors that have occurred and returns a slice of errors (Error type)
func (errs Errors) GetErrors() []error {
	return errs
}

// Add adds an error to a given slice of errors
func (errs Errors) Add(newErrors ...error) Errors {
	for _, err := range newErrors {
		if err == nil {
			continue
		}

		if errors, ok := err.(Errors); ok {
			errs = errs.Add(errors...)
		} else {
			ok = true
			for _, e := range errs {
				if err == e {
					ok = false
				}
			}
			if ok {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

// Error takes a slice of all errors that have occurred and returns it as a formatted string
func (errs Errors) Error() string {
	var errors = []string{}
	for _, e := range errs {
		errors = append(errors, e.Error())
	}
	return strings.Join(errors, "; ")
}

// GormError is a custom error with the error and the SQL executed.
type GormError struct {
	Err error
	SQL string
}

// New is a construtor of custom error.
func NewGormError(err error, sql string) GormError {
	return GormError{err, sql}
}

// Error return the error message.
func (e GormError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	} else {
		return "unexpected error"
	}
}
