package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// WithMarketingTransaction retries the whole rolled-back transaction only for
// SQLite lock contention. Callers must use stable business keys and keep network
// calls outside fn. An ambiguous commit or business error is never retried here.
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
		if err == nil || !common.UsingMainDatabase(common.DatabaseTypeSQLite) || attempt == 3 {
			return err
		}
		var sqliteError interface{ Code() int }
		if !errors.As(err, &sqliteError) || (sqliteError.Code()&0xff != 5 && sqliteError.Code()&0xff != 6) {
			return err
		}
		// BUSY (5), including BUSY_SNAPSHOT, and LOCKED (6) mean the
		// transaction could not complete. Never retry only its final write.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * 5 * time.Millisecond):
		}
	}
	return ErrMarketingConflict
}
