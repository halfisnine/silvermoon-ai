package service

import (
	"regexp"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
)

var marketingPaidCNYPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,13})(\.[0-9]{1,2})?$`)

// ParseMarketingPaidCNY reads actual sale proceeds, independently of wallet
// display settings. Fractional cents are rejected, never silently rounded.
func ParseMarketingPaidCNY(amount string) (int64, error) {
	if !marketingPaidCNYPattern.MatchString(amount) {
		return 0, common.NewMessage("Enter a positive payment amount in CNY with up to two decimal places")
	}
	value, err := decimal.NewFromString(amount)
	if err != nil || !value.IsPositive() {
		return 0, common.NewMessage("Enter a positive payment amount in CNY with up to two decimal places")
	}
	cents := value.Shift(2)
	if cents.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return 0, common.NewMessage("Payment amount is too large")
	}
	return cents.IntPart(), nil
}

// MarketingDisplayMoney freezes trusted site settings with the human-entered
// wallet amount. The browser cannot supply its own quota conversion. TOKENS
// mode uses currency for marketing forms too, rather than exposing raw quota.
func MarketingDisplayMoney(amount string) (model.MarketingMoneySnapshot, error) {
	settings := *operation_setting.GetGeneralSetting()
	currency, rate := "USD", 1.0
	switch settings.QuotaDisplayType {
	case operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeTokens:
	case operation_setting.QuotaDisplayTypeCNY:
		currency, rate = "CNY", operation_setting.USDExchangeRate
	case operation_setting.QuotaDisplayTypeCustom:
		currency, rate = settings.CustomCurrencySymbol, settings.CustomCurrencyExchangeRate
	default:
		return model.MarketingMoneySnapshot{}, common.NewMessage("Invalid wallet currency settings")
	}
	money := model.MarketingMoneySnapshot{
		Amount: amount, Currency: currency,
		QuotaPerUnit:    strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64),
		CurrencyPerUnit: strconv.FormatFloat(rate, 'f', -1, 64),
	}
	if _, err := money.Quota(); err != nil {
		return model.MarketingMoneySnapshot{}, common.NewMessage("Invalid wallet amount or currency settings")
	}
	return money, nil
}

// MarketingWalletDisplayAmount projects stored funds through current site
// currency settings. HTTP responses must not expose the storage-unit ratio.
func MarketingWalletDisplayAmount(quota int64) (amount, currency string, err error) {
	if quota < 0 || quota > common.MaxWalletQuota {
		return "", "", model.ErrMarketingInput
	}
	money, err := MarketingDisplayMoney("1")
	if err != nil {
		return "", "", err
	}
	units, err := decimal.NewFromString(money.QuotaPerUnit)
	if err != nil {
		return "", "", err
	}
	rate, err := decimal.NewFromString(money.CurrencyPerUnit)
	if err != nil {
		return "", "", err
	}
	return decimal.NewFromInt(quota).Mul(rate).DivRound(units, 12).String(), money.Currency, nil
}
