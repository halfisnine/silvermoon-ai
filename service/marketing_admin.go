package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Public forms contain human currency amounts, never storage units or a
// client-supplied conversion. IDs are relation keys, not display labels.
type MarketingAdminReward struct {
	CreditedAt         int64  `json:"credited_at,omitempty"`
	ExpiresAt          *int64 `json:"expires_at,omitempty"`
	State              string `json:"state,omitempty"`
	Kind               string `json:"kind"`
	Calculation        string `json:"calculation,omitempty"`
	Amount             string `json:"amount,omitempty"`
	Currency           string `json:"currency,omitempty"`
	RatioPercent       string `json:"ratio_percent,omitempty"`
	ValidSeconds       int64  `json:"valid_seconds,omitempty"`
	PoolID             int64  `json:"pool_id,omitempty"`
	PoolName           string `json:"pool_name,omitempty"`
	Count              int64  `json:"count,omitempty"`
	ChanceMode         string `json:"chance_mode,omitempty"`
	ProbabilityPercent string `json:"probability_percent,omitempty"`
}
type MarketingAdminMilestone struct {
	Days    int                    `json:"days"`
	Rewards []MarketingAdminReward `json:"rewards"`
}
type MarketingAdminCheckin struct {
	Condition    string                    `json:"condition"`
	ThresholdCNY string                    `json:"threshold_cny,omitempty"`
	CycleDays    int                       `json:"cycle_days"`
	MakeupDays   int                       `json:"makeup_days"`
	MakeupLimit  int                       `json:"makeup_limit"`
	MakeupDaily  bool                      `json:"makeup_daily"`
	Milestones   []MarketingAdminMilestone `json:"milestones"`
}
type MarketingAdminActivityForm struct {
	Name           string                 `json:"name"`
	Kind           string                 `json:"kind"`
	Published      bool                   `json:"published"`
	StartsAt       int64                  `json:"starts_at"`
	EndsAt         int64                  `json:"ends_at"`
	Condition      string                 `json:"condition"`
	Frequency      string                 `json:"frequency"`
	ThresholdCNY   string                 `json:"threshold_cny,omitempty"`
	ExclusiveGroup string                 `json:"exclusive_group"`
	Priority       int                    `json:"priority"`
	Rewards        []MarketingAdminReward `json:"rewards"`
	Checkin        *MarketingAdminCheckin `json:"checkin"`
}
type MarketingAdminActivityView struct {
	MoneyRevision string                     `json:"money_revision"`
	ID            int64                      `json:"id"`
	Revision      int64                      `json:"revision"`
	State         string                     `json:"state"`
	EnabledAt     int64                      `json:"enabled_at"`
	Editable      bool                       `json:"editable"`
	Activity      MarketingAdminActivityForm `json:"activity"`
}
type MarketingAdminGift struct {
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	ValidSeconds int64  `json:"valid_seconds"`
}
type MarketingAdminPrize struct {
	ID     int64                `json:"id,omitempty"`
	Name   string               `json:"name"`
	Weight int64                `json:"weight"`
	Stock  int64                `json:"stock"`
	Gifts  []MarketingAdminGift `json:"gifts"`
}
type MarketingAdminPoolForm struct {
	Name     string                `json:"name"`
	Style    string                `json:"style"`
	StartsAt int64                 `json:"starts_at"`
	EndsAt   int64                 `json:"ends_at"`
	Capacity int64                 `json:"capacity"`
	Prizes   []MarketingAdminPrize `json:"prizes"`
}
type MarketingAdminPoolView struct {
	MoneyRevision      string                 `json:"money_revision"`
	ID                 int64                  `json:"id"`
	Revision           int64                  `json:"revision"`
	State              string                 `json:"state"`
	Editable           bool                   `json:"editable"`
	Issued             int64                  `json:"issued"`
	RemainingCapacity  int64                  `json:"remaining_capacity"`
	AppendableCapacity *int64                 `json:"appendable_capacity"`
	Pool               MarketingAdminPoolForm `json:"pool"`
}
type MarketingAdminRequest struct {
	MoneyRevision string                      `json:"money_revision,omitempty"`
	OperationKey  string                      `json:"operation_key"`
	Revision      *int64                      `json:"revision,omitempty"`
	Activity      *MarketingAdminActivityForm `json:"activity,omitempty"`
	Pool          *MarketingAdminPoolForm     `json:"pool,omitempty"`
	PrizeID       int64                       `json:"prize_id,omitempty"`
	Amount        int64                       `json:"amount,omitempty"`
	EndsAt        int64                       `json:"ends_at,omitempty"`
}
type MarketingAdminPage struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

var marketingAdminDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,13})(\.[0-9]{1,12})?$`)

// MarketingMoneyContext freezes all conversion inputs once per write request.
// Only Revision and Display.Currency are sent to the browser.
type MarketingMoneyContext struct {
	Display    model.MarketingMoneySnapshot
	CNYPerUnit string
	Revision   string
}

func CurrentMarketingMoneyContext() (MarketingMoneyContext, error) {
	money, err := MarketingDisplayMoney("1")
	if err != nil {
		return MarketingMoneyContext{}, err
	}
	context := MarketingMoneyContext{Display: money, CNYPerUnit: strconv.FormatFloat(operation_setting.USDExchangeRate, 'f', -1, 64)}
	evidence, err := common.Marshal(struct {
		Display    model.MarketingMoneySnapshot
		CNYPerUnit string
	}{money, context.CNYPerUnit})
	if err != nil {
		return context, err
	}
	context.Revision = fmt.Sprintf("%x", sha256.Sum256(evidence))
	return context, nil
}
func marketingAdminMoney(amount, currency string, configuration MarketingMoneyContext) (model.MarketingMoneySnapshot, error) {
	money := configuration.Display
	if currency != money.Currency {
		return money, common.NewMessage("Wallet currency changed; refresh the form and try again")
	}
	money.Amount = amount
	if _, err := money.Quota(); err != nil {
		return money, common.NewMessage("Invalid wallet amount or currency settings")
	}
	return money, nil
}
func marketingAdminDisplayAmount(quota int64, configuration MarketingMoneyContext) (string, string, error) {
	if quota < 0 || quota > common.MaxWalletQuota {
		return "", "", model.ErrMarketingInput
	}
	units, err := decimal.NewFromString(configuration.Display.QuotaPerUnit)
	if err != nil {
		return "", "", err
	}
	rate, err := decimal.NewFromString(configuration.Display.CurrencyPerUnit)
	if err != nil {
		return "", "", err
	}
	return decimal.NewFromInt(quota).Mul(rate).DivRound(units, 12).String(), configuration.Display.Currency, nil
}

func marketingAdminRewardsInput(rows []MarketingAdminReward, configuration MarketingMoneyContext) ([]model.MarketingActivityRewardSpec, error) {
	result := make([]model.MarketingActivityRewardSpec, 0, len(rows))
	for _, r := range rows {
		item := model.MarketingActivityRewardSpec{Kind: r.Kind, Calculation: r.Calculation, ValidSeconds: r.ValidSeconds, PoolID: r.PoolID, Count: r.Count, ChanceMode: r.ChanceMode}
		if r.Kind == "gift" {
			if r.ProbabilityPercent != "" {
				return nil, model.ErrMarketingInput
			}
			amount := r.Amount
			if r.Calculation != "fixed" {
				amount = "1"
				if r.Amount != "" {
					return nil, model.ErrMarketingInput
				}
			}
			money, err := marketingAdminMoney(amount, r.Currency, configuration)
			if err != nil {
				return nil, err
			}
			item.Money = money
			if r.Calculation != "fixed" {
				if !marketingAdminDecimal.MatchString(r.RatioPercent) {
					return nil, model.ErrMarketingInput
				}
				percentage, err := decimal.NewFromString(r.RatioPercent)
				if err != nil || !percentage.IsPositive() {
					return nil, model.ErrMarketingInput
				}
				item.Ratio = percentage.Div(decimal.NewFromInt(100)).String()
				if r.Calculation == "cash_ratio" {
					item.Money.Currency = "CNY"
					item.Money.CurrencyPerUnit = configuration.CNYPerUnit
					if _, err := item.Money.Quota(); err != nil {
						return nil, err
					}
				}
			} else if r.RatioPercent != "" {
				return nil, model.ErrMarketingInput
			}
		} else if r.Kind == "chance" {
			if r.Amount != "" || r.Currency != "" || r.RatioPercent != "" {
				return nil, model.ErrMarketingInput
			}
			if r.ChanceMode == "probability" {
				if !marketingAdminDecimal.MatchString(r.ProbabilityPercent) {
					return nil, model.ErrMarketingInput
				}
				percent, err := decimal.NewFromString(r.ProbabilityPercent)
				if err != nil || percent.IsNegative() || percent.GreaterThan(decimal.NewFromInt(100)) {
					return nil, model.ErrMarketingInput
				}
				ppm := percent.Mul(decimal.NewFromInt(10000))
				if !ppm.Equal(ppm.Truncate(0)) {
					return nil, model.ErrMarketingInput
				}
				item.ProbabilityPPM = ppm.IntPart()
			} else if r.ProbabilityPercent != "" {
				return nil, model.ErrMarketingInput
			}
		}
		result = append(result, item)
	}
	return result, nil
}
func marketingAdminActivityInput(form MarketingAdminActivityForm, configuration MarketingMoneyContext) (model.MarketingActivitySpec, error) {
	spec := model.MarketingActivitySpec{Name: form.Name, Kind: form.Kind, Published: form.Published, StartsAt: form.StartsAt, EndsAt: form.EndsAt, Condition: form.Condition, Frequency: form.Frequency, ExclusiveGroup: form.ExclusiveGroup, Priority: form.Priority}
	if spec.Condition == "first_payment" {
		spec.Condition = "first_successful_payment"
	}
	var err error
	if form.ThresholdCNY != "" {
		spec.ThresholdCents, err = ParseMarketingPaidCNY(form.ThresholdCNY)
		if err != nil {
			return spec, err
		}
	}
	spec.Rewards, err = marketingAdminRewardsInput(form.Rewards, configuration)
	if err != nil {
		return spec, err
	}
	if form.Checkin != nil {
		c := form.Checkin
		spec.Checkin = &model.MarketingActivityCheckinSpec{Condition: c.Condition, CycleDays: c.CycleDays, MakeupDays: c.MakeupDays, MakeupLimit: c.MakeupLimit, MakeupDaily: c.MakeupDaily}
		if c.ThresholdCNY != "" {
			spec.Checkin.ThresholdCents, err = ParseMarketingPaidCNY(c.ThresholdCNY)
			if err != nil {
				return spec, err
			}
		}
		for _, m := range c.Milestones {
			rewards, err := marketingAdminRewardsInput(m.Rewards, configuration)
			if err != nil {
				return spec, err
			}
			spec.Checkin.Milestones = append(spec.Checkin.Milestones, model.MarketingActivityMilestone{Days: m.Days, Rewards: rewards})
		}
	}
	return spec, nil
}
func marketingAdminPoolInput(form MarketingAdminPoolForm, configuration MarketingMoneyContext) (model.MarketingLotteryPoolSpec, error) {
	spec := model.MarketingLotteryPoolSpec{Name: form.Name, Style: form.Style, StartsAt: form.StartsAt, EndsAt: form.EndsAt, Capacity: form.Capacity}
	for _, p := range form.Prizes {
		prize := model.MarketingLotteryPrizeSpec{Name: p.Name, Weight: p.Weight, Stock: p.Stock}
		for _, g := range p.Gifts {
			money, err := marketingAdminMoney(g.Amount, g.Currency, configuration)
			if err != nil {
				return spec, err
			}
			prize.Gifts = append(prize.Gifts, model.MarketingLotteryGiftSpec{Money: money, ValidSeconds: g.ValidSeconds})
		}
		spec.Prizes = append(spec.Prizes, prize)
	}
	return spec, nil
}
func marketingAdminRewardsView(tx *gorm.DB, rows []model.MarketingActivityRewardSpec, configuration MarketingMoneyContext) ([]MarketingAdminReward, error) {
	result := make([]MarketingAdminReward, 0, len(rows))
	for _, r := range rows {
		item := MarketingAdminReward{Kind: r.Kind, Calculation: r.Calculation, ValidSeconds: r.ValidSeconds, PoolID: r.PoolID, Count: r.Count, ChanceMode: r.ChanceMode}
		if r.Kind == "gift" {
			if r.Calculation == "fixed" {
				quota, err := r.Money.Quota()
				if err != nil {
					return nil, err
				}
				item.Amount, item.Currency, err = marketingAdminDisplayAmount(quota, configuration)
				if err != nil {
					return nil, err
				}
			} else {
				item.Currency = configuration.Display.Currency
				ratio, err := decimal.NewFromString(r.Ratio)
				if err != nil {
					return nil, err
				}
				item.RatioPercent = ratio.Mul(decimal.NewFromInt(100)).String()
			}
		} else {
			var pool model.MarketingLotteryPool
			if err := tx.Select("id", "name").First(&pool, r.PoolID).Error; err != nil {
				return nil, err
			}
			item.PoolName = pool.Name
			if r.ChanceMode == "probability" {
				item.ProbabilityPercent = decimal.NewFromInt(r.ProbabilityPPM).Div(decimal.NewFromInt(10000)).String()
			}
		}
		result = append(result, item)
	}
	return result, nil
}
func marketingAdminActivityView(tx *gorm.DB, row model.MarketingActivity, now int64) (MarketingAdminActivityView, error) {
	view := MarketingAdminActivityView{ID: row.ID, Revision: row.Revision, EnabledAt: row.EnabledAt, Editable: row.EnabledAt == 0 || now < row.StartsAt, State: "draft"}
	configuration, err := CurrentMarketingMoneyContext()
	if err != nil {
		return view, err
	}
	view.MoneyRevision = configuration.Revision
	var spec model.MarketingActivitySpec
	if err := common.UnmarshalJsonStr(row.Spec, &spec); err != nil {
		return view, err
	}
	f := MarketingAdminActivityForm{Name: spec.Name, Kind: spec.Kind, Published: row.Published, StartsAt: spec.StartsAt, EndsAt: spec.EndsAt, Condition: spec.Condition, Frequency: spec.Frequency, ExclusiveGroup: spec.ExclusiveGroup, Priority: spec.Priority}
	if f.Condition == "first_successful_payment" {
		f.Condition = "first_payment"
	}
	if spec.ThresholdCents > 0 {
		f.ThresholdCNY = decimal.NewFromInt(spec.ThresholdCents).Shift(-2).String()
	}
	f.Rewards, err = marketingAdminRewardsView(tx, spec.Rewards, configuration)
	if err != nil {
		return view, err
	}
	if spec.Checkin != nil {
		c := spec.Checkin
		f.Checkin = &MarketingAdminCheckin{Condition: c.Condition, CycleDays: c.CycleDays, MakeupDays: c.MakeupDays, MakeupLimit: c.MakeupLimit, MakeupDaily: c.MakeupDaily, Milestones: []MarketingAdminMilestone{}}
		if c.ThresholdCents > 0 {
			f.Checkin.ThresholdCNY = decimal.NewFromInt(c.ThresholdCents).Shift(-2).String()
		}
		for _, m := range c.Milestones {
			rewards, err := marketingAdminRewardsView(tx, m.Rewards, configuration)
			if err != nil {
				return view, err
			}
			f.Checkin.Milestones = append(f.Checkin.Milestones, MarketingAdminMilestone{Days: m.Days, Rewards: rewards})
		}
	}
	view.Activity = f
	if row.EnabledAt > 0 {
		view.State = "active"
		if now < row.StartsAt {
			view.State = "scheduled"
		}
		if row.Paused {
			view.State = "paused"
		}
		if row.EndsAt > 0 && now >= row.EndsAt {
			view.State = "ended"
		}
	}
	return view, nil
}
func marketingAdminPoolView(tx *gorm.DB, row model.MarketingLotteryPool, now int64) (MarketingAdminPoolView, error) {
	view := MarketingAdminPoolView{ID: row.ID, Revision: row.Revision, State: "active", Editable: now < row.StartsAt && row.Issued == 0, Issued: row.Issued, RemainingCapacity: row.Capacity - row.Issued, Pool: MarketingAdminPoolForm{Name: row.Name, Style: row.Style, StartsAt: row.StartsAt, EndsAt: row.EndsAt, Capacity: row.Capacity, Prizes: []MarketingAdminPrize{}}}
	configuration, err := CurrentMarketingMoneyContext()
	if err != nil {
		return view, err
	}
	view.MoneyRevision = configuration.Revision
	if now < row.StartsAt {
		view.State = "scheduled"
	}
	if row.Paused {
		view.State = "paused"
	}
	if now >= row.EndsAt {
		view.State = "ended"
	}
	var prizes []model.MarketingLotteryPrize
	if err := tx.Where("pool_id = ?", row.ID).Order("id").Find(&prizes).Error; err != nil {
		return view, err
	}
	supply := int64(0)
	unlimited := false
	for _, p := range prizes {
		item := MarketingAdminPrize{ID: p.ID, Name: p.Name, Weight: p.Weight, Stock: p.Stock, Gifts: []MarketingAdminGift{}}
		var gifts []model.MarketingLotteryGiftSpec
		if err := common.UnmarshalJsonStr(p.Gifts, &gifts); err != nil {
			return view, err
		}
		for _, g := range gifts {
			quota, err := g.Money.Quota()
			if err != nil {
				return view, err
			}
			amount, currency, err := marketingAdminDisplayAmount(quota, configuration)
			if err != nil {
				return view, err
			}
			item.Gifts = append(item.Gifts, MarketingAdminGift{Amount: amount, Currency: currency, ValidSeconds: g.ValidSeconds})
		}
		if p.Weight > 0 && len(gifts) > 0 {
			if p.Stock == -1 {
				unlimited = true
			} else {
				if p.Stock > common.MaxWalletQuota-supply {
					supply = common.MaxWalletQuota
				} else {
					supply += p.Stock
				}
			}
		}
		view.Pool.Prizes = append(view.Pool.Prizes, item)
	}
	if !unlimited {
		var outstanding int64
		if err := tx.Model(&model.MarketingLotteryChance{}).Where("pool_id = ? AND state = ? AND expires_at > ?", row.ID, "ready", now).Select("COALESCE(SUM(remaining), 0)").Scan(&outstanding).Error; err != nil {
			return view, err
		}
		available := max(int64(0), supply-outstanding-view.RemainingCapacity)
		view.AppendableCapacity = &available
	}
	return view, nil
}

func ReadMarketingAdmin(ctx context.Context, actorID int, resource string, id int64, filter model.MarketingAdminFilter) (any, error) {
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		if err := model.AuthorizeMarketingAdmin(tx, actorID); err != nil {
			return err
		}
		now := time.Now().Unix()
		if resource == "metadata" {
			configuration, err := CurrentMarketingMoneyContext()
			if err != nil {
				return err
			}
			result = map[string]string{"currency": configuration.Display.Currency, "money_revision": configuration.Revision}
			return nil
		}
		if resource == "groups" && id == 0 {
			rows, total, err := model.ListMarketingAdminGroups(tx, filter)
			if err != nil {
				return err
			}
			result = MarketingAdminPage{Items: rows, Total: total, Page: filter.Page, PageSize: filter.PageSize}
			return nil
		}
		if resource == "activities" {
			if id > 0 {
				var row model.MarketingActivity
				if err := tx.First(&row, id).Error; err != nil {
					return err
				}
				v, err := marketingAdminActivityView(tx, row, now)
				result = v
				return err
			}
			rows, total, err := model.ListMarketingAdminActivities(tx, filter)
			if err != nil {
				return err
			}
			views := make([]MarketingAdminActivityView, 0, len(rows))
			for _, row := range rows {
				v, err := marketingAdminActivityView(tx, row, now)
				if err != nil {
					return err
				}
				views = append(views, v)
			}
			result = MarketingAdminPage{Items: views, Total: total, Page: filter.Page, PageSize: filter.PageSize}
			return nil
		}
		if resource == "pools" {
			if id > 0 {
				var row model.MarketingLotteryPool
				if err := tx.First(&row, id).Error; err != nil {
					return err
				}
				v, err := marketingAdminPoolView(tx, row, now)
				result = v
				return err
			}
			rows, total, err := model.ListMarketingAdminPools(tx, filter)
			if err != nil {
				return err
			}
			views := make([]MarketingAdminPoolView, 0, len(rows))
			for _, row := range rows {
				v, err := marketingAdminPoolView(tx, row, now)
				if err != nil {
					return err
				}
				views = append(views, v)
			}
			result = MarketingAdminPage{Items: views, Total: total, Page: filter.Page, PageSize: filter.PageSize}
			return nil
		}
		return model.ErrMarketingInput
	})
	return result, err
}
func WriteMarketingAdmin(ctx context.Context, actorID int, resource, action string, id int64, input MarketingAdminRequest) (any, error) {
	if id > 0 && input.Revision == nil {
		return nil, model.ErrMarketingInput
	}
	if resource != "activities" && resource != "pools" {
		return nil, model.ErrMarketingInput
	}
	if id == 0 && action != "create" {
		return nil, model.ErrMarketingInput
	}
	revision := int64(0)
	if input.Revision != nil {
		revision = *input.Revision
	}
	var result any
	err := model.WithMarketingTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().Unix()
		resultID, err := model.ApplyMarketingAdminRequest(tx, actorID, resource == "pools", input.OperationKey, action, id, revision, input, now, func(reference string) (int64, error) {
			configuration, err := CurrentMarketingMoneyContext()
			if err != nil {
				return 0, err
			}
			if (action == "create" || action == "update") && input.MoneyRevision != configuration.Revision {
				return 0, common.NewMessage("Wallet currency changed; refresh the form and try again")
			}
			if resource == "activities" {
				var row *model.MarketingActivity
				var err error
				if action == "create" || action == "update" {
					if input.Activity == nil || input.Pool != nil {
						return 0, model.ErrMarketingInput
					}
					spec, err := marketingAdminActivityInput(*input.Activity, configuration)
					if err != nil {
						return 0, err
					}
					if action == "create" {
						row, err = model.CreateMarketingActivity(tx, actorID, reference, spec, now)
					} else {
						row, err = model.UpdateMarketingActivity(tx, actorID, id, reference, spec, now)
					}
					if err != nil {
						return 0, err
					}
					return row.ID, nil
				}
				if input.Activity != nil || input.Pool != nil || input.PrizeID != 0 || input.Amount != 0 || input.EndsAt != 0 {
					return 0, model.ErrMarketingInput
				}
				switch action {
				case "activate":
					row, err = model.ActivateMarketingActivity(tx, actorID, id, reference, now)
				case "pause", "resume":
					row, err = model.SetMarketingActivityPaused(tx, actorID, id, reference, action == "pause", now)
				case "publish", "unpublish":
					row, err = model.SetMarketingActivityPublished(tx, actorID, id, reference, action == "publish", now)
				default:
					return 0, model.ErrMarketingInput
				}
				if err != nil {
					return 0, err
				}
				return row.ID, nil
			}
			if action == "create" || action == "update" {
				if input.Pool == nil || input.Activity != nil {
					return 0, model.ErrMarketingInput
				}
				spec, err := marketingAdminPoolInput(*input.Pool, configuration)
				if err != nil {
					return 0, err
				}
				var row *model.MarketingLotteryPool
				if action == "create" {
					row, err = model.CreateMarketingLotteryPool(tx, actorID, reference, spec, now)
				} else {
					row, err = model.UpdateMarketingLotteryPool(tx, actorID, id, reference, spec, now)
				}
				if err != nil {
					return 0, err
				}
				return row.ID, nil
			}
			if input.Pool != nil || input.Activity != nil {
				return 0, model.ErrMarketingInput
			}
			amount := input.Amount
			switch action {
			case "extend":
				if input.Amount != 0 || input.PrizeID != 0 {
					return 0, model.ErrMarketingInput
				}
				amount = input.EndsAt
			case "restock":
				if input.EndsAt != 0 {
					return 0, model.ErrMarketingInput
				}
			case "append":
				if input.EndsAt != 0 || input.PrizeID != 0 {
					return 0, model.ErrMarketingInput
				}
			case "pause", "resume":
				if input.EndsAt != 0 || input.Amount != 0 || input.PrizeID != 0 {
					return 0, model.ErrMarketingInput
				}
			default:
				return 0, model.ErrMarketingInput
			}
			row, err := model.MutateMarketingLotteryPool(tx, actorID, id, reference, action, input.PrizeID, amount, now)
			if err != nil {
				return 0, err
			}
			return row.ID, nil
		})
		if err != nil {
			return err
		}
		if resource == "activities" {
			var row model.MarketingActivity
			if err := tx.First(&row, resultID).Error; err != nil {
				return err
			}
			view, err := marketingAdminActivityView(tx, row, now)
			result = view
			return err
		}
		var row model.MarketingLotteryPool
		if err := tx.First(&row, resultID).Error; err != nil {
			return err
		}
		view, err := marketingAdminPoolView(tx, row, now)
		result = view
		return err
	})
	return result, err
}

// Preserve typed errors for the controller while hiding database messages and
// frozen accounting payloads from the public management API.
func MarketingAdminError(err error) (int, string) {
	switch {
	case errors.Is(err, model.ErrMarketingAccess):
		return 403, "Access Forbidden"
	case errors.Is(err, gorm.ErrRecordNotFound):
		return 404, "Marketing record not found"
	case errors.Is(err, model.ErrMarketingConflict):
		return 409, "This operation conflicts with an existing record"
	case errors.Is(err, model.ErrMarketingInsufficient):
		return 409, "Prize supply is insufficient for these opportunities"
	case errors.Is(err, model.ErrMarketingInput):
		return 400, "Invalid marketing configuration"
	}
	var message *common.Message
	if errors.As(err, &message) {
		if message.Key == "Wallet currency changed; refresh the form and try again" {
			return 409, message.Key
		}
		return 400, message.Key
	}
	return 500, "Unable to process marketing configuration"
}
