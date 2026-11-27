package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type MarketingGiftUserOption struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type MarketingGiftBatchInput struct {
	OperationKey  string `json:"operation_key"`
	MoneyRevision string `json:"money_revision"`
	UserIDs       []int  `json:"user_ids"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	ValidSeconds  int64  `json:"valid_seconds"`
	Reason        string `json:"reason"`
}
type MarketingAdminGiftView struct {
	MarketingUserGift
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	BatchID  int64  `json:"batch_id,omitempty"`
}
type MarketingGiftRecipientView struct {
	ID          int64              `json:"id"`
	UserID      int                `json:"user_id"`
	Username    string             `json:"username"`
	State       string             `json:"state"`
	ErrorKey    string             `json:"error_key,omitempty"`
	LastActorID int                `json:"last_actor_id"`
	UpdatedAt   int64              `json:"updated_at"`
	Gift        *MarketingUserGift `json:"gift,omitempty"`
}
type MarketingGiftBatchView struct {
	ID           int64                        `json:"id"`
	ActorID      int                          `json:"actor_id"`
	ActorName    string                       `json:"actor_name"`
	Reason       string                       `json:"reason"`
	Amount       string                       `json:"amount"`
	TotalAmount  string                       `json:"total_amount"`
	Currency     string                       `json:"currency"`
	ValidSeconds int64                        `json:"valid_seconds"`
	CreatedAt    int64                        `json:"created_at"`
	Total        int                          `json:"total"`
	Credited     int                          `json:"credited"`
	Failed       int                          `json:"failed"`
	Pending      int                          `json:"pending"`
	Recipients   []MarketingGiftRecipientView `json:"recipients,omitempty"`
}

// The HTTP operation is bound to its original input before checking today's
// currency revision. A retry uses the original conversion, never today's rate.
func IssueMarketingAdminGifts(ctx context.Context, actorID int, input MarketingGiftBatchInput) (any, error) {
	if len(input.OperationKey) < 1 || len(input.OperationKey) > 64 || len(input.UserIDs) < 1 || len(input.UserIDs) > model.MarketingGiftBatchLimit || input.ValidSeconds < 0 || input.ValidSeconds > model.MarketingGiftMaxValidity || strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(input.Reason) > 500 || !utf8.ValidString(input.Reason) {
		return nil, model.ErrMarketingInput
	}
	for _, c := range input.OperationKey {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return nil, model.ErrMarketingInput
		}
	}
	input.UserIDs = slices.Clone(input.UserIDs)
	slices.Sort(input.UserIDs)
	for i, id := range input.UserIDs {
		if id <= 0 || (i > 0 && id == input.UserIDs[i-1]) {
			return nil, model.ErrMarketingInput
		}
	}
	evidence, err := common.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(evidence))
	var batch model.MarketingGiftBatch
	err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
			return err
		}
		var existing model.MarketingGiftBatch
		err := tx.Where("actor_id = ? AND operation_key = ?", actorID, input.OperationKey).First(&existing).Error
		if err == nil {
			if existing.PayloadHash != hash {
				return model.ErrMarketingConflict
			}
			batch = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		configuration, err := CurrentMarketingMoneyContext()
		if err != nil {
			return err
		}
		if input.MoneyRevision != configuration.Revision {
			return common.NewMessage("Wallet currency changed; refresh the form and try again")
		}
		money, err := marketingAdminMoney(input.Amount, input.Currency, configuration)
		if err != nil {
			return err
		}
		conversion, err := common.Marshal(money)
		if err != nil {
			return err
		}
		batch = model.MarketingGiftBatch{ActorID: actorID, OperationKey: input.OperationKey, PayloadHash: hash, Reason: input.Reason, Conversion: string(conversion), ValidSeconds: input.ValidSeconds, CreatedAt: time.Now().Unix()}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		rows := make([]model.MarketingGiftRecipient, 0, len(input.UserIDs))
		for _, id := range input.UserIDs {
			rows = append(rows, model.MarketingGiftRecipient{BatchID: batch.ID, UserID: id, State: "pending"})
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	return RetryMarketingAdminGifts(ctx, actorID, batch.ID)
}

func RetryMarketingAdminGifts(ctx context.Context, actorID int, batchID int64) (any, error) {
	var recipients []model.MarketingGiftRecipient
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
			return err
		}
		var batch model.MarketingGiftBatch
		if err := tx.First(&batch, batchID).Error; err != nil {
			return err
		}
		return tx.Where("batch_id = ? AND state <> ?", batch.ID, "credited").Order("user_id").Find(&recipients).Error
	})
	if err != nil {
		return nil, err
	}
	for _, recipient := range recipients {
		err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			return model.DeliverMarketingGiftRecipient(tx, actorID, batchID, recipient.ID)
		})
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Check current actor authorization separately: a disabled recipient is an
		// individual failure, but a revoked administrator must stop the whole request.
		key := "Gift delivery failed; retry this batch"
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, model.ErrMarketingAccess):
			key = "Recipient is missing or disabled"
		case errors.Is(err, model.ErrMarketingInput):
			key = "Gift amount exceeds the recipient wallet limit"
		}
		err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
			return model.RecordMarketingGiftFailure(tx, actorID, batchID, recipient.ID, key)
		})
		if err != nil {
			return nil, err
		}
	}
	return ReadMarketingAdminGifts(ctx, actorID, "gift-batches", batchID, model.MarketingAdminFilter{Page: 1, PageSize: 20}, "", "")
}

func ReadMarketingAdminGifts(ctx context.Context, actorID int, resource string, id int64, f model.MarketingAdminFilter, state, validity string) (any, error) {
	if resource != "gifts" && resource != "gift-batches" && resource != "gift-users" {
		return nil, model.ErrMarketingInput
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if state != "" && !slices.Contains([]string{"active", "used", "expired", "cleared"}, state) {
		return nil, model.ErrMarketingInput
	}
	if validity != "" && validity != "permanent" && validity != "limited" {
		return nil, model.ErrMarketingInput
	}
	if f.Kind != "" && !slices.Contains([]string{"registration", "checkin", "recharge", "invitation", "lottery", "admin_gift"}, f.Kind) {
		return nil, model.ErrMarketingInput
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
			return err
		}
		if resource == "gift-users" {
			q := tx.Model(&model.User{}).Where("status = ?", common.UserStatusEnabled)
			if f.Keyword != "" {
				condition := "username LIKE ? OR display_name LIKE ?"
				args := []any{"%" + f.Keyword + "%", "%" + f.Keyword + "%"}
				if uid, e := strconv.Atoi(f.Keyword); e == nil {
					condition += " OR id = ?"
					args = append(args, uid)
				}
				q = q.Where("("+condition+")", args...)
			}
			var total int64
			if err := q.Count(&total).Error; err != nil {
				return err
			}
			rows := []MarketingGiftUserOption{}
			if err := q.Select("id", "username", "display_name").Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Scan(&rows).Error; err != nil {
				return err
			}
			result = MarketingAdminPage{Items: rows, Total: total, Page: f.Page, PageSize: f.PageSize}
			return nil
		}
		users := tx.Unscoped().Model(&model.User{}).Select("id")
		if f.Keyword != "" {
			condition := "username LIKE ? OR display_name LIKE ?"
			args := []any{"%" + f.Keyword + "%", "%" + f.Keyword + "%"}
			if uid, e := strconv.Atoi(f.Keyword); e == nil {
				condition += " OR id = ?"
				args = append(args, uid)
			}
			users = users.Where("("+condition+")", args...)
		}
		if f.Username != "" {
			users = users.Where("username = ?", f.Username)
		}
		if f.UserID > 0 {
			users = users.Where("id = ?", f.UserID)
		}
		filterUser := f.Keyword != "" || f.Username != "" || f.UserID > 0
		now := time.Now().Unix()
		var total int64
		if resource == "gift-batches" {
			q := tx.Model(&model.MarketingGiftBatch{})
			if id > 0 {
				q = q.Where("id = ?", id)
			}
			if filterUser {
				q = q.Where("id IN (?)", tx.Model(&model.MarketingGiftRecipient{}).Select("batch_id").Where("user_id IN (?)", users))
			}
			if f.StartsAt > 0 {
				q = q.Where("created_at >= ?", f.StartsAt)
			}
			if f.EndsAt > 0 {
				q = q.Where("created_at < ?", f.EndsAt)
			}
			if err := q.Count(&total).Error; err != nil {
				return err
			}
			var rows []model.MarketingGiftBatch
			if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
				return err
			}
			items := make([]MarketingGiftBatchView, 0, len(rows))
			for _, b := range rows {
				item, err := marketingGiftBatchView(tx, b, id > 0, now)
				if err != nil {
					return err
				}
				items = append(items, item)
			}
			if id > 0 {
				if len(items) != 1 {
					return gorm.ErrRecordNotFound
				}
				result = items[0]
			} else {
				result = MarketingAdminPage{Items: items, Total: total, Page: f.Page, PageSize: f.PageSize}
			}
			return nil
		}
		q := tx.Model(&model.MarketingGift{})
		if id > 0 {
			q = q.Where("id = ?", id)
		}
		if filterUser {
			q = q.Where("user_id IN (?)", users)
		}
		if f.Kind != "" {
			q = q.Where("source_kind = ?", f.Kind)
		}
		if f.StartsAt > 0 {
			q = q.Where("granted_at >= ?", f.StartsAt)
		}
		if f.EndsAt > 0 {
			q = q.Where("granted_at < ?", f.EndsAt)
		}
		if validity == "permanent" {
			q = q.Where("expires_at = ?", 0)
		} else if validity == "limited" {
			q = q.Where("expires_at > ?", 0)
		}
		switch state {
		case "cleared":
			q = q.Where("cleared_quota > ?", 0)
		case "expired":
			q = q.Where("cleared_quota = ? AND expires_at > ? AND expires_at <= ?", 0, 0, now)
		case "used":
			q = q.Where("cleared_quota = ? AND (expires_at = ? OR expires_at > ?) AND remaining_quota = ?", 0, 0, now, 0)
		case "active":
			q = q.Where("cleared_quota = ? AND (expires_at = ? OR expires_at > ?) AND remaining_quota > ?", 0, 0, now, 0)
		}
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		var gifts []model.MarketingGift
		if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&gifts).Error; err != nil {
			return err
		}
		items := make([]MarketingAdminGiftView, 0, len(gifts))
		for _, gift := range gifts {
			view, err := marketingUserGiftView(tx, gift, now)
			if err != nil {
				return err
			}
			var user model.User
			if err := tx.Unscoped().Select("id", "username").Where("id = ?", gift.UserID).Find(&user).Error; err != nil {
				return err
			}
			item := MarketingAdminGiftView{MarketingUserGift: view, UserID: gift.UserID, Username: user.Username}
			var recipient model.MarketingGiftRecipient
			if err := tx.Where("gift_id = ?", gift.ID).Find(&recipient).Error; err != nil {
				return err
			}
			item.BatchID = recipient.BatchID
			items = append(items, item)
		}
		if id > 0 {
			if len(items) != 1 {
				return gorm.ErrRecordNotFound
			}
			result = items[0]
		} else {
			result = MarketingAdminPage{Items: items, Total: total, Page: f.Page, PageSize: f.PageSize}
		}
		return nil
	})
	return result, err
}

func marketingGiftBatchView(tx *gorm.DB, b model.MarketingGiftBatch, detail bool, now int64) (MarketingGiftBatchView, error) {
	var money model.MarketingMoneySnapshot
	if err := common.UnmarshalJsonStr(b.Conversion, &money); err != nil {
		return MarketingGiftBatchView{}, err
	}
	quota, err := money.Quota()
	if err != nil {
		return MarketingGiftBatchView{}, err
	}
	amount, currency, err := marketingAdminDisplayAmount(quota, MarketingMoneyContext{Display: money})
	if err != nil {
		return MarketingGiftBatchView{}, err
	}
	view := MarketingGiftBatchView{ID: b.ID, ActorID: b.ActorID, Reason: b.Reason, Amount: amount, Currency: currency, ValidSeconds: b.ValidSeconds, CreatedAt: b.CreatedAt}
	var actor model.User
	if err := tx.Unscoped().Select("id", "username").Where("id = ?", b.ActorID).Find(&actor).Error; err != nil {
		return view, err
	}
	view.ActorName = actor.Username
	var rows []model.MarketingGiftRecipient
	if err := tx.Where("batch_id = ?", b.ID).Order("id").Find(&rows).Error; err != nil {
		return view, err
	}
	view.Total = len(rows)
	for _, row := range rows {
		switch row.State {
		case "credited":
			view.Credited++
		case "failed":
			view.Failed++
		default:
			view.Pending++
		}
		if !detail {
			continue
		}
		item := MarketingGiftRecipientView{ID: row.ID, UserID: row.UserID, State: row.State, ErrorKey: row.ErrorKey, LastActorID: row.LastActorID, UpdatedAt: row.UpdatedAt}
		var user model.User
		if err := tx.Unscoped().Select("id", "username").Where("id = ?", row.UserID).Find(&user).Error; err != nil {
			return view, err
		}
		item.Username = user.Username
		if row.GiftID > 0 {
			var gift model.MarketingGift
			if err := tx.First(&gift, row.GiftID).Error; err != nil {
				return view, err
			}
			g, err := marketingUserGiftView(tx, gift, now)
			if err != nil {
				return view, err
			}
			item.Gift = &g
		}
		view.Recipients = append(view.Recipients, item)
	}
	value, err := decimal.NewFromString(amount)
	if err != nil {
		return view, err
	}
	view.TotalAmount = value.Mul(decimal.NewFromInt(int64(view.Credited))).String()
	return view, nil
}
