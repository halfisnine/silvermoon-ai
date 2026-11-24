package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// MarketingAccountingMoney contains display money only. The conversion evidence
// stays in the business database; no storage-unit amount crosses this API.
type MarketingAccountingMoney struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}
type MarketingAccountingGiftSource struct {
	GiftID        int64                     `json:"gift_id"`
	SourceName    string                    `json:"source_name"`
	SourceNameKey string                    `json:"source_name_key,omitempty"`
	Money         *MarketingAccountingMoney `json:"money"`
	ExpiresAt     int64                     `json:"expires_at"`
	RewardID      int64                     `json:"reward_id,omitempty"`
	RewardKind    string                    `json:"reward_kind,omitempty"`
}
type MarketingAccountingRequest struct {
	ID              int64                           `json:"id"`
	UserID          int                             `json:"user_id"`
	RequestID       string                          `json:"request_id"`
	ModelName       string                          `json:"model_name"`
	State           string                          `json:"state"`
	CostKnown       bool                            `json:"cost_known"`
	FinalObserved   bool                            `json:"final_observed"`
	Realtime        bool                            `json:"realtime"`
	RealtimeClosed  bool                            `json:"realtime_closed"`
	ConversionKnown bool                            `json:"conversion_known"`
	QualifiedAt     int64                           `json:"qualified_at"`
	Total           *MarketingAccountingMoney       `json:"total"`
	Actual          *MarketingAccountingMoney       `json:"actual"`
	Paid            *MarketingAccountingMoney       `json:"paid"`
	Gift            *MarketingAccountingMoney       `json:"gift"`
	Unsettled       *MarketingAccountingMoney       `json:"unsettled"`
	PendingReturn   *MarketingAccountingMoney       `json:"pending_return"`
	Reserved        *MarketingAccountingMoney       `json:"reserved"`
	GiftSources     []MarketingAccountingGiftSource `json:"gift_sources"`
}

type MarketingAccountingFilter struct {
	Page      int
	PageSize  int
	UserID    int
	RequestID string
	State     string
	StartsAt  int64
	EndsAt    int64
}

func (f MarketingAccountingFilter) Validate() error {
	if f.Page < 1 || f.Page > 1000000 || f.PageSize < 1 || f.PageSize > 100 || f.UserID < 0 || len(f.RequestID) > 128 || strings.TrimSpace(f.RequestID) != f.RequestID || f.StartsAt < 0 || f.EndsAt < 0 || (f.EndsAt > 0 && f.StartsAt >= f.EndsAt) {
		return model.ErrMarketingInput
	}
	if f.State != "" && !slices.Contains([]string{"reserved", "active", "settled", "released", "review"}, f.State) {
		return model.ErrMarketingInput
	}
	return nil
}

func marketingAccountingConversion(funding model.MarketingFunding) (*model.MarketingMoneySnapshot, error) {
	var evidence struct {
		DisplayMoney *model.MarketingMoneySnapshot `json:"display_money"`
	}
	// Older retained evidence can be a non-JSON tariff reference. It does not
	// prove historical display conversion and is never replaced by today's rate.
	if funding.PricingEvidence == "" || !strings.HasPrefix(strings.TrimSpace(funding.PricingEvidence), "{") {
		return nil, nil
	}
	if err := common.UnmarshalJsonStr(funding.PricingEvidence, &evidence); err != nil {
		return nil, err
	}
	if evidence.DisplayMoney == nil {
		return nil, nil
	}
	if _, err := evidence.DisplayMoney.Quota(); err != nil {
		return nil, err
	}
	return evidence.DisplayMoney, nil
}
func marketingAccountingMoney(quota int64, snapshot *model.MarketingMoneySnapshot) (*MarketingAccountingMoney, error) {
	if snapshot == nil {
		return nil, nil
	}
	amount, currency, err := marketingAdminDisplayAmount(quota, MarketingMoneyContext{Display: *snapshot})
	if err != nil {
		return nil, err
	}
	return &MarketingAccountingMoney{amount, currency}, nil
}

