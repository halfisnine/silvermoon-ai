package model

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Credentials must stay out of SQL diagnostics even when global DEBUG enables
// parameter rendering. Keep the configured output and verbosity, and reuse the
// project's database error-code sanitizer before delegating to its logger.
type paidRedemptionLogger struct{ logger.Interface }

func (l paidRedemptionLogger) LogMode(level logger.LogLevel) logger.Interface {
	return paidRedemptionLogger{Interface: l.Interface.LogMode(level)}
}

func (l paidRedemptionLogger) ParamsFilter(_ context.Context, sql string, _ ...any) (string, []any) {
	return sql, nil
}

func (l paidRedemptionLogger) Trace(ctx context.Context, begin time.Time, query func() (string, int64), err error) {
	l.Interface.Trace(ctx, begin, query, sanitizeDBError(err))
}

func paidRedemptionDB(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{Logger: paidRedemptionLogger{Interface: db.Logger}})
}

// PaidRedemptionSpec is an internal contract. The management service builds
// Money from trusted site settings; an HTTP body cannot supply exchange rates.
type PaidRedemptionSpec struct {
	Name          string
	Money         MarketingMoneySnapshot
	PaidCents     int64
	SalesChannel  string
	SalesBatch    string
	SalesEvidence string
	ExpiredTime   int64
}

type PaidRedemptionResult struct {
	RedemptionID  int
	Quota         int
	PaymentID     int64
	AlreadyPosted bool
}

var (
	ErrPaidRedemptionSale = common.NewMessage("Paid redemption code sale details are required")
	ErrPaidRedemptionUsed = common.NewMessage("Used redemption codes cannot be changed")
)

// Database diagnostics can contain a conflicting live credential. Preserve
// typed causes for transaction retry without exposing their text to clients.
type paidRedemptionStorageError struct{ cause error }

func (e paidRedemptionStorageError) Error() string { return "Failed to save paid redemption codes" }
func (e paidRedemptionStorageError) Unwrap() error { return e.cause }

func validatePaidRedemptionSpec(spec PaidRedemptionSpec, now int64) (int, string, error) {
	if now <= 0 {
		return 0, "", ErrMarketingInput
	}
	if spec.PaidCents <= 0 || spec.SalesChannel == "" || spec.SalesBatch == "" {
		return 0, "", ErrPaidRedemptionSale
	}
	if spec.PaidCents > common.MaxWalletQuota {
		return 0, "", common.NewMessage("Paid redemption code sale amount is invalid")
	}
	if spec.ExpiredTime < 0 || (spec.ExpiredTime != 0 && spec.ExpiredTime < now) {
		return 0, "", common.NewMessage("Expiration time cannot be earlier than current time")
	}
	for _, field := range []struct {
		value    string
		min, max int
	}{{spec.Name, 1, 20}, {spec.SalesChannel, 1, 80}, {spec.SalesBatch, 1, 80}, {spec.SalesEvidence, 0, 1000}} {
		length := utf8.RuneCountInString(field.value)
		if !utf8.ValidString(field.value) || length < field.min || length > field.max || strings.TrimSpace(field.value) != field.value {
			return 0, "", common.NewMessage("Redemption sale details are invalid")
		}
	}
	quota, err := spec.Money.Quota()
	if err != nil {
		return 0, "", common.NewMessage("Redemption credit amount is invalid")
	}
	credited, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(quota))
	if err != nil {
		return 0, "", common.NewMessage("Redemption credit amount is invalid")
	}
	conversion, err := common.Marshal(spec.Money)
	return credited, string(conversion), err
}

