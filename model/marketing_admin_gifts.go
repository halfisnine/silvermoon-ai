package model

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const MarketingGiftBatchLimit = 100
const MarketingGiftMaxValidity = int64(36500 * 86400)

// A batch is issuance evidence, not an activity or another wallet. The money
// snapshot is immutable; successful recipients link to the existing gift ledger.
type MarketingGiftBatch struct {
	ID           int64  `gorm:"primaryKey"`
	ActorID      int    `gorm:"not null;uniqueIndex:idx_mv2_gift_batch_operation,priority:1"`
	OperationKey string `gorm:"size:64;not null;uniqueIndex:idx_mv2_gift_batch_operation,priority:2"`
	PayloadHash  string `gorm:"size:64;not null"`
	Reason       string `gorm:"size:500;not null"`
	Conversion   string `gorm:"type:text;not null"`
	ValidSeconds int64  `gorm:"not null"`
	CreatedAt    int64  `gorm:"not null"`
}

func (MarketingGiftBatch) TableName() string { return "marketing_v2_gift_batches" }

type MarketingGiftRecipient struct {
	ID          int64  `gorm:"primaryKey"`
	BatchID     int64  `gorm:"not null;uniqueIndex:idx_mv2_gift_recipient,priority:1"`
	UserID      int    `gorm:"not null;uniqueIndex:idx_mv2_gift_recipient,priority:2;index"`
	GiftID      int64  `gorm:"not null;index"`
	State       string `gorm:"size:16;not null"`
	ErrorKey    string `gorm:"size:160;not null"`
	UpdatedAt   int64  `gorm:"not null"`
	LastActorID int    `gorm:"not null"`
}

func (MarketingGiftRecipient) TableName() string { return "marketing_v2_gift_recipients" }

// Every delivery/retry locks the same batch before its recipient and wallet.
// This also serializes two different administrators retrying a partial batch.
func DeliverMarketingGiftRecipient(tx *gorm.DB, actorID int, batchID, recipientID int64) error {
	if err := AuthorizeMarketingAdmin(tx, actorID); err != nil {
		return err
	}
	var batch MarketingGiftBatch
	if err := lockForUpdate(tx).First(&batch, batchID).Error; err != nil {
		return err
	}
	var recipient MarketingGiftRecipient
	if err := tx.Where("id = ? AND batch_id = ?", recipientID, batch.ID).First(&recipient).Error; err != nil {
		return err
	}
	if recipient.State == "credited" {
		return nil
	}
	if err := lockMarketingLotteryActor(tx, recipient.UserID, false); err != nil {
		return err
	}
	var money MarketingMoneySnapshot
	if err := common.UnmarshalJsonStr(batch.Conversion, &money); err != nil {
		return err
	}
	now := time.Now().Unix()
	expires := int64(0)
	if batch.ValidSeconds > 0 {
		expires = now + batch.ValidSeconds
	}
	gift, err := GrantMarketingGift(tx, MarketingGiftInput{
		UserID: recipient.UserID, Kind: "admin_gift",
		Reference: fmt.Sprintf("manual:%d:%d", batch.ID, recipient.UserID),
		GrantedAt: now, ExpiresAt: expires, Money: money,
	})
	if err != nil {
		return err
	}
	return tx.Model(&recipient).Updates(map[string]any{
		"gift_id": gift.ID, "state": "credited", "error_key": "", "updated_at": now, "last_actor_id": actorID,
	}).Error
}

// Called only AFTER the entire failed delivery transaction rolled back. Never
// overwrite a success that a concurrent retry committed in the meantime.
func RecordMarketingGiftFailure(tx *gorm.DB, actorID int, batchID, recipientID int64, errorKey string) error {
	if err := AuthorizeMarketingAdmin(tx, actorID); err != nil {
		return err
	}
	var batch MarketingGiftBatch
	if err := lockForUpdate(tx).First(&batch, batchID).Error; err != nil {
		return err
	}
	return tx.Model(&MarketingGiftRecipient{}).
		Where("id = ? AND batch_id = ? AND state <> ?", recipientID, batch.ID, "credited").
		Updates(map[string]any{"state": "failed", "error_key": errorKey, "updated_at": time.Now().Unix(), "last_actor_id": actorID}).Error
}