// Batch projection is shared by the existing usage-log list and the main-store
// reconciliation reader. Caller locks owners before reading financial facts.
func marketingAccountingRequests(tx *gorm.DB, funding []model.MarketingFunding) ([]MarketingAccountingRequest, error) {
	result := make([]MarketingAccountingRequest, 0, len(funding))
	if len(funding) == 0 {
		return result, nil
	}
	ids := make([]int64, 0, len(funding))
	fundingByID := make(map[int64]model.MarketingFunding, len(funding))
	for _, f := range funding {
		ids = append(ids, f.ID)
		fundingByID[f.ID] = f
	}
	var allocations []model.MarketingAllocation
	if err := tx.Where("funding_id IN ?", ids).Order("id").Find(&allocations).Error; err != nil {
		return nil, err
	}
	byFunding := map[int64][]model.MarketingAllocation{}
	giftIDs := []int64{}
	for _, a := range allocations {
		byFunding[a.FundingID] = append(byFunding[a.FundingID], a)
		if a.GiftID > 0 {
			giftIDs = append(giftIDs, a.GiftID)
		}
	}
	slices.Sort(giftIDs)
	giftIDs = slices.Compact(giftIDs)
	gifts := map[int64]model.MarketingGift{}
	giftSources := map[int64]MarketingAccountingGiftSource{}
	if len(giftIDs) > 0 {
		var rows []model.MarketingGift
		if err := tx.Where("id IN ?", giftIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, g := range rows {
			gifts[g.ID] = g
			giftSources[g.ID] = MarketingAccountingGiftSource{GiftID: g.ID, SourceNameKey: "Activity reward", ExpiresAt: g.ExpiresAt}
		}
		var rewards []model.MarketingActivityReward
		if err := tx.Where("gift_id IN ?", giftIDs).Find(&rewards).Error; err != nil {
			return nil, err
		}
		activityIDs := []int64{}
		for _, r := range rewards {
			activityIDs = append(activityIDs, r.ActivityID)
		}
		var activities []model.MarketingActivity
		if len(activityIDs) > 0 {
			if err := tx.Select("id", "name").Where("id IN ?", activityIDs).Find(&activities).Error; err != nil {
				return nil, err
			}
		}
		names := map[int64]string{}
		for _, a := range activities {
			names[a.ID] = a.Name
		}
		for _, r := range rewards {
			g, ok := gifts[r.GiftID]
			if !ok || g.UserID != r.UserID {
				return nil, model.ErrMarketingReview
			}
			s := giftSources[r.GiftID]
			s.SourceName = names[r.ActivityID]
			s.SourceNameKey = ""
			s.RewardID = r.ID
			s.RewardKind = "activity"
			giftSources[r.GiftID] = s
		}
		var awards []model.MarketingLotteryAward
		if err := tx.Where("gift_id IN ?", giftIDs).Find(&awards).Error; err != nil {
			return nil, err
		}
		poolIDs := []int64{}
		for _, a := range awards {
			poolIDs = append(poolIDs, a.PoolID)
		}
		var pools []model.MarketingLotteryPool
		if len(poolIDs) > 0 {
			if err := tx.Select("id", "name").Where("id IN ?", poolIDs).Find(&pools).Error; err != nil {
				return nil, err
			}
		}
		names = map[int64]string{}
		for _, p := range pools {
			names[p.ID] = p.Name
		}
		for _, a := range awards {
			g, ok := gifts[a.GiftID]
			if !ok || g.UserID != a.UserID {
				return nil, model.ErrMarketingReview
			}
			s := giftSources[a.GiftID]
			s.SourceName = names[a.PoolID]
			s.SourceNameKey = ""
			s.RewardID = a.DrawID
			s.RewardKind = "lottery"
			giftSources[a.GiftID] = s
		}
	}
	var projections []model.MarketingUsageProjection
	latest := tx.Model(&model.MarketingUsageProjection{}).Select("MAX(id)").Where("funding_id IN ?", ids).Group("funding_id")
	if err := tx.Where("id IN (?)", latest).Find(&projections).Error; err != nil {
		return nil, err
	}
	requests := map[int64]string{}
	for _, p := range projections {
		var log model.Log
		if err := common.UnmarshalJsonStr(p.Payload, &log); err != nil {
			return nil, err
		}
		owner, exists := fundingByID[p.FundingID]
		if !exists || log.UserId != owner.UserID || log.UsageEventID != owner.RequestKey {
			return nil, model.ErrMarketingReview
		}
		requests[p.FundingID] = log.RequestId
	}
	for _, f := range funding {
		snapshot, err := marketingAccountingConversion(f)
		if err != nil {
			return nil, err
		}
		view := MarketingAccountingRequest{ID: f.ID, UserID: f.UserID, RequestID: requests[f.ID], State: f.State, CostKnown: f.CostKnown, FinalObserved: f.FinalObserved, Realtime: f.Realtime, RealtimeClosed: f.RealtimeClosed, ConversionKnown: snapshot != nil, QualifiedAt: f.QualifiedAt, GiftSources: []MarketingAccountingGiftSource{}}
		if strings.HasPrefix(strings.TrimSpace(f.PricingEvidence), "{") {
			var pricing struct {
				Model string `json:"model"`
			}
			if err := common.UnmarshalJsonStr(f.PricingEvidence, &pricing); err != nil {
				return nil, err
			}
			view.ModelName = pricing.Model
		}
		var paid, gift int64
		sourceQuota := map[int64]int64{}
		for _, a := range byFunding[f.ID] {
			if a.ConsumedQuota < 0 || a.ConsumedQuota > common.MaxWalletQuota-paid-gift {
				return nil, model.ErrMarketingReview
			}
			if a.GiftID == 0 {
				paid += a.ConsumedQuota
				continue
			}
			g, ok := gifts[a.GiftID]
			if !ok || g.UserID != f.UserID {
				return nil, model.ErrMarketingReview
			}
			gift += a.ConsumedQuota
			sourceQuota[a.GiftID] += a.ConsumedQuota
		}
		if paid+gift != f.SettledQuota {
			return nil, model.ErrMarketingReview
		}
		for _, field := range []struct {
			quota  int64
			target **MarketingAccountingMoney
		}{{paid, &view.Paid}, {gift, &view.Gift}, {f.SettledQuota, &view.Total}, {f.ReservedQuota, &view.Reserved}} {
			*field.target, err = marketingAccountingMoney(field.quota, snapshot)
			if err != nil {
				return nil, err
			}
		}
		if f.CostKnown {
			view.Actual, err = marketingAccountingMoney(f.ActualQuota, snapshot)
			if err != nil {
				return nil, err
			}
			view.Unsettled, err = marketingAccountingMoney(max(int64(0), f.ActualQuota-f.SettledQuota), snapshot)
			if err != nil {
				return nil, err
			}
			view.PendingReturn, err = marketingAccountingMoney(max(int64(0), f.SettledQuota-f.ActualQuota), snapshot)
			if err != nil {
				return nil, err
			}
		}
		for _, id := range giftIDs {
			amount := sourceQuota[id]
			if amount == 0 {
				continue
			}
			source := giftSources[id]
			source.Money, err = marketingAccountingMoney(amount, snapshot)
			if err != nil {
				return nil, err
			}
			view.GiftSources = append(view.GiftSources, source)
		}
		result = append(result, view)
	}
	return result, nil
}

func ReadMarketingWallet(ctx context.Context, userID int) (any, error) {
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingUser(tx, userID); err != nil {
			return err
		}
		var user model.User
		if err := tx.Select("id", "quota").First(&user, userID).Error; err != nil {
			return err
		}
		var gifts []model.MarketingGift
		if err := tx.Select("remaining_quota").Where("user_id = ? AND (expires_at = ? OR expires_at > ?)", userID, 0, time.Now().Unix()).Find(&gifts).Error; err != nil {
			return err
		}
		var gift int64
		for _, g := range gifts {
			if g.RemainingQuota < 0 || g.RemainingQuota > common.MaxWalletQuota-gift {
				return model.ErrMarketingReview
			}
			gift += g.RemainingQuota
		}
		snapshot, err := MarketingDisplayMoney("1")
		if err != nil {
			return err
		}
		paidMoney, err := marketingAccountingMoney(int64(user.Quota), &snapshot)
		if err != nil {
			return err
		}
		giftMoney, err := marketingAccountingMoney(gift, &snapshot)
		if err != nil {
			return err
		}
		result = map[string]any{"paid": paidMoney, "gift": giftMoney}
		return nil
	})
	return result, err
}

