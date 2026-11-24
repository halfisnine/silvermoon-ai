package model

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Pool issuance is monotone. Unused opportunities and pending awards are durable
// obligations; Redis is never the authority for stock, randomness, or recovery.
type MarketingLotteryPool struct {
	ID       int64  `gorm:"primaryKey"`
	Name     string `gorm:"size:160;not null"`
	Style    string `gorm:"size:16;not null"`
	StartsAt int64  `gorm:"not null"`
	EndsAt   int64  `gorm:"not null"`
	Capacity int64  `gorm:"not null"`
	Issued   int64  `gorm:"not null"`
	Paused   bool   `gorm:"not null"`
	Revision int64  `gorm:"not null"`
}

func (MarketingLotteryPool) TableName() string { return "marketing_v2_lottery_pools" }

type MarketingLotteryPrize struct {
	ID     int64  `gorm:"primaryKey"`
	PoolID int64  `gorm:"not null;index"`
	Name   string `gorm:"size:160;not null"`
	Weight int64  `gorm:"not null"`
	Stock  int64  `gorm:"not null"` // -1 means unlimited; weight remains independent.
	Gifts  string `gorm:"type:text;not null"`
}

func (MarketingLotteryPrize) TableName() string { return "marketing_v2_lottery_prizes" }

type MarketingLotteryIssue struct {
	ID              int64  `gorm:"primaryKey"`
	SourceKey       string `gorm:"size:64;not null;uniqueIndex"`
	SourceReference string `gorm:"size:128;not null"`
	PayloadHash     string `gorm:"size:64;not null"`
	UserID          int    `gorm:"not null;index"`
	PoolID          int64  `gorm:"not null;index"`
	Count           int64  `gorm:"not null"`
	State           string `gorm:"size:16;not null"`
	RecordedAt      int64  `gorm:"not null"`
}

func (MarketingLotteryIssue) TableName() string { return "marketing_v2_lottery_issues" }

type MarketingLotteryChance struct {
	ID        int64  `gorm:"primaryKey"`
	UserID    int    `gorm:"not null;index"`
	PoolID    int64  `gorm:"not null;index:idx_mv2_chance_supply,priority:1"`
	IssueID   int64  `gorm:"not null;uniqueIndex"`
	Granted   int64  `gorm:"not null"`
	Remaining int64  `gorm:"not null"`
	Cleared   int64  `gorm:"not null"`
	State     string `gorm:"size:16;not null;index:idx_mv2_chance_supply,priority:2"`
	ExpiresAt int64  `gorm:"not null;index:idx_mv2_chance_supply,priority:3"`
}

func (MarketingLotteryChance) TableName() string { return "marketing_v2_lottery_chances" }

type MarketingLotteryDraw struct {
	ID              int64  `gorm:"primaryKey;index:idx_mv2_draw_pending,priority:2"`
	ChanceID        int64  `gorm:"not null;index"`
	RequestKey      string `gorm:"size:64;not null;uniqueIndex"`
	SourceReference string `gorm:"size:128;not null"`
	UserID          int    `gorm:"not null;index"`
	PoolID          int64  `gorm:"not null;index"`
	PrizeID         int64  `gorm:"not null"`
	PrizeSnapshot   string `gorm:"type:text;not null"`
	State           string `gorm:"size:16;not null;index:idx_mv2_draw_pending,priority:1"`
	DrawnAt         int64  `gorm:"not null"`
}

func (MarketingLotteryDraw) TableName() string { return "marketing_v2_lottery_draws" }

type MarketingLotteryAward struct {
	ID         int64  `gorm:"primaryKey"`
	DrawID     int64  `gorm:"not null;index"`
	UserID     int    `gorm:"not null;index"`
	PoolID     int64  `gorm:"not null;index"`
	Position   int    `gorm:"not null"`
	GiftSpec   string `gorm:"type:text;not null"`
	State      string `gorm:"size:16;not null"`
	GiftID     int64  `gorm:"not null"`
	CreditedAt int64  `gorm:"not null"`
}

func (MarketingLotteryAward) TableName() string { return "marketing_v2_lottery_awards" }

type MarketingLotteryOperation struct {
	ID           int64  `gorm:"primaryKey"`
	OperationKey string `gorm:"size:64;not null;uniqueIndex"`
	PayloadHash  string `gorm:"size:64;not null"`
	ActorID      int    `gorm:"not null"`
	PoolID       int64  `gorm:"not null;index"`
	Kind         string `gorm:"size:24;not null"`
	Details      string `gorm:"type:text;not null"`
	RecordedAt   int64  `gorm:"not null"`
}

