package model

import (
	"context"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

type Redemption struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id"`
	Key                string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index"`
	Quota              int            `json:"quota" gorm:"default:100"`
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime       int64          `json:"redeemed_time" gorm:"bigint"`
	Count              int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId         int            `json:"used_user_id"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
	PaidCents          int64          `json:"paid_cents" gorm:"not null;default:0"`
	CreditedConversion string         `json:"-" gorm:"type:text"`
	SalesChannel       string         `json:"sales_channel" gorm:"size:160"`
	SalesBatch         string         `json:"sales_batch" gorm:"size:160"`
	SalesEvidence      string         `json:"sales_evidence" gorm:"type:text"`
	PaymentID          int64          `json:"payment_id" gorm:"not null;default:0"`
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := paidRedemptionDB(DB).Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := paidRedemptionDB(DB).Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, common.NewMessage("ID is empty")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = paidRedemptionDB(DB).First(&redemption, "id = ?", id).Error
	return &redemption, err
}

// Redeem preserves the public recharge entry while the database transaction owns
// both the one-use credential transition and its payment/activity facts.
func Redeem(key string, userId int) (int, error) {
	var result *PaidRedemptionResult
	err := WithMarketingTransaction(context.Background(), func(tx *gorm.DB) error {
		var err error
		result, err = redeemPaidCodeWithClock(tx, userId, key, common.GetTimestamp)
		return err
	})
	if err != nil {
		return 0, ErrRedeemFailed
	}
	if !result.AlreadyPosted {
		syncCreditUserQuotaCache(userId, result.Quota, common.LogText("redemption"))
		RecordLog(userId, LogTypeTopup, common.NewMessage("Topped up {{quota}} with a redemption code, code ID {{id}}", map[string]any{"quota": logger.FormatQuota(result.Quota), "id": result.RedemptionID}))
	}
	return result.Quota, nil
}