// ReadMarketingAccounting is owner-scoped for users. Administrators can query
// unresolved requests even when the independent usage-log database is absent.
func ReadMarketingAccounting(ctx context.Context, actorID int, admin bool, id int64, f MarketingAccountingFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if id < 0 {
		return nil, model.ErrMarketingInput
	}
	if !admin {
		if f.UserID != 0 && f.UserID != actorID {
			return nil, model.ErrMarketingAccess
		}
		f.UserID = actorID
	}
	var rows []model.MarketingFunding
	var total int64
	// Discover the bounded page in a short authorized transaction, then acquire
	// all owner locks in the common sorted order in a fresh transaction. This
	// avoids both actor/owner lock inversion and a stale MySQL snapshot created
	// before the owner lock was acquired. Funding ownership is immutable.
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if admin {
			if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
				return err
			}
		} else if err := model.AuthorizeMarketingUser(tx, actorID); err != nil {
			return err
		}
		q := tx.Model(&model.MarketingFunding{})
		if id > 0 {
			q = q.Where("id = ?", id)
		}
		if f.UserID > 0 {
			q = q.Where("user_id = ?", f.UserID)
		}
		if f.State != "" {
			q = q.Where("state = ?", f.State)
		}
		if f.StartsAt > 0 {
			q = q.Where("qualified_at >= ?", f.StartsAt)
		}
		if f.EndsAt > 0 {
			q = q.Where("qualified_at < ?", f.EndsAt)
		}
		if f.RequestID != "" {
			key, err := model.MarketingRequestKey(f.RequestID)
			if err != nil {
				return err
			}
			q = q.Where("request_key = ?", key)
		}
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		return q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	owners := []int{actorID}
	ids := []int64{}
	for _, row := range rows {
		owners = append(owners, row.UserID)
		ids = append(ids, row.ID)
	}
	var result any
	err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.LockMarketingAccountingUsers(tx, owners); err != nil {
			return err
		}
		if admin {
			if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
				return err
			}
		} else if err := model.AuthorizeMarketingUser(tx, actorID); err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Where("id IN ?", ids).Order("id DESC").Find(&rows).Error; err != nil {
				return err
			}
		}
		views, err := marketingAccountingRequests(tx, rows)
		if err != nil {
			return err
		}
		if id > 0 || f.RequestID != "" {
			if len(views) != 1 {
				return gorm.ErrRecordNotFound
			}
			if views[0].RequestID == "" {
				views[0].RequestID = f.RequestID
			}
			result = views[0]
		} else {
			result = MarketingAdminPage{Items: views, Total: total, Page: f.Page, PageSize: f.PageSize}
		}
		return nil
	})
	return result, err
}