func (MarketingLotteryOperation) TableName() string { return "marketing_v2_lottery_operations" }

type MarketingLotteryGiftSpec struct {
	Money        MarketingMoneySnapshot
	ValidSeconds int64
}
type MarketingLotteryPrizeSpec struct {
	Name          string
	Weight, Stock int64
	Gifts         []MarketingLotteryGiftSpec
}
type MarketingLotteryPoolSpec struct {
	Name, Style                string
	StartsAt, EndsAt, Capacity int64
	Prizes                     []MarketingLotteryPrizeSpec
}

func lockMarketingLotteryActor(tx *gorm.DB, userID int, admin bool) error {
	if tx == nil || tx.Statement == nil {
		return ErrMarketingTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrMarketingTransaction
	}
	if userID <= 0 {
		return ErrMarketingInput
	}
	var user User
	if err := lockForUpdate(tx.Session(&gorm.Session{})).First(&user, userID).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled || user.DeletedAt.Valid || (admin && user.Role < common.RoleAdminUser) {
		return ErrMarketingAccess
	}
	return nil
}
func lockMarketingLotteryPool(tx *gorm.DB, id int64) (*MarketingLotteryPool, error) {
	var pool MarketingLotteryPool
	if err := lockForUpdate(tx).First(&pool, id).Error; err != nil {
		return nil, err
	}
	return &pool, nil
}
func marketingLotteryPrizes(tx *gorm.DB, poolID int64) ([]MarketingLotteryPrize, error) {
	var prizes []MarketingLotteryPrize
	err := lockForUpdate(tx).Where("pool_id = ?", poolID).Order("id").Find(&prizes).Error
	return prizes, err
}

// Shape limits bound configuration payloads, not a user's award/issuance count.
func validateMarketingLotterySpec(spec MarketingLotteryPoolSpec, now int64) error {
	if now <= 0 || strings.TrimSpace(spec.Name) == "" || len(spec.Name) > 160 || (spec.Style != "wheel" && spec.Style != "blind_box") || spec.StartsAt <= 0 || spec.EndsAt <= spec.StartsAt || spec.EndsAt <= now || spec.Capacity <= 0 || len(spec.Prizes) == 0 || len(spec.Prizes) > 100 {
		return ErrMarketingInput
	}
	var weight int64
	for _, p := range spec.Prizes {
		if strings.TrimSpace(p.Name) == "" || len(p.Name) > 160 || p.Weight < 0 || p.Weight > math.MaxInt64-weight || p.Stock < -1 || len(p.Gifts) > 20 {
			return ErrMarketingInput
		}
		weight += p.Weight
		for _, g := range p.Gifts {
			if _, err := g.Money.Quota(); err != nil {
				return err
			}
			if g.ValidSeconds < 0 || g.ValidSeconds > math.MaxInt64-spec.EndsAt {
				return ErrMarketingInput
			}
		}
	}
	if weight == 0 {
		return ErrMarketingInput
	}
	return nil
}

