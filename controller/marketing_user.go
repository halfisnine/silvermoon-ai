package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func MarketingUserRead(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("a dashboard login session is required"))
		return
	}
	switch c.Param("resource") {
	case "wallet", "consumption", "accounting":
		marketingAccountingRead(c, identity.UserID, false)
		return
	}
	if _, exists := c.GetQuery("user_id"); exists {
		marketingUserFailure(c, model.ErrMarketingInput)
		return
	}
	f := service.MarketingUserFilter{Page: 1, PageSize: 20, Kind: c.Query("kind"), State: c.Query("state"), ActivityKind: c.Query("activity_kind")}
	var err error
	for key, target := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
		if raw, ok := c.GetQuery(key); ok {
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
	resource := c.Param("resource")
	if c.Param("kind") != "" {
		resource = "rewards"
		f.Kind = c.Param("kind")
	}
	if c.Param("operation_key") != "" {
		resource = "draws"
	}
	switch resource {
	case "activities":
		result, err = service.ReadMarketingUserActivities(c.Request.Context(), identity.UserID, id, f)
	case "pools":
		result, err = service.ReadMarketingUserPools(c.Request.Context(), identity.UserID, id, f)
	case "draws":
		result, err = service.ReadMarketingUserDraw(c.Request.Context(), identity.UserID, id, c.Param("operation_key"))
	case "rewards":
		result, err = service.ReadMarketingUserRewards(c.Request.Context(), identity.UserID, id, f)
	case "gifts":
		result, err = service.ReadMarketingUserGifts(c.Request.Context(), identity.UserID, id, f)
	case "invitation":
		result, err = service.ReadMarketingUserInvitation(c.Request.Context(), identity.UserID, f)
	default:
		err = model.ErrMarketingInput
	}
	if err != nil {
		marketingUserFailure(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func MarketingUserDraw(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("a dashboard login session is required"))
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		marketingUserFailure(c, model.ErrMarketingInput)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 2049))
	if err != nil || len(body) > 2048 {
		marketingUserFailure(c, model.ErrMarketingInput)
		return
	}
	var request struct {
		OperationKey string `json:"operation_key"`
	}
	if err := common.Unmarshal(body, &request); err != nil {
		marketingUserFailure(c, model.ErrMarketingInput)
		return
	}
	result, err := service.SubmitMarketingUserDraw(c.Request.Context(), identity.UserID, id, request.OperationKey)
	if err != nil {
		marketingUserFailure(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func marketingUserFailure(c *gin.Context, err error) {
	status, key := http.StatusInternalServerError, "Unable to load activity data; please try again"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, key = http.StatusNotFound, "Marketing record not found"
	case errors.Is(err, model.ErrMarketingAccess):
		status, key = http.StatusForbidden, "This activity is not available for your account"
	case errors.Is(err, model.ErrMarketingInput):
		status, key = http.StatusBadRequest, "Invalid activity request; refresh the page and try again"
	case errors.Is(err, model.ErrMarketingConflict):
		status, key = http.StatusConflict, "This request has already been used for another draw"
	case errors.Is(err, model.ErrMarketingInsufficient):
		status, key = http.StatusConflict, "No prizes are available; your opportunity has not been used"
	}
	common.ApiErrorStatus(c, status, common.NewMessage(key))
}
