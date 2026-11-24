package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// This is a read-only extension of the existing session-authenticated marketing
// routes. Actor identity comes from the verified session, never query parameters.
func marketingAccountingRead(c *gin.Context, actorID int, admin bool) {
	f := service.MarketingAccountingFilter{Page: 1, PageSize: 20, RequestID: c.Query("request_id"), State: c.Query("state")}
	var err error
	for key, target := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize, "user_id": &f.UserID} {
		if raw, ok := c.GetQuery(key); ok {
			if key == "user_id" && !admin {
				marketingUserFailure(c, model.ErrMarketingInput)
				return
			}
			*target, err = strconv.Atoi(raw)
			if err != nil {
				marketingUserFailure(c, model.ErrMarketingInput)
				return
			}
		}
	}
	for key, target := range map[string]*int64{"starts_at": &f.StartsAt, "ends_at": &f.EndsAt} {
		if raw, ok := c.GetQuery(key); ok {
			*target, err = strconv.ParseInt(raw, 10, 64)
			if err != nil {
				marketingUserFailure(c, model.ErrMarketingInput)
				return
			}
		}
	}
	var id int64
	if raw := c.Param("id"); raw != "" {
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			marketingUserFailure(c, model.ErrMarketingInput)
			return
		}
	}
	if err := f.Validate(); err != nil {
		marketingUserFailure(c, err)
		return
	}
	var result any
	switch c.Param("resource") {
	case "wallet":
		if admin || id != 0 {
			err = model.ErrMarketingInput
		} else {
			result, err = service.ReadMarketingWallet(c.Request.Context(), actorID)
		}
	case "consumption":
		if id != 0 {
			err = model.ErrMarketingInput
		} else {
			result, err = service.ReadMarketingPaidConsumption(c.Request.Context(), actorID, f.UserID, admin, c.Query("month"))
		}
	case "accounting":
		result, err = service.ReadMarketingAccounting(c.Request.Context(), actorID, admin, id, f)
	case "payments":
		if !admin {
			err = model.ErrMarketingAccess
		} else {
			result, err = service.ReadMarketingAccountingPayments(c.Request.Context(), actorID, id, f)
		}
	case "cash-refunds":
		if !admin {
			err = model.ErrMarketingAccess
		} else {
			result, err = service.ReadMarketingAccountingCashRefunds(c.Request.Context(), actorID, id, f)
		}
	default:
		err = model.ErrMarketingInput
	}
	if err != nil {
		marketingUserFailure(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