// Supply excludes empty/zero-weight slots, and counts a combination only once.
func checkMarketingLotterySupply(prizes []MarketingLotteryPrize, required int64) error {
	if required <= 0 {
		return nil
	}
	for _, p := range prizes {
		var gifts []MarketingLotteryGiftSpec
		if err := common.UnmarshalJsonStr(p.Gifts, &gifts); err != nil {
			return err
		}
		if p.Weight <= 0 || len(gifts) == 0 {
			continue
		}
		if p.Stock == -1 || p.Stock >= required {
			return nil
		}
		required -= p.Stock
	}
	return ErrMarketingInsufficient
}
func marketingLotteryOperation(tx *gorm.DB, actorID int, reference, kind string, payload any) (*MarketingLotteryOperation, bool, error) {
	key, err := marketingSourceKey("lottery_admin", reference)
	if err != nil {
		return nil, false, err
	}
	hash, err := marketingDigest(payload)
	if err != nil {
		return nil, false, err
	}
	var op MarketingLotteryOperation
	err = lockForUpdate(tx).Where("operation_key = ?", key).First(&op).Error
	if err == nil {
		if op.ActorID != actorID || op.Kind != kind || op.PayloadHash != hash {
			return nil, false, ErrMarketingConflict
		}
		return &op, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	details, err := common.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return &MarketingLotteryOperation{OperationKey: key, PayloadHash: hash, ActorID: actorID, Kind: kind, Details: string(details)}, false, nil
}
func CreateMarketingLotteryPool(tx *gorm.DB, actorID int, reference string, spec MarketingLotteryPoolSpec, now int64) (*MarketingLotteryPool, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	op, replay, err := marketingLotteryOperation(tx, actorID, reference, "create", spec)
	if err != nil {
		return nil, err
	}
	if replay {
		return lockMarketingLotteryPool(tx, op.PoolID)
	}
	if err := validateMarketingLotterySpec(spec, now); err != nil {
		return nil, err
	}
	pool := &MarketingLotteryPool{Name: spec.Name, Style: spec.Style, StartsAt: spec.StartsAt, EndsAt: spec.EndsAt, Capacity: spec.Capacity}
	if err := tx.Create(pool).Error; err != nil {
		return nil, err
	}
	prizes := make([]MarketingLotteryPrize, 0, len(spec.Prizes))
	for _, p := range spec.Prizes {
		data, err := common.Marshal(p.Gifts)
		if err != nil {
			return nil, err
		}
		prizes = append(prizes, MarketingLotteryPrize{PoolID: pool.ID, Name: p.Name, Weight: p.Weight, Stock: p.Stock, Gifts: string(data)})
	}
	if err := checkMarketingLotterySupply(prizes, pool.Capacity); err != nil {
		return nil, err
	}
	if err := tx.Create(&prizes).Error; err != nil {
		return nil, err
	}
	op.PoolID = pool.ID
	op.RecordedAt = now
	return pool, tx.Create(op).Error
}

// Updating an unstarted pool with promises is deliberately restricted to its
// immutable core too. Restock/append/extend remain available as audited actions.
func UpdateMarketingLotteryPool(tx *gorm.DB, actorID int, poolID int64, reference string, spec MarketingLotteryPoolSpec, now int64) (*MarketingLotteryPool, error) {
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	pool, err := lockMarketingLotteryPool(tx, poolID)
	if err != nil {
		return nil, err
	}
	payload := struct {
		PoolID int64
		Spec   MarketingLotteryPoolSpec
	}{poolID, spec}
	op, replay, err := marketingLotteryOperation(tx, actorID, reference, "update", payload)
	if err != nil {
		return nil, err
	}
	if replay {
		return pool, nil
	}
	if now >= pool.StartsAt || pool.Issued > 0 {
		return nil, ErrMarketingConflict
	}
	if err := validateMarketingLotterySpec(spec, now); err != nil {
		return nil, err
	}
	if spec.EndsAt < pool.EndsAt {
		return nil, ErrMarketingConflict
	}
	prizes := make([]MarketingLotteryPrize, 0, len(spec.Prizes))
	for _, p := range spec.Prizes {
		data, err := common.Marshal(p.Gifts)
		if err != nil {
			return nil, err
		}
		prizes = append(prizes, MarketingLotteryPrize{PoolID: pool.ID, Name: p.Name, Weight: p.Weight, Stock: p.Stock, Gifts: string(data)})
	}
	if err := checkMarketingLotterySupply(prizes, spec.Capacity); err != nil {
		return nil, err
	}
	if err := tx.Where("pool_id = ?", poolID).Delete(&MarketingLotteryPrize{}).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&prizes).Error; err != nil {
		return nil, err
	}
	pool.Name = spec.Name
	pool.Style = spec.Style
	pool.StartsAt = spec.StartsAt
	pool.EndsAt = spec.EndsAt
	pool.Capacity = spec.Capacity
	pool.Revision++
	if err := tx.Save(pool).Error; err != nil {
		return nil, err
	}
	op.PoolID = poolID
	op.RecordedAt = now
	return pool, tx.Create(op).Error
}

