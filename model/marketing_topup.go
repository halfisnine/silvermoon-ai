package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var (
	ErrEpaySnapshot = common.NewMessage("This order has no verified payment snapshot")
	ErrEpayReceipt  = common.NewMessage("Payment amount or merchant does not match the order")
)

// EpayTopUpReceipt is only constructed after the caller verifies the provider
// signature and successful payment status. It is not a browser request schema.
type EpayTopUpReceipt struct {
	PaidCents                                    int64
	MerchantID, PaymentMethod, ProviderReference string
}

type EpayTopUpResult struct {
	UserID        int
	Quota         int
	PaymentID     int64
	AlreadyPosted bool
	PaymentMethod string
	PaidCents     int64
}

// FreezeEpayTopUp preserves the checkout's original credit calculation. Actual
// CNY paid is separate; neither is reconstructed from the other at callback time.
func (topUp *TopUp) FreezeEpayTopUp(money MarketingMoneySnapshot, paidCents int64, merchantID string) error {
	if topUp == nil || topUp.Id != 0 || topUp.UserId <= 0 || topUp.Amount <= 0 || topUp.CreateTime <= 0 || topUp.Status != common.TopUpStatusPending || topUp.PaymentProvider != PaymentProviderEpay || topUp.PaymentID != 0 || topUp.CompleteTime != 0 || len(topUp.TradeNo) == 0 || len(topUp.TradeNo) > 255 || strings.TrimSpace(topUp.TradeNo) != topUp.TradeNo {
		return ErrTopUpStatusInvalid
	}
	data, err := common.Marshal(money)
	if err != nil {
		return err
	}
	topUp.CreditedConversion = string(data)
	topUp.ExpectedPaidCents = paidCents
	topUp.MerchantID = merchantID
	if _, _, err := validateEpayTopUpSnapshot(topUp); err != nil {
		return err
	}
	// Money is retained solely for existing order display. Authoritative checks
	// and rewards use the integer ExpectedPaidCents, never this float.
	topUp.Money = decimal.NewFromInt(paidCents).Shift(-2).InexactFloat64()
	return nil
}

func validateEpayTopUpSnapshot(order *TopUp) (MarketingMoneySnapshot, int, error) {
	var money MarketingMoneySnapshot
	if order.ExpectedPaidCents <= 0 || order.ExpectedPaidCents > common.MaxWalletQuota || order.CreditedConversion == "" || len(order.MerchantID) == 0 || len(order.MerchantID) > 100 || strings.TrimSpace(order.MerchantID) != order.MerchantID {
		return money, 0, ErrEpaySnapshot
	}
	if err := common.UnmarshalJsonStr(order.CreditedConversion, &money); err != nil {
		return money, 0, ErrEpaySnapshot
	}
	quota, err := money.Quota()
	if err != nil {
		return money, 0, ErrInvalidTopUpQuota
	}
	// Epay's existing Amount is the normalized base-unit quantity. Preserve
	// Amount * checkout QuotaPerUnit, not the site's display-currency rate.
	amountUnits, err := decimal.NewFromString(money.Amount)
	if err != nil || order.Amount <= 0 || !amountUnits.Equal(decimal.NewFromInt(order.Amount)) || money.Currency != "USD" {
		return money, 0, ErrEpaySnapshot
	}
	currencyUnits, err := decimal.NewFromString(money.CurrencyPerUnit)
	if err != nil || !currencyUnits.Equal(decimal.NewFromInt(1)) {
		return money, 0, ErrEpaySnapshot
	}
	amount, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(quota))
	if err != nil {
		return money, 0, ErrInvalidTopUpQuota
	}
	return money, amount, nil
}

// CreditEpayTopUp commits only verified receipts. The caller owns the complete
// transaction; no cache writes, logs or external work happen before it commits.
func CreditEpayTopUp(tx *gorm.DB, tradeNo string, receipt EpayTopUpReceipt, now int64) (*EpayTopUpResult, error) {
	return completeEpayTopUp(tx, tradeNo, receipt, 0, "", now)
}