// EnrichMarketingUsageLogs receives an already authorized list. It binds every
// source to BOTH the log owner and its unguessable request key; it never expands
// a token/user-filtered query into records belonging to another user.
func EnrichMarketingUsageLogs(ctx context.Context, logs []*model.Log) error {
	keys := []string{}
	owners := []int{}
	for _, log := range logs {
		if log.Type == model.LogTypeConsume && log.UsageEventID != "" {
			keys = append(keys, log.UsageEventID)
			owners = append(owners, log.UserId)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.LockMarketingAccountingUsers(tx, owners); err != nil {
			return err
		}
		var funding []model.MarketingFunding
		if err := tx.Where("request_key IN ? AND user_id IN ?", keys, owners).Find(&funding).Error; err != nil {
			return err
		}
		views, err := marketingAccountingRequests(tx, funding)
		if err != nil {
			return err
		}
		byKey := map[string]MarketingAccountingRequest{}
		for i, f := range funding {
			byKey[f.RequestKey] = views[i]
		}
		for _, log := range logs {
			view, ok := byKey[log.UsageEventID]
			if !ok || view.UserID != log.UserId {
				continue
			}
			var other map[string]common.RawMessage
			if log.Other != "" {
				if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
					return err
				}
			}
			if other == nil {
				other = map[string]common.RawMessage{}
			}
			accounting, err := common.Marshal(view)
			if err != nil {
				return err
			}
			other["accounting"] = accounting
			encoded, err := common.Marshal(other)
			if err != nil {
				return err
			}
			log.Other = string(encoded)
		}
		return nil
	})
}