func MutateMarketingLotteryPool(tx *gorm.DB, actorID int, poolID int64, reference, kind string, prizeID, amount, now int64) (*MarketingLotteryPool, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return nil, err
	}
	pool, err := lockMarketingLotteryPool(tx, poolID)
	if err != nil {
		return nil, err
	}
	payload := struct{ PoolID, PrizeID, Amount int64 }{poolID, prizeID, amount}
	op, replay, err := marketingLotteryOperation(tx, actorID, reference, kind, payload)
	if err != nil {
		return nil, err
	}
	if replay {
		return pool, nil
	}
	prizes, err := marketingLotteryPrizes(tx, poolID)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "pause":
		pool.Paused = true
	case "resume":
		pool.Paused = false
	case "restock":
		found := false
		for i := range prizes {
			p := &prizes[i]
			if p.ID != prizeID {
				continue
			}
			found = true
			if p.Stock < 0 || amount <= 0 || amount > math.MaxInt64-p.Stock {
				return nil, ErrMarketingInput
			}
			p.Stock += amount
			if err := tx.Save(p).Error; err != nil {
				return nil, err
			}
		}
		if !found {
			return nil, ErrMarketingInput
		}
	case "append":
		if amount <= 0 || amount > math.MaxInt64-pool.Capacity || now >= pool.EndsAt {
			return nil, ErrMarketingInput
		}
		var outstanding int64
		var promises []MarketingLotteryChance
		if err := lockForUpdate(tx).Where("pool_id = ? AND state = ? AND expires_at > ?", poolID, "ready", now).Order("id").Find(&promises).Error; err != nil {
			return nil, err
		}
		for _, promise := range promises {
			if promise.Remaining > math.MaxInt64-outstanding {
				return nil, ErrMarketingInput
			}
			outstanding += promise.Remaining
		}
		remaining := pool.Capacity - pool.Issued
		if outstanding > math.MaxInt64-remaining || amount > math.MaxInt64-remaining-outstanding {
			return nil, ErrMarketingInput
		}
		if err := checkMarketingLotterySupply(prizes, remaining+outstanding+amount); err != nil {
			return nil, err
		}
		pool.Capacity += amount
	case "extend":
		if now >= pool.EndsAt || amount <= pool.EndsAt {
			return nil, ErrMarketingInput
		}
		// Draw and clear take this same pool lock. Exact expiry cannot be revived.
		if err := tx.Model(&MarketingLotteryChance{}).Where("pool_id = ? AND state = ? AND expires_at > ?", poolID, "ready", now).Update("expires_at", amount).Error; err != nil {
			return nil, err
		}
		pool.EndsAt = amount
	default:
		return nil, ErrMarketingInput
	}
	pool.Revision++
	if err := tx.Save(pool).Error; err != nil {
		return nil, err
	}
	op.PoolID = poolID
	op.RecordedAt = now
	return pool, tx.Create(op).Error
}

func IssueMarketingLotteryChances(tx *gorm.DB, userID int, poolID int64, reference string, count, now int64) (*MarketingLotteryIssue, error) {
	return issueMarketingLotteryChances(tx, userID, poolID, reference, count, now, 0)
}

// AtCapacity freezes the eligible issuance tranche of an earlier activity fact.
// It reserves nothing before qualification and never spends later-added slots.
func IssueMarketingLotteryChancesAtCapacity(tx *gorm.DB, userID int, poolID int64, reference string, count, now, capacity int64) (*MarketingLotteryIssue, error) {
	if capacity <= 0 {
		return nil, ErrMarketingInput
	}
	return issueMarketingLotteryChances(tx, userID, poolID, reference, count, now, capacity)
}
func issueMarketingLotteryChances(tx *gorm.DB, userID int, poolID int64, reference string, count, now, capacity int64) (*MarketingLotteryIssue, error) {
	if count <= 0 || now <= 0 {
		return nil, ErrMarketingInput
	}
	if capacity > 0 {
		if err := lockMarketingActivityUsers(tx, []int{userID}); err != nil {
			return nil, err
		}
	} else if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, err
	}
	pool, err := lockMarketingLotteryPool(tx, poolID)
	if err != nil {
		return nil, err
	}
	key, err := marketingSourceKey("lottery_issue", reference)
	if err != nil {
		return nil, err
	}
	hash, err := marketingDigest(struct {
		UserID        int
		PoolID, Count int64
	}{userID, poolID, count})
	if err != nil {
		return nil, err
	}
	if capacity > 0 {
		hash, err = marketingDigest(struct {
			UserID                  int
			PoolID, Count, Capacity int64
		}{userID, poolID, count, capacity})
		if err != nil {
			return nil, err
		}
	}
	var issue MarketingLotteryIssue
	err = lockForUpdate(tx).Where("source_key = ?", key).First(&issue).Error
	if err == nil {
		if issue.PayloadHash != hash {
			return nil, ErrMarketingConflict
		}
		return &issue, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	issue = MarketingLotteryIssue{SourceKey: key, SourceReference: reference, PayloadHash: hash, UserID: userID, PoolID: poolID, Count: count, State: "shortage", RecordedAt: now}
	// Pausing stops draws, not activity-funded issuance. New opportunities
	// still consume finite capacity and keep the original pool deadline.
	if now >= pool.EndsAt {
		return nil, ErrMarketingAccess
	}
	available := pool.Capacity - pool.Issued
	if capacity > 0 {
		available = min(pool.Capacity, capacity) - pool.Issued
	}
	if count <= available {
		issue.State = "issued"
	}
	if err := tx.Create(&issue).Error; err != nil {
		return nil, err
	}
	if issue.State == "shortage" {
		return &issue, nil
	} // Commit this outcome, never retry it as a fresh award.
	chance := MarketingLotteryChance{UserID: userID, PoolID: poolID, IssueID: issue.ID, State: "ready", ExpiresAt: pool.EndsAt, Granted: count, Remaining: count}
	if err := tx.Create(&chance).Error; err != nil {
		return nil, err
	}
	pool.Issued += count
	pool.Revision++
	return &issue, tx.Save(pool).Error
}

