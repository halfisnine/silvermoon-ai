package model

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// OpenMarketingMaintenanceDatabase opens an existing database without startup,
// migrations, backfills, workers or setup mutations. The DSN is deployment-owned.
func OpenMarketingMaintenanceDatabase() error {
	dsn := os.Getenv("SQL_DSN")
	if dsn == "" || strings.HasPrefix(dsn, "local") {
		filename := strings.TrimPrefix(common.SQLitePath, "file:")
		filename, _, _ = strings.Cut(filename, "?")
		decoded, err := url.PathUnescape(filename)
		if err != nil {
			return fmt.Errorf("invalid existing SQLite path")
		}
		info, err := os.Stat(decoded)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("maintenance requires an existing SQLite database file")
		}
		parsed, err := url.Parse("file:" + decoded)
		if err != nil {
			return err
		}
		_, query, _ := strings.Cut(common.SQLitePath, "?")
		values, err := url.ParseQuery(query)
		if err != nil {
			return err
		}
		values.Set("mode", "rw")
		parsed.RawQuery = values.Encode()
		common.SQLitePath = parsed.String()
	}
	db, kind, err := chooseDB("SQL_DSN", false)
	if err != nil {
		return fmt.Errorf("cannot open maintenance database")
	}
	DB = db
	common.SetMainDatabaseType(kind)
	common.SetLogDatabaseType(kind)
	initCol()
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(1)
	// Only read the conversion options needed by human-facing amount inputs.
	keys := []string{"QuotaPerUnit", "USDExchangeRate", "general_setting.quota_display_type", "general_setting.custom_currency_symbol", "general_setting.custom_currency_exchange_rate"}
	var options []Option
	if err := db.Where(commonKeyCol+" IN ?", keys).Find(&options).Error; err != nil {
		return err
	}
	common.OptionMap = make(map[string]string)
	for _, option := range options {
		if err := updateOptionMap(option.Key, option.Value); err != nil {
			return err
		}
	}
	return nil
}

// RefreshMarketingMaintenanceCaches runs only after the financial transaction.
// Retry is safe; a cache failure must not replay or roll back committed funds.
func RefreshMarketingMaintenanceCaches(userID int) error {
	return errors.Join(invalidateUserCache(userID), InvalidateUserTokensCache(userID))
}