// Monthly paid net consumption is booked by settlement/reversal time. Neither
// a projection nor a realtime parent aggregate is counted: ledger debits and
// their original-source reversals are the only contribution records.
func ReadMarketingPaidConsumption(ctx context.Context, actorID, userID int, admin bool, month string) (any, error) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	start, err := time.ParseInLocation("2006-01", month, location)
	if err != nil || start.Year() < 1970 || start.Year() > 9998 {
		return nil, model.ErrMarketingInput
	}
	if !admin {
		if userID != 0 && userID != actorID {
			return nil, model.ErrMarketingAccess
		}
		userID = actorID
	}
	if userID <= 0 {
		return nil, model.ErrMarketingInput
	}
	var result any
	err = model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.LockMarketingAccountingUsers(tx, []int{actorID, userID}); err != nil {
			return err
		}
		if admin {
			if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
				return err
			}
		} else if err := model.AuthorizeMarketingUser(tx, actorID); err != nil {
			return err
		}
		sums := map[string]decimal.Decimal{}
		unknown := int64(0)
		var after int64
		for {
			var rows []model.MarketingLedgerEntry
			if err := tx.Where("user_id = ? AND gift_id = ? AND kind IN ? AND recorded_at >= ? AND recorded_at < ? AND id > ?", userID, 0, []string{"consume", "consume_reversal"}, start.Unix(), start.AddDate(0, 1, 0).Unix(), after).Order("id").Limit(500).Find(&rows).Error; err != nil {
				return err
			}
			ids := []int64{}
			for _, row := range rows {
				ids = append(ids, row.FundingID)
			}
			var funds []model.MarketingFunding
			if len(ids) > 0 {
				if err := tx.Where("id IN ? AND user_id = ?", ids, userID).Find(&funds).Error; err != nil {
					return err
				}
			}
			snapshots := map[int64]*model.MarketingMoneySnapshot{}
			for _, f := range funds {
				snapshot, err := marketingAccountingConversion(f)
				if err != nil {
					return err
				}
				snapshots[f.ID] = snapshot
			}
			debitIDs := []int64{}
			for _, r := range rows {
				debitIDs = append(debitIDs, r.DebitID)
			}
			var debits []model.MarketingDebit
			if len(debitIDs) > 0 {
				if err := tx.Where("id IN ?", debitIDs).Find(&debits).Error; err != nil {
					return err
				}
			}
			debitByID := map[int64]model.MarketingDebit{}
			for _, d := range debits {
				debitByID[d.ID] = d
			}
			for _, row := range rows {
				after = row.ID
				snapshot, exists := snapshots[row.FundingID]
				if !exists {
					return model.ErrMarketingReview
				}
				original, exists := debitByID[row.DebitID]
				if !exists || original.FundingID != row.FundingID || original.GiftID != 0 || row.AmountQuota < 0 || row.AmountQuota > original.Quota {
					return model.ErrMarketingReview
				}
				if snapshot == nil {
					unknown++
					continue
				}
				units, _ := decimal.NewFromString(snapshot.QuotaPerUnit)
				rate, _ := decimal.NewFromString(snapshot.CurrencyPerUnit)
				amount := decimal.NewFromInt(row.AmountQuota).Mul(rate).DivRound(units, 32)
				if row.Kind == "consume_reversal" {
					amount = amount.Neg()
				}
				sums[snapshot.Currency] = sums[snapshot.Currency].Add(amount)
			}
			if len(rows) < 500 {
				break
			}
		}
		currencies := []string{}
		for c := range sums {
			currencies = append(currencies, c)
		}
		slices.Sort(currencies)
		amounts := []MarketingAccountingMoney{}
		for _, c := range currencies {
			amounts = append(amounts, MarketingAccountingMoney{sums[c].Round(12).String(), c})
		}
		result = map[string]any{"month": month, "amounts": amounts, "conversion_unknown_count": unknown}
		return nil
	})
	return result, err
}