// Draw commits the outcome and every gift obligation before any wallet credit.
// Callers must commit successfully before they begin fulfilling awards.
func DrawMarketingLotteryChance(tx *gorm.DB, userID int, poolID int64, reference string, now int64) (*MarketingLotteryDraw, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, err
	}
	pool, err := lockMarketingLotteryPool(tx, poolID)
	if err != nil {
		return nil, err
	}
	key, err := marketingSourceKey("lottery_draw", reference)
	if err != nil {
		return nil, err
	}
	var draw MarketingLotteryDraw
	err = lockForUpdate(tx).Where("request_key = ?", key).First(&draw).Error
	if err == nil {
		if draw.UserID != userID || draw.PoolID != poolID {
			return nil, ErrMarketingConflict
		}
		return &draw, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if now < pool.StartsAt || now >= pool.EndsAt || pool.Paused {
		return nil, ErrMarketingAccess
	}
	var chance MarketingLotteryChance
	err = lockForUpdate(tx).Where("user_id = ? AND pool_id = ? AND state = ? AND remaining > 0 AND expires_at > ?", userID, poolID, "ready", now).Order("expires_at, id").First(&chance).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMarketingAccess
	}
	if err != nil {
		return nil, err
	}
	prizes, err := marketingLotteryPrizes(tx, pool.ID)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, p := range prizes {
		if p.Stock == 0 || p.Weight <= 0 {
			continue
		}
		if p.Weight > math.MaxInt64-total {
			return nil, ErrMarketingInput
		}
		total += p.Weight
	}
	if total <= 0 {
		return nil, ErrMarketingInsufficient
	}
	random, err := rand.Int(rand.Reader, big.NewInt(total))
	if err != nil {
		return nil, err
	}
	ticket := random.Int64()
	var chosen *MarketingLotteryPrize
	for i := range prizes {
		p := &prizes[i]
		if p.Stock == 0 || p.Weight <= 0 {
			continue
		}
		if ticket < p.Weight {
			chosen = p
			break
		}
		ticket -= p.Weight
	}
	if chosen == nil {
		return nil, ErrMarketingConflict
	}
	var gifts []MarketingLotteryGiftSpec
	if err := common.UnmarshalJsonStr(chosen.Gifts, &gifts); err != nil {
		return nil, err
	}
	snapshot, err := common.Marshal(chosen)
	if err != nil {
		return nil, err
	}
	draw = MarketingLotteryDraw{ChanceID: chance.ID, RequestKey: key, SourceReference: reference, UserID: userID, PoolID: pool.ID, PrizeID: chosen.ID, PrizeSnapshot: string(snapshot), State: "pending", DrawnAt: now}
	if len(gifts) == 0 {
		draw.State = "complete"
	}
	if err := tx.Create(&draw).Error; err != nil {
		return nil, err
	}
	for i, gift := range gifts {
		data, err := common.Marshal(gift)
		if err != nil {
			return nil, err
		}
		award := MarketingLotteryAward{DrawID: draw.ID, UserID: userID, PoolID: pool.ID, Position: i, GiftSpec: string(data), State: "pending"}
		if err := tx.Create(&award).Error; err != nil {
			return nil, err
		}
	}
	if chosen.Stock > 0 {
		chosen.Stock--
		if err := tx.Save(chosen).Error; err != nil {
			return nil, err
		}
	}
	chance.Remaining--
	if chance.Remaining == 0 {
		chance.State = "used"
	}
	if err := tx.Save(&chance).Error; err != nil {
		return nil, err
	}
	pool.Revision++
	return &draw, tx.Save(pool).Error
}

