package controller

import (
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// paidRedemptionView keeps storage units and internal funding IDs out of the
// administration contract. The recorded sale remains independent of display settings.
type paidRedemptionView struct {
	ID                     int    `json:"id"`
	Name                   string `json:"name"`
	Key                    string `json:"key"`
	Status                 int    `json:"status"`
	CreatedTime            int64  `json:"created_time"`
	RedeemedTime           int64  `json:"redeemed_time"`
	ExpiredTime            int64  `json:"expired_time"`
	Redeemed               bool   `json:"redeemed"`
	UsedUserName           string `json:"used_user_name"`
	PaidAmountCNY          string `json:"paid_amount_cny"`
	CreditAmount           string `json:"credit_amount"`
	CreditCurrency         string `json:"credit_currency"`
	MoneyRevision          string `json:"money_revision"`
	OriginalCreditAmount   string `json:"original_credit_amount"`
	OriginalCreditCurrency string `json:"original_credit_currency"`
	SalesChannel           string `json:"sales_channel"`
	SalesBatch             string `json:"sales_batch"`
	SalesEvidence          string `json:"sales_evidence"`
	SaleRegistered         bool   `json:"sale_registered"`
}

func paidRedemptionViews(rows []*model.Redemption) ([]paidRedemptionView, error) {
	moneyContext, err := service.CurrentMarketingMoneyContext()
	if err != nil {
		return nil, err
	}
	units, _ := decimal.NewFromString(moneyContext.Display.QuotaPerUnit)
	rate, _ := decimal.NewFromString(moneyContext.Display.CurrencyPerUnit)
	views := make([]paidRedemptionView, 0, len(rows))
	userIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.UsedUserId > 0 {
			userIDs = append(userIDs, row.UsedUserId)
		}
	}
	users := make(map[int]string)
	if len(userIDs) > 0 {
		var names []model.User
		if err := model.DB.Select("id", "username").Where("id IN ?", userIDs).Find(&names).Error; err != nil {
			return nil, err
		}
		for _, user := range names {
			users[user.Id] = user.Username
		}
	}
	for _, row := range rows {
		amount := decimal.NewFromInt(int64(row.Quota)).Mul(rate).DivRound(units, 12).String()
		currency := moneyContext.Display.Currency
		view := paidRedemptionView{ID: row.Id, Name: row.Name, Key: row.Key, Status: row.Status,
			CreatedTime: row.CreatedTime, RedeemedTime: row.RedeemedTime, ExpiredTime: row.ExpiredTime,
			Redeemed:     row.Status == common.RedemptionCodeStatusUsed || row.UsedUserId != 0 || row.RedeemedTime != 0 || row.PaymentID != 0,
			UsedUserName: users[row.UsedUserId], PaidAmountCNY: decimal.NewFromInt(row.PaidCents).Shift(-2).String(),
			CreditAmount: amount, CreditCurrency: currency, MoneyRevision: moneyContext.Revision,
			SalesChannel: row.SalesChannel, SalesBatch: row.SalesBatch, SalesEvidence: row.SalesEvidence,
		}
		var original model.MarketingMoneySnapshot
		if row.PaidCents > 0 && row.SalesChannel != "" && row.SalesBatch != "" && common.UnmarshalJsonStr(row.CreditedConversion, &original) == nil {
			quota, err := original.Quota()
			if err == nil && quota == int64(row.Quota) {
				view.SaleRegistered = true
				view.OriginalCreditAmount, view.OriginalCreditCurrency = original.Amount, original.Currency
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func GetAllRedemptions(c *gin.Context) {
	if err := model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error { return model.AuthorizeMarketingAdmin(tx, c.GetInt("id")) }); err != nil {
		marketingAPIError(c, err)
		return
	}
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	views, err := paidRedemptionViews(redemptions)
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	pageInfo.SetItems(views)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	if err := model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error { return model.AuthorizeMarketingAdmin(tx, c.GetInt("id")) }); err != nil {
		marketingAPIError(c, err)
		return
	}
	keyword := c.Query("keyword")
	status := c.Query("status")
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptions(keyword, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	views, err := paidRedemptionViews(redemptions)
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	pageInfo.SetItems(views)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	if err := model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error { return model.AuthorizeMarketingAdmin(tx, c.GetInt("id")) }); err != nil {
		marketingAPIError(c, err)
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	views, err := paidRedemptionViews([]*model.Redemption{redemption})
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	common.ApiSuccess(c, views[0])
	return
}

// Paid-code sale proceeds and wallet credit are separate amounts. Conversion
// rates and actor identity always come from the server, never this request.
type paidRedemptionRequest struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Count          int    `json:"count"`
	Status         int    `json:"status"`
	PaidAmountCNY  string `json:"paid_amount_cny"`
	CreditAmount   string `json:"credit_amount"`
	CreditCurrency string `json:"credit_currency"`
	MoneyRevision  string `json:"money_revision"`
	SalesChannel   string `json:"sales_channel"`
	SalesBatch     string `json:"sales_batch"`
	SalesEvidence  string `json:"sales_evidence"`
	ExpiredTime    int64  `json:"expired_time"`
}

func (request paidRedemptionRequest) saleSpec() (model.PaidRedemptionSpec, error) {
	if utf8.RuneCountInString(request.Name) == 0 || utf8.RuneCountInString(request.Name) > 20 {
		return model.PaidRedemptionSpec{}, common.NewMessage("Redemption code name length must be between 1-20")
	}
	paid, err := service.ParseMarketingPaidCNY(request.PaidAmountCNY)
	if err != nil {
		return model.PaidRedemptionSpec{}, err
	}
	moneyContext, err := service.CurrentMarketingMoneyContext()
	if err != nil {
		return model.PaidRedemptionSpec{}, err
	}
	if request.MoneyRevision == "" || request.MoneyRevision != moneyContext.Revision {
		return model.PaidRedemptionSpec{}, common.NewMessage("Wallet currency settings changed. Refresh and try again")
	}
	money := moneyContext.Display
	money.Amount = request.CreditAmount
	if _, err := money.Quota(); err != nil {
		return model.PaidRedemptionSpec{}, common.NewMessage("Invalid wallet amount or currency settings")
	}
	if request.CreditCurrency != money.Currency {
		return model.PaidRedemptionSpec{}, common.NewMessage("Wallet currency settings changed. Refresh and try again")
	}
	if err := validateExpiredTime(request.ExpiredTime); err != nil {
		return model.PaidRedemptionSpec{}, err
	}
	return model.PaidRedemptionSpec{Name: request.Name, Money: money, PaidCents: paid,
		SalesChannel: request.SalesChannel, SalesBatch: request.SalesBatch,
		SalesEvidence: request.SalesEvidence, ExpiredTime: request.ExpiredTime}, nil
}

func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return
	}
	var request paidRedemptionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if request.Count <= 0 || request.Count > 100 {
		common.ApiErrorT(c, "Select between 1 and 100 paid redemption codes")
		return
	}
	spec, err := request.saleSpec()
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	keys := make([]string, request.Count)
	for i := range keys {
		keys[i] = common.GetUUID()
	}
	err = model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error {
		_, err := model.CreatePaidRedemptionBatch(tx, c.GetInt("id"), keys, spec, common.GetTimestamp())
		return err
	})
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	quota, _ := spec.Money.Quota() // validated before the transaction
	recordManageAudit(c, "redemption.create", map[string]any{
		"name": request.Name, "count": len(keys), "quota": logger.FormatQuota(int(quota)),
		"paid_amount_cny": request.PaidAmountCNY,
		"sales_channel":   request.SalesChannel, "sales_batch": request.SalesBatch,
	})
	common.ApiSuccess(c, keys)
}

func DeleteRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error {
		_, err := model.DeletePaidRedemptions(tx, c.GetInt("id"), []int{id})
		return err
	})
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func UpdateRedemption(c *gin.Context) {
	var request paidRedemptionRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.ID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	statusOnly := c.Query("status_only") != ""
	var spec model.PaidRedemptionSpec
	var err error
	if !statusOnly {
		spec, err = request.saleSpec()
		if err != nil {
			marketingAPIError(c, err)
			return
		}
	}
	var result *model.Redemption
	err = model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error {
		var err error
		if statusOnly {
			result, err = model.SetPaidRedemptionStatus(tx, c.GetInt("id"), request.ID, request.Status, common.GetTimestamp())
		} else {
			result, err = model.UpdatePaidRedemption(tx, c.GetInt("id"), request.ID, spec, common.GetTimestamp())
		}
		return err
	})
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	views, err := paidRedemptionViews([]*model.Redemption{result})
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	common.ApiSuccess(c, views[0])
}

func DeleteInvalidRedemption(c *gin.Context) {
	var rows int64
	err := model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error {
		var err error
		rows, err = model.DeleteInvalidPaidRedemptions(tx, c.GetInt("id"), common.GetTimestamp())
		return err
	})
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func validateExpiredTime(expired int64) error {
	if expired != 0 && expired < common.GetTimestamp() {
		return common.NewMessage("Expiration time cannot be earlier than current time")
	}
	return nil
}

func DeleteRedemptionBatch(c *gin.Context) {
	var request struct {
		Ids []int `json:"ids" binding:"required,min=1,max=1000,dive,gt=0"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var count int64
	err := model.WithMarketingTransaction(c.Request.Context(), func(tx *gorm.DB) error {
		var err error
		count, err = model.DeletePaidRedemptions(tx, c.GetInt("id"), request.Ids)
		return err
	})
	if err != nil {
		marketingAPIError(c, err)
		return
	}
	recordManageAudit(c, "redemption.delete_batch", map[string]any{
		"count":                    count,
		"total":                    len(request.Ids),
		"requested_redemption_ids": request.Ids,
	})
	common.ApiSuccess(c, count)
}

// Marketing accounting rejects stale or conflicting facts with stable internal
// errors. Public endpoints expose translated messages rather than those details.
func marketingAPIError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrMarketingInput):
		common.ApiErrorT(c, "Invalid parameters")
	case errors.Is(err, model.ErrMarketingAccess):
		common.ApiErrorT(c, "Access Forbidden")
	case errors.Is(err, model.ErrMarketingConflict):
		common.ApiErrorT(c, "This operation conflicts with an existing record")
	case errors.Is(err, model.ErrTopUpNotFound):
		common.ApiErrorT(c, "Top-up order does not exist")
	case errors.Is(err, model.ErrPaymentMethodMismatch):
		common.ApiErrorT(c, "Unsupported payment channel")
	case errors.Is(err, model.ErrTopUpStatusInvalid):
		common.ApiErrorT(c, "Order is not pending payment and cannot be completed")
	default:
		common.ApiError(c, err)
	}
}