type MarketingAccountingPayment struct {
	ID             int64                     `json:"id"`
	UserID         int                       `json:"user_id"`
	Kind           string                    `json:"kind"`
	PaidCNY        string                    `json:"paid_cny"`
	Credited       *MarketingAccountingMoney `json:"credited"`
	PostedAt       int64                     `json:"posted_at"`
	OrderID        int                       `json:"order_id,omitempty"`
	OrderReference string                    `json:"order_reference,omitempty"`
	Provider       string                    `json:"provider,omitempty"`
	RedemptionID   int                       `json:"redemption_id,omitempty"`
	RedemptionName string                    `json:"redemption_name,omitempty"`
	SalesChannel   string                    `json:"sales_channel,omitempty"`
	SalesBatch     string                    `json:"sales_batch,omitempty"`
	FactID         int64                     `json:"fact_id,omitempty"`
}

// Payment provenance identifies a verified receipt. It never infers payment
// from a recharge-type usage log, returns a usable code, or invents FIFO lots.
func ReadMarketingAccountingPayments(ctx context.Context, actorID int, id int64, f MarketingAccountingFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
			return err
		}
		q := tx.Model(&model.MarketingPayment{})
		if id > 0 {
			q = q.Where("id = ?", id)
		}
		if f.UserID > 0 {
			q = q.Where("user_id = ?", f.UserID)
		}
		if f.StartsAt > 0 {
			q = q.Where("posted_at >= ?", f.StartsAt)
		}
		if f.EndsAt > 0 {
			q = q.Where("posted_at < ?", f.EndsAt)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		var rows []model.MarketingPayment
		if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
			return err
		}
		ids := []int64{}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		var orders []model.TopUp
		var codes []model.Redemption
		var facts []model.MarketingActivityFact
		if len(ids) > 0 {
			if err := tx.Select("id", "user_id", "payment_id", "trade_no", "payment_provider").Where("payment_id IN ?", ids).Find(&orders).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Select("id", "used_user_id", "payment_id", "name", "sales_channel", "sales_batch").Where("payment_id IN ?", ids).Find(&codes).Error; err != nil {
				return err
			}
			if err := tx.Select("id", "user_id", "payment_id").Where("payment_id IN ?", ids).Find(&facts).Error; err != nil {
				return err
			}
		}
		orderByPayment := map[int64]model.TopUp{}
		for _, o := range orders {
			orderByPayment[o.PaymentID] = o
		}
		codeByPayment := map[int64]model.Redemption{}
		for _, c := range codes {
			codeByPayment[c.PaymentID] = c
		}
		factByPayment := map[int64]model.MarketingActivityFact{}
		for _, f := range facts {
			factByPayment[f.PaymentID] = f
		}
		views := []MarketingAccountingPayment{}
		for _, p := range rows {
			var snapshot model.MarketingMoneySnapshot
			if err := common.UnmarshalJsonStr(p.Conversion, &snapshot); err != nil {
				return err
			}
			quota, err := snapshot.Quota()
			if err != nil || quota != p.CreditedQuota {
				return model.ErrMarketingReview
			}
			credited, err := marketingAccountingMoney(p.CreditedQuota, &snapshot)
			if err != nil {
				return err
			}
			v := MarketingAccountingPayment{ID: p.ID, UserID: p.UserID, Kind: p.SourceKind, PaidCNY: decimal.NewFromInt(p.PaidCents).Shift(-2).String(), Credited: credited, PostedAt: p.PostedAt}
			if order, ok := orderByPayment[p.ID]; ok {
				if order.UserId != p.UserID {
					return model.ErrMarketingReview
				}
				v.OrderID = order.Id
				v.OrderReference = order.TradeNo
				v.Provider = order.PaymentProvider
			}
			if code, ok := codeByPayment[p.ID]; ok {
				if code.UsedUserId != p.UserID {
					return model.ErrMarketingReview
				}
				v.RedemptionID = code.Id
				v.RedemptionName = code.Name
				v.SalesChannel = code.SalesChannel
				v.SalesBatch = code.SalesBatch
			}
			if fact, ok := factByPayment[p.ID]; ok {
				if fact.UserID != p.UserID {
					return model.ErrMarketingReview
				}
				v.FactID = fact.ID
			}
			views = append(views, v)
		}
		if id > 0 {
			if len(views) != 1 {
				return gorm.ErrRecordNotFound
			}
			result = views[0]
		} else {
			result = MarketingAdminPage{Items: views, Total: total, Page: f.Page, PageSize: f.PageSize}
		}
		return nil
	})
	return result, err
}

