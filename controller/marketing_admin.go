package controller

import (
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// MarketingAdminRead handles only administrator configuration and provenance.
// User activity pages use their own owner-scoped APIs, never this projection.
func MarketingAdminRead(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("a dashboard login session is required"))
		return
	}
	switch c.Param("resource") {
	case "consumption", "accounting", "payments", "cash-refunds":
		marketingAccountingRead(c, identity.UserID, true)
		return
	}
	resource := c.Param("resource")
	if resource == "" {
		resource = "metadata"
	}
	if c.Param("kind") != "" {
		resource = "rewards"
	}
	id := int64(0)
	var err error
	if c.Param("id") != "" {
		id, err = strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			marketingAdminFailure(c, model.ErrMarketingInput)
			return
		}
	}
	f := model.MarketingAdminFilter{Page: 1, PageSize: 20, Kind: c.Query("kind"), Keyword: c.Query("keyword"), ExclusiveGroup: c.Query("exclusive_group"), Username: c.Query("username"), Archive: c.Query("archive")}
	for key, target := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize, "user_id": &f.UserID} {
		if value, exists := c.GetQuery(key); exists {
			*target, err = strconv.Atoi(value)
			if err != nil {
				marketingAdminFailure(c, model.ErrMarketingInput)
				return
			}
		}
	}
	for key, target := range map[string]*int64{"activity_id": &f.ActivityID, "starts_at": &f.StartsAt, "ends_at": &f.EndsAt} {
		if value, exists := c.GetQuery(key); exists {
			*target, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				marketingAdminFailure(c, model.ErrMarketingInput)
				return
			}
		}
	}
	if err = f.Validate(); err != nil {
		marketingAdminFailure(c, err)
		return
	}
	var result any
	if resource == "gifts" || resource == "gift-batches" || resource == "gift-users" {
		result, err = service.ReadMarketingAdminGifts(c.Request.Context(), identity.UserID, resource, id, f, c.Query("state"), c.Query("validity"))
	} else if resource == "rewards" {
		if c.Param("kind") != "" {
			f.Kind = c.Param("kind")
		}
		result, err = service.ReadMarketingAdminRewards(c.Request.Context(), identity.UserID, id, f)
	} else {
		result, err = service.ReadMarketingAdmin(c.Request.Context(), identity.UserID, resource, id, f)
	}
	if err != nil {
		marketingAdminFailure(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func MarketingAdminWrite(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("a dashboard login session is required"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 262145))
	if err != nil || len(body) > 262144 {
		marketingAdminFailure(c, model.ErrMarketingInput)
		return
	}
	if c.Param("resource") == "gift-batches" {
		var result any
		if c.Param("id") != "" {
			id, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
			if parseErr != nil || id <= 0 || c.Param("action") != "retry" || c.Request.Method != http.MethodPost {
				marketingAdminFailure(c, model.ErrMarketingInput)
				return
			}
			result, err = service.RetryMarketingAdminGifts(c.Request.Context(), identity.UserID, id)
		} else {
			var input service.MarketingGiftBatchInput
			if c.Request.Method != http.MethodPost || common.Unmarshal(body, &input) != nil {
				marketingAdminFailure(c, model.ErrMarketingInput)
				return
			}
			result, err = service.IssueMarketingAdminGifts(c.Request.Context(), identity.UserID, input)
		}
		if err != nil {
			marketingAdminFailure(c, err)
			return
		}
		common.ApiSuccess(c, result)
		return
	}
	var input service.MarketingAdminRequest
	if err = common.Unmarshal(body, &input); err != nil {
		marketingAdminFailure(c, model.ErrMarketingInput)
		return
	}
	id := int64(0)
	action := "create"
	if c.Param("id") != "" {
		id, err = strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			marketingAdminFailure(c, model.ErrMarketingInput)
			return
		}
		action = "update"
	}
	if c.Param("action") != "" {
		action = c.Param("action")
	}
	result, err := service.WriteMarketingAdmin(c.Request.Context(), identity.UserID, c.Param("resource"), action, id, input)
	if err != nil {
		marketingAdminFailure(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func marketingAdminFailure(c *gin.Context, err error) {
	status, key := service.MarketingAdminError(err)
	common.ApiErrorStatus(c, status, common.NewMessage(key))
}