func completeEpayTopUp(tx *gorm.DB, tradeNo string, receipt EpayTopUpReceipt, actorID int, evidence string, now int64) (*EpayTopUpResult, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	return completeEpayTopUpWithClock(tx, tradeNo, receipt, actorID, evidence, func() int64 { return now })
}

func completeEpayTopUpWithClock(tx *gorm.DB, tradeNo string, receipt EpayTopUpReceipt, actorID int, evidence string, clock func() int64) (*EpayTopUpResult, error) {
	if err := lockMarketingActivityUsers(tx, nil); err != nil {
		return nil, err
	}
	if len(tradeNo) == 0 || len(tradeNo) > 255 || strings.TrimSpace(tradeNo) != tradeNo || clock == nil || receipt.PaidCents <= 0 || receipt.PaidCents > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	if actorID < 0 {
		return nil, ErrMarketingInput
	}
	if actorID == 0 {
		if len(receipt.MerchantID) == 0 || len(receipt.MerchantID) > 100 || strings.TrimSpace(receipt.MerchantID) != receipt.MerchantID || len(receipt.ProviderReference) == 0 || len(receipt.ProviderReference) > 255 || strings.TrimSpace(receipt.ProviderReference) != receipt.ProviderReference || len(receipt.PaymentMethod) == 0 || len(receipt.PaymentMethod) > 50 {
			return nil, ErrEpayReceipt
		}
	} else if strings.TrimSpace(evidence) == "" || len(evidence) > 4000 || !utf8.ValidString(evidence) {
		return nil, common.NewMessage("Confirmed CNY payment evidence is required for manual completion")
	}
	// Only immutable identity is read before taking user locks. Every mutable
	// order field is reread under lock after users/account, never order -> user.
	var identity TopUp
	if err := tx.Select("id", "user_id").Where("trade_no = ?", tradeNo).First(&identity).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTopUpNotFound
		}
		return nil, err
	}
	users := []int{identity.UserId}
	if actorID > 0 {
		users = append(users, actorID)
	}
	if err := lockMarketingActivityUsers(tx, users); err != nil {
		return nil, err
	}
	if actorID > 0 {
		if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
			return nil, err
		}
	}
	if _, _, err := lockMarketingAccount(tx, identity.UserId); err != nil {
		return nil, err
	}
	var order TopUp
	if err := lockForUpdate(tx).First(&order, identity.Id).Error; err != nil {
		return nil, err
	}
	if order.UserId != identity.UserId || order.TradeNo != tradeNo {
		return nil, ErrMarketingConflict
	}
	if order.PaymentProvider != PaymentProviderEpay {
		return nil, ErrPaymentMethodMismatch
	}
	money, quota, err := validateEpayTopUpSnapshot(&order)
	if err != nil {
		return nil, err
	}
	if receipt.PaidCents != order.ExpectedPaidCents || (actorID == 0 && receipt.MerchantID != order.MerchantID) {
		return nil, ErrEpayReceipt
	}
	if actorID == 0 && order.ProviderReference != "" && order.ProviderReference != receipt.ProviderReference {
		return nil, ErrEpayReceipt
	}
	result := &EpayTopUpResult{UserID: order.UserId, Quota: quota, PaymentID: order.PaymentID, PaymentMethod: order.PaymentMethod, PaidCents: order.ExpectedPaidCents}
	reference := "topup:" + strconv.Itoa(order.Id)
	if order.Status == common.TopUpStatusSuccess {
		if order.PaymentID <= 0 || order.CompleteTime <= 0 {
			return nil, ErrEpaySnapshot
		}
		var payment MarketingPayment
		if err := lockForUpdate(tx).First(&payment, order.PaymentID).Error; err != nil {
			return nil, err
		}
		if payment.UserID != order.UserId || payment.SourceKind != "online" || payment.SourceReference != reference || payment.PaidCents != order.ExpectedPaidCents || payment.CreditedQuota != int64(quota) || payment.PostedAt != order.CompleteTime || payment.Conversion != order.CreditedConversion {
			return nil, ErrMarketingConflict
		}
		if actorID == 0 && order.ProviderReference == "" {
			if err := tx.Model(&order).Update("provider_reference", receipt.ProviderReference).Error; err != nil {
				return nil, err
			}
		}
		result.AlreadyPosted = true
		return result, nil
	}
	if order.Status != common.TopUpStatusPending || order.PaymentID != 0 || order.CompleteTime != 0 {
		return nil, ErrTopUpStatusInvalid
	}
	var user User
	if err := lockForUpdate(tx).First(&user, order.UserId).Error; err != nil {
		return nil, err
	}
	if int64(user.Quota) < -common.MaxWalletQuota || user.Quota > common.MaxWalletQuota-quota {
		return nil, ErrTopUpQuotaLimitExceeded
	}
	// A request can wait on another instance's user/account/order locks. Its
	// successful posting time must not be the time it entered the transaction.
	now := clock()
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	payment, err := CreditMarketingPayment(tx, MarketingPaymentInput{UserID: order.UserId, Kind: "online", Reference: reference, PaidCents: order.ExpectedPaidCents, PostedAt: now, Money: money})
	if err != nil {
		return nil, err
	}
	updates := map[string]any{"status": common.TopUpStatusSuccess, "complete_time": now, "payment_id": payment.ID, "completion_actor_id": actorID, "completion_evidence": evidence}
	if actorID == 0 {
		updates["provider_reference"] = receipt.ProviderReference
		updates["payment_method"] = receipt.PaymentMethod
		result.PaymentMethod = receipt.PaymentMethod
	}
	changed := tx.Model(&TopUp{}).Where("id = ? AND status = ? AND payment_id = 0", order.Id, common.TopUpStatusPending).Updates(updates)
	if changed.Error != nil {
		return nil, changed.Error
	}
	if changed.RowsAffected != 1 {
		return nil, ErrMarketingConflict
	}
	result.PaymentID = payment.ID
	return result, nil
}

