package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// WithMarketingTransaction retries whole transactions after SQLite lock
// contention or a confirmed MySQL deadlock rollback. Callers must use stable
// business keys and keep network calls outside fn. An ambiguous commit, lock
// timeout, connection failure or business error is never retried here.
func WithMarketingTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	if DB == nil {
		return ErrMarketingTransaction
	}
	if ctx == nil || fn == nil {
		return ErrMarketingInput
	}
	for attempt := range 4 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := DB.WithContext(ctx).Transaction(fn)
		if err == nil || attempt == 3 {
			return err
		}
		retry := false
		switch common.MainDatabaseType() {
		case common.DatabaseTypeSQLite:
			var sqliteError interface{ Code() int }
			retry = errors.As(err, &sqliteError) && (sqliteError.Code()&0xff == 5 || sqliteError.Code()&0xff == 6)
		case common.DatabaseTypeMySQL:
			var mysqlError *mysql.MySQLError
			// InnoDB rolls back the complete victim transaction for 1213.
			// 1205 may roll back only the waiting statement, so it is excluded.
			retry = errors.As(err, &mysqlError) && mysqlError.Number == 1213
		}
		if !retry {
			return err
		}
		// Retry all reads and writes together, never only the failed statement.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * 5 * time.Millisecond):
		}
	}
	return ErrMarketingConflict
}
