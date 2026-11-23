package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// MarketingRewardAmount is internal calculation evidence, not a display/API
// amount. Money is the original payment conversion for paid_ratio, and the
// frozen CNY conversion for cash_ratio. Quota is rounded exactly once.
type MarketingRewardAmount struct {
	Calculation   string
	Ratio         string
	Money         MarketingMoneySnapshot
	PaymentID     int64
	PaidCents     int64
	CreditedQuota int64
	Quota         int64
}

func CalculateMarketingRewardAmount(mode, ratio string, money MarketingMoneySnapshot, payment *MarketingPayment) (MarketingRewardAmount, error) {
	result := MarketingRewardAmount{Calculation: mode, Money: money}
	if mode == "fixed" {
		if ratio != "" {
			return result, ErrMarketingInput
		}
		quota, err := money.Quota()
		result.Quota = quota
		return result, err
	}
	if mode != "paid_ratio" && mode != "cash_ratio" {
		return result, ErrMarketingInput
	}
	if !marketingDecimal.MatchString(ratio) || payment == nil || payment.ID <= 0 || payment.PaidCents <= 0 || payment.PaidCents > common.MaxWalletQuota || payment.CreditedQuota <= 0 || payment.CreditedQuota > common.MaxWalletQuota {
		return result, ErrMarketingInput
	}
	proportion, err := decimal.NewFromString(ratio)
	if err != nil || !proportion.IsPositive() {
		return result, ErrMarketingInput
	}
	result.Ratio = ratio
	result.PaymentID = payment.ID
	result.PaidCents = payment.PaidCents
	result.CreditedQuota = payment.CreditedQuota
	var amount decimal.Decimal
	if mode == "paid_ratio" {
		if err := common.UnmarshalJsonStr(payment.Conversion, &result.Money); err != nil {
			return result, ErrMarketingInput
		}
		base, err := result.Money.Quota()
		if err != nil || base != payment.CreditedQuota {
			return result, ErrMarketingInput
		}
		amount = decimal.NewFromInt(payment.CreditedQuota).Mul(proportion)
	} else {
		// Actual CNY and credited wallet money are distinct. A display currency
		// rate (or recharge purchase price) cannot substitute for this CNY rate.
		if money.Currency != "CNY" || !marketingDecimal.MatchString(money.QuotaPerUnit) || !marketingDecimal.MatchString(money.CurrencyPerUnit) {
			return result, ErrMarketingInput
		}
		quotaRate, err := decimal.NewFromString(money.QuotaPerUnit)
		if err != nil || !quotaRate.IsPositive() {
			return result, ErrMarketingInput
		}
		cnyRate, err := decimal.NewFromString(money.CurrencyPerUnit)
		if err != nil || !cnyRate.IsPositive() {
			return result, ErrMarketingInput
		}
		result.Money.Amount = "1" // Unit conversion evidence, not the reward face value.
		amount = decimal.NewFromInt(payment.PaidCents).Shift(-2).Mul(proportion).Mul(quotaRate).DivRound(cnyRate, 32)
	}
	amount = amount.Round(0)
	if amount.IsNegative() || amount.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return result, ErrMarketingInput
	}
	quota, err := common.WalletQuotaFromDecimalStrict(amount)
	if err != nil {
		return result, ErrMarketingInput
	}
	result.Quota = int64(quota)
	return result, nil
}

// GrantMarketingRewardGift revalidates trusted, persisted calculation evidence before
// crediting. It never converts quota back into a rounded display amount and then
// converts that display value again. A zero rounded result stays an activity
// outcome, not a zero-valued gift or paid-wallet credit. This is an internal
// delivery entry: callers load evidence from a captured real payment/award,
// never bind this structure from a browser request.
func GrantMarketingRewardGift(tx *gorm.DB, input MarketingGiftInput, amount MarketingRewardAmount) (*MarketingGift, error) {
	conversion, err := common.Marshal(amount.Money)
	if err != nil {
		return nil, err
	}
	payment := &MarketingPayment{ID: amount.PaymentID, PaidCents: amount.PaidCents, CreditedQuota: amount.CreditedQuota, Conversion: string(conversion)}
	verified, err := CalculateMarketingRewardAmount(amount.Calculation, amount.Ratio, amount.Money, payment)
	if err != nil || verified != amount || amount.Quota <= 0 {
		return nil, ErrMarketingInput
	}
	if (amount.Calculation == "paid_ratio" && input.Kind != "recharge") || (amount.Calculation == "cash_ratio" && input.Kind != "invitation") {
		return nil, ErrMarketingInput
	}
	input.Money = amount.Money
	// Fixed gifts retain the existing digest and conversion contract.
	if amount.Calculation == "fixed" {
		return GrantMarketingGift(tx, input)
	}
	evidence := struct {
		Input  MarketingGiftInput
		Amount MarketingRewardAmount
	}{input, amount}
	return grantMarketingGift(tx, input, amount.Quota, evidence, amount)
}