func RechargeEpay(tradeNo string, receipt EpayTopUpReceipt, callerIP string) (bool, error) {
	var result *EpayTopUpResult
	err := WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		result, err = completeEpayTopUpWithClock(tx, tradeNo, receipt, 0, "", common.GetTimestamp)
		return err
	})
	if err != nil {
		return false, err
	}
	if result.AlreadyPosted {
		return true, nil
	}
	syncCreditUserQuotaCache(result.UserID, result.Quota, common.LogText("epay topup"))
	RecordTopupLog(result.UserID, common.NewMessage("Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"quota": logger.FormatQuota(result.Quota), "amount": decimal.NewFromInt(result.PaidCents).Shift(-2).StringFixed(2)}), callerIP, result.PaymentMethod, PaymentProviderEpay)
	return false, nil
}

func ManualCompleteTopUp(actorID int, tradeNo string, paidCents int64, evidence string, callerIP string) error {
	if actorID <= 0 {
		return ErrMarketingAccess
	}
	var result *EpayTopUpResult
	err := WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		result, err = completeEpayTopUpWithClock(tx, tradeNo, EpayTopUpReceipt{PaidCents: paidCents}, actorID, evidence, common.GetTimestamp)
		return err
	})
	if err != nil {
		return err
	}
	if result.AlreadyPosted {
		return nil
	}
	syncCreditUserQuotaCache(result.UserID, result.Quota, common.LogText("manual topup"))
	RecordTopupLog(result.UserID, common.NewMessage("Administrator completed the order, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"quota": logger.FormatQuota(result.Quota), "amount": decimal.NewFromInt(result.PaidCents).Shift(-2).StringFixed(2)}), callerIP, result.PaymentMethod, "admin")
	return nil
}