func CreatePaidRedemptionBatch(tx *gorm.DB, actorID int, keys []string, spec PaidRedemptionSpec, now int64) ([]*Redemption, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	tx = paidRedemptionDB(tx)
	if len(keys) == 0 || len(keys) > 100 {
		return nil, ErrMarketingInput
	}
	quota, conversion, err := validatePaidRedemptionSpec(spec, now)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(keys))
	rows := make([]*Redemption, 0, len(keys))
	for _, key := range keys {
		if len(key) != 32 || strings.TrimSpace(key) != key || seen[key] {
			return nil, ErrMarketingInput
		}
		seen[key] = true
		if strings.Contains(spec.Name, key) || strings.Contains(spec.SalesChannel, key) || strings.Contains(spec.SalesBatch, key) || strings.Contains(spec.SalesEvidence, key) {
			return nil, common.NewMessage("Sale details must not contain redemption codes")
		}
		rows = append(rows, &Redemption{UserId: actorID, Key: key, Status: common.RedemptionCodeStatusEnabled, Name: spec.Name, Quota: quota, CreatedTime: now, ExpiredTime: spec.ExpiredTime, PaidCents: spec.PaidCents, CreditedConversion: conversion, SalesChannel: spec.SalesChannel, SalesBatch: spec.SalesBatch, SalesEvidence: spec.SalesEvidence})
	}
	if err := tx.Create(&rows).Error; err != nil {
		// A duplicate credential is never echoed in an error or audit log.
		return nil, paidRedemptionStorageError{cause: err}
	}
	return rows, nil
}

func UpdatePaidRedemption(tx *gorm.DB, actorID, codeID int, spec PaidRedemptionSpec, now int64) (*Redemption, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	tx = paidRedemptionDB(tx)
	quota, conversion, err := validatePaidRedemptionSpec(spec, now)
	if err != nil {
		return nil, err
	}
	if codeID <= 0 {
		return nil, ErrMarketingInput
	}
	var row Redemption
	if err := lockForUpdate(tx).First(&row, codeID).Error; err != nil {
		return nil, err
	}
	if row.Status == common.RedemptionCodeStatusUsed || row.UsedUserId != 0 || row.RedeemedTime != 0 || row.PaymentID != 0 {
		return nil, ErrPaidRedemptionUsed
	}
	if strings.Contains(spec.Name, row.Key) || strings.Contains(spec.SalesChannel, row.Key) || strings.Contains(spec.SalesBatch, row.Key) || strings.Contains(spec.SalesEvidence, row.Key) {
		return nil, common.NewMessage("Sale details must not contain redemption codes")
	}
	row.Name, row.Quota, row.ExpiredTime = spec.Name, quota, spec.ExpiredTime
	row.PaidCents, row.CreditedConversion = spec.PaidCents, conversion
	row.SalesChannel, row.SalesBatch, row.SalesEvidence = spec.SalesChannel, spec.SalesBatch, spec.SalesEvidence
	return &row, tx.Model(&row).Select("name", "quota", "expired_time", "paid_cents", "credited_conversion", "sales_channel", "sales_batch", "sales_evidence").Updates(&row).Error
}

func SetPaidRedemptionStatus(tx *gorm.DB, actorID, codeID, status int, now int64) (*Redemption, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	tx = paidRedemptionDB(tx)
	if codeID <= 0 || now <= 0 || (status != common.RedemptionCodeStatusEnabled && status != common.RedemptionCodeStatusDisabled) {
		return nil, ErrMarketingInput
	}
	var row Redemption
	if err := lockForUpdate(tx).First(&row, codeID).Error; err != nil {
		return nil, err
	}
	if row.Status == common.RedemptionCodeStatusUsed || row.UsedUserId != 0 || row.RedeemedTime != 0 || row.PaymentID != 0 {
		return nil, ErrPaidRedemptionUsed
	}
	row.Status = status
	return &row, tx.Model(&row).Update("status", status).Error
}

func DeletePaidRedemptions(tx *gorm.DB, actorID int, ids []int) (int64, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return 0, err
	}
	tx = paidRedemptionDB(tx)
	if len(ids) == 0 || len(ids) > 1000 {
		return 0, ErrMarketingInput
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	if ids[0] <= 0 {
		return 0, ErrMarketingInput
	}
	ids = slices.Compact(ids)
	var rows []Redemption
	if err := lockForUpdate(tx).Select("id").Where("id IN ?", ids).Order("id").Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Delete(&rows)
	return result.RowsAffected, result.Error
}