type MarketingAccountingCashRefund struct {
	ID                    int64                     `json:"id"`
	UserID                int                       `json:"user_id"`
	RecordedAt            int64                     `json:"recorded_at"`
	ExactRefundCNY        string                    `json:"exact_refund_cny"`
	RefundedCNY           string                    `json:"refunded_cny"`
	RoundingDifferenceCNY string                    `json:"rounding_difference_cny"`
	ClearedPaid           *MarketingAccountingMoney `json:"cleared_paid"`
	ClearedGift           *MarketingAccountingMoney `json:"cleared_gift"`
}

// Cash refunds are completed, manually evidenced maintenance operations. They
// are deliberately separate from model/task cost reversals and have no write API.
func ReadMarketingAccountingCashRefunds(ctx context.Context, actorID int, id int64, f MarketingAccountingFilter) (any, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
			return err
		}
		q := tx.Model(&model.MarketingMaintenanceOperation{}).Where("kind = ?", "asset_clear")
		if id > 0 {
			q = q.Where("id = ?", id)
		}
		if f.UserID > 0 {
			q = q.Where("user_id = ?", f.UserID)
		}
		if f.StartsAt > 0 {
			q = q.Where("recorded_at >= ?", f.StartsAt)
		}
		if f.EndsAt > 0 {
			q = q.Where("recorded_at < ?", f.EndsAt)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		var rows []model.MarketingMaintenanceOperation
		if err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error; err != nil {
			return err
		}
		views := []MarketingAccountingCashRefund{}
		for _, row := range rows {
			var cleared model.MarketingAssetClearResult
			if err := common.UnmarshalJsonStr(row.Result, &cleared); err != nil {
				return err
			}
			view := MarketingAccountingCashRefund{ID: row.ID, UserID: row.UserID, RecordedAt: row.RecordedAt, ExactRefundCNY: cleared.ExactRefundCNY, RefundedCNY: decimal.NewFromInt(cleared.RefundedCents).Shift(-2).String(), RoundingDifferenceCNY: cleared.RoundingDifferenceCNY}
			var details struct{ Evidence string }
			if err := common.UnmarshalJsonStr(row.Details, &details); err != nil {
				return err
			}
			var evidence struct{ DisplayConversion *model.MarketingMoneySnapshot }
			// Direct trusted operator calls predating the display snapshot retain cash
			// facts but do not authorize reconstructing cleared asset values at today's rate.
			if strings.HasPrefix(strings.TrimSpace(details.Evidence), "{") {
				if err := common.UnmarshalJsonStr(details.Evidence, &evidence); err != nil {
					return err
				}
			}
			if evidence.DisplayConversion != nil {
				if _, err := evidence.DisplayConversion.Quota(); err != nil {
					return err
				}
				var err error
				view.ClearedPaid, err = marketingAccountingMoney(cleared.PaidQuota, evidence.DisplayConversion)
				if err != nil {
					return err
				}
				view.ClearedGift, err = marketingAccountingMoney(cleared.GiftQuota, evidence.DisplayConversion)
				if err != nil {
					return err
				}
			}
			views = append(views, view)
		}
		if id > 0 {
			if len(views) != 1 {
				return gorm.ErrRecordNotFound
			}
			result = views[0]
		} else {
			result = MarketingAdminPage{Items: views, Total: total, Page: f.Page, PageSize: f.PageSize}
		}
		return nil
	})
	return result, err
}