func FulfillMarketingLotteryAward(tx *gorm.DB, userID int, awardID, now int64) (*MarketingLotteryAward, error) {
	if now <= 0 {
		return nil, ErrMarketingInput
	}
	if err := lockMarketingLotteryActor(tx, userID, false); err != nil {
		return nil, err
	}
	// The committed draw already consumed stock and froze every gift spec.
	// Delivery needs only its beneficiary lock, never the shared pool hot row.
	var award MarketingLotteryAward
	if err := lockForUpdate(tx).First(&award, awardID).Error; err != nil {
		return nil, err
	}
	if award.UserID != userID {
		return nil, ErrMarketingAccess
	}
	if award.State == "credited" {
		return &award, nil
	}
	if award.State != "pending" {
		return nil, ErrMarketingAccess
	}
	var spec MarketingLotteryGiftSpec
	if err := common.UnmarshalJsonStr(award.GiftSpec, &spec); err != nil {
		return nil, err
	}
	expires := int64(0)
	if spec.ValidSeconds > 0 {
		if now > math.MaxInt64-spec.ValidSeconds {
			return nil, ErrMarketingInput
		}
		expires = now + spec.ValidSeconds
	}
	gift, err := GrantMarketingGift(tx, MarketingGiftInput{UserID: userID, Kind: "lottery", Reference: fmt.Sprintf("lottery-award:%d", award.ID), GrantedAt: now, ExpiresAt: expires, Money: spec.Money})
	if err != nil {
		return nil, err
	}
	award.State = "credited"
	award.GiftID = gift.ID
	award.CreditedAt = gift.GrantedAt
	if err := tx.Save(&award).Error; err != nil {
		return nil, err
	}
	var pending []MarketingLotteryAward
	if err := lockForUpdate(tx).Where("draw_id = ? AND state = ?", award.DrawID, "pending").Find(&pending).Error; err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		if err := tx.Model(&MarketingLotteryDraw{}).Where("id = ? AND state = ?", award.DrawID, "pending").Update("state", "complete").Error; err != nil {
			return nil, err
		}
	}
	return &award, nil
}

// Refund orchestration calls this in its transaction after disabling new usage.
// Already credited gifts are cleared by the wallet refund operation, not here.
func ClearMarketingLotteryChances(tx *gorm.DB, actorID, userID int, reference string, now int64) error {
	if now <= 0 {
		return ErrMarketingInput
	}
	if tx == nil || tx.Statement == nil {
		return ErrMarketingTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrMarketingTransaction
	}
	if actorID <= 0 || userID <= 0 {
		return ErrMarketingInput
	}
	// Lock users in ID order, avoiding admin/beneficiary cross-operation deadlocks.
	for _, id := range []int{min(actorID, userID), max(actorID, userID)} {
		if id == userID {
			var user User
			if err := lockForUpdate(tx).First(&user, id).Error; err != nil {
				return err
			}
		} else if err := lockMarketingLotteryActor(tx, id, true); err != nil {
			return err
		}
	}
	// Even self-clear must check current administrator authorization on replay.
	if err := lockMarketingLotteryActor(tx, actorID, true); err != nil {
		return err
	}
	op, replay, err := marketingLotteryOperation(tx, actorID, reference, "clear", struct{ UserID int }{userID})
	if err != nil {
		return err
	}
	if replay {
		return nil
	}
	// Refund is rare. Lock pools in the same stable order as other mutations,
	// avoiding both stale MySQL snapshot discovery and inverted batch row locks.
	// The beneficiary user lock prevents issuance from a concurrently new pool.
	var pools []MarketingLotteryPool
	if err := lockForUpdate(tx).Order("id").Find(&pools).Error; err != nil {
		return err
	}
	if err := tx.Model(&MarketingLotteryChance{}).Where("user_id = ? AND state = ?", userID, "ready").Updates(map[string]any{"state": "cleared", "cleared": gorm.Expr("cleared + remaining"), "remaining": 0}).Error; err != nil {
		return err
	}
	if err := tx.Model(&MarketingLotteryAward{}).Where("user_id = ? AND state = ?", userID, "pending").Update("state", "cleared").Error; err != nil {
		return err
	}
	if err := tx.Model(&MarketingLotteryDraw{}).Where("user_id = ? AND state = ?", userID, "pending").Update("state", "cleared").Error; err != nil {
		return err
	}
	op.RecordedAt = now
	return tx.Create(op).Error
}