func DeleteInvalidPaidRedemptions(tx *gorm.DB, actorID int, now int64) (int64, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return 0, err
	}
	tx = paidRedemptionDB(tx)
	if now <= 0 {
		return 0, ErrMarketingInput
	}
	var rows []Redemption
	if err := lockForUpdate(tx).Select("id").Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Order("id").Find(&rows).Error; err != nil {
		return 0, err
	}
	var count int64
	for start := 0; start < len(rows); start += 500 {
		result := tx.Delete(rows[start:min(start+500, len(rows))])
		if result.Error != nil {
			return 0, result.Error
		}
		count += result.RowsAffected
	}
	return count, nil
}

func RedeemPaidCode(tx *gorm.DB, userID int, key string, now int64) (*PaidRedemptionResult, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	return redeemPaidCodeWithClock(tx, userID, key, func() int64 { return now })
}

func redeemPaidCodeWithClock(tx *gorm.DB, userID int, key string, clock func() int64) (*PaidRedemptionResult, error) {
	if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, err
	}
	tx = paidRedemptionDB(tx)
	if len(key) != 32 || clock == nil {
		return nil, ErrMarketingInput
	}
	if _, _, err := lockMarketingAccount(tx, userID); err != nil {
		return nil, err
	}
	var row Redemption
	if err := lockForUpdate(tx).Where(commonKeyCol+" = ?", key).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRedeemFailed
		}
		return nil, err
	}
	var money MarketingMoneySnapshot
	if row.PaidCents <= 0 || row.CreditedConversion == "" || row.SalesChannel == "" || row.SalesBatch == "" {
		return nil, ErrPaidRedemptionSale
	}
	if err := common.UnmarshalJsonStr(row.CreditedConversion, &money); err != nil {
		return nil, ErrMarketingConflict
	}
	quota, err := money.Quota()
	if err != nil || quota != int64(row.Quota) {
		return nil, ErrMarketingConflict
	}
	reference := "redemption:" + strconv.Itoa(row.Id)
	alreadyPosted := row.Status == common.RedemptionCodeStatusUsed || row.UsedUserId != 0 || row.RedeemedTime != 0 || row.PaymentID != 0
	if alreadyPosted {
		if row.Status != common.RedemptionCodeStatusUsed || row.UsedUserId != userID || row.RedeemedTime <= 0 || row.PaymentID <= 0 {
			return nil, ErrMarketingConflict
		}
		var prior MarketingPayment
		if err := lockForUpdate(tx).First(&prior, row.PaymentID).Error; err != nil {
			return nil, err
		}
		if prior.UserID != userID || prior.SourceKind != "paid_code" || prior.SourceReference != reference || prior.PaidCents != row.PaidCents || prior.CreditedQuota != quota || prior.PostedAt != row.RedeemedTime || prior.Conversion != row.CreditedConversion {
			return nil, ErrMarketingConflict
		}
		return &PaidRedemptionResult{RedemptionID: row.Id, Quota: row.Quota, PaymentID: prior.ID, AlreadyPosted: true}, nil
	}
	if row.Status != common.RedemptionCodeStatusEnabled {
		return nil, ErrRedeemFailed
	}
	// Expiry and posting share one fresh time after every potentially blocking
	// source lock. Whole-transaction retries call the clock again; replays do not.
	now := clock()
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if row.ExpiredTime != 0 && row.ExpiredTime < now {
		return nil, ErrRedeemFailed
	}
	result := tx.Model(&Redemption{}).Where("id = ? AND status = ? AND used_user_id = 0 AND redeemed_time = 0 AND payment_id = 0", row.Id, common.RedemptionCodeStatusEnabled).Updates(map[string]any{"status": common.RedemptionCodeStatusUsed, "used_user_id": userID, "redeemed_time": now})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrMarketingConflict
	}
	payment, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: userID, Kind: "paid_code", Reference: reference, PaidCents: row.PaidCents, PostedAt: now, Money: money})
	if err != nil {
		return nil, err
	}
	if err := tx.Model(&row).Update("payment_id", payment.ID).Error; err != nil {
		return nil, err
	}
	return &PaidRedemptionResult{RedemptionID: row.Id, Quota: row.Quota, PaymentID: payment.ID}, nil
}
