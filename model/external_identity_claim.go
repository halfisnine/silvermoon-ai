package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ExternalIdentityProviderTelegram = "telegram"

var ErrExternalIdentityAlreadyClaimed = errors.New("external identity is already claimed")

// ExternalIdentityClaim is the durable ownership record for an identity issued
// by an external provider. The two unique indexes make both the provider
// subject and the user's provider slot single-owner without relying on a
// check-then-update sequence.
type ExternalIdentityClaim struct {
	Id        int64     `json:"id" gorm:"primaryKey"`
	Provider  string    `json:"provider" gorm:"type:varchar(32);not null;uniqueIndex:idx_external_identity_subject,priority:1;uniqueIndex:idx_external_identity_user,priority:1"`
	Subject   string    `json:"subject" gorm:"type:varchar(128);not null;uniqueIndex:idx_external_identity_subject,priority:2"`
	UserId    int       `json:"user_id" gorm:"not null;index;uniqueIndex:idx_external_identity_user,priority:2"`
	CreatedAt time.Time `json:"created_at"`
}

func (ExternalIdentityClaim) TableName() string {
	return "external_identity_claims"
}

// ClaimExternalIdentityWithTx atomically claims a provider subject for one
// user. Repeating the exact mapping is idempotent; every competing subject or
// user is rejected. Ownership is read back instead of trusting RowsAffected,
// whose duplicate-key semantics differ between supported databases.
func ClaimExternalIdentityWithTx(tx *gorm.DB, provider, subject string, userId int) error {
	provider = strings.TrimSpace(provider)
	subject = strings.TrimSpace(subject)
	if tx == nil || provider == "" || subject == "" || userId == 0 {
		return errors.New("external identity claim is invalid")
	}

	claim := ExternalIdentityClaim{Provider: provider, Subject: subject, UserId: userId}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&claim)
	if result.Error != nil {
		return result.Error
	}
	var subjectOwner ExternalIdentityClaim
	if err := tx.Where("provider = ? AND subject = ?", provider, subject).First(&subjectOwner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrExternalIdentityAlreadyClaimed
		}
		return err
	}
	if subjectOwner.UserId != userId {
		return ErrExternalIdentityAlreadyClaimed
	}

	var userClaim ExternalIdentityClaim
	if err := tx.Where("provider = ? AND user_id = ?", provider, userId).First(&userClaim).Error; err != nil {
		return err
	}
	if userClaim.Subject != subject {
		return ErrExternalIdentityAlreadyClaimed
	}
	return nil
}

func ReleaseExternalIdentityWithTx(tx *gorm.DB, provider string, userId int) error {
	provider = strings.TrimSpace(provider)
	if tx == nil || provider == "" || userId == 0 {
		return errors.New("external identity release is invalid")
	}
	return tx.Where("provider = ? AND user_id = ?", provider, userId).
		Delete(&ExternalIdentityClaim{}).Error
}

func GetUserByTelegramID(telegramID string) (*User, error) {
	var user User
	err := DB.Where("telegram_id = ?", telegramID).First(&user).Error
	return &user, err
}

// BindTelegramForSessionWithTx preserves single ownership and the session that
// started the binding. The caller consumes its OAuth flow in this transaction.
func BindTelegramForSessionWithTx(tx *gorm.DB, identity AuthSessionIdentity, telegramID string) error {
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return err
	}
	var user User
	if err := tx.Select("id", "telegram_id").First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if user.TelegramId != "" {
		return ErrExternalIdentityAlreadyClaimed
	}
	if err := ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, telegramID, user.Id); err != nil {
		return err
	}
	result := tx.Model(&User{}).Where("id = ? AND (telegram_id = ? OR telegram_id IS NULL)", user.Id, "").Update("telegram_id", telegramID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrExternalIdentityAlreadyClaimed
	}
	return nil
}

func releaseAllExternalIdentitiesWithTx(tx *gorm.DB, userId int) error {
	if tx == nil || userId == 0 {
		return errors.New("external identity release is invalid")
	}
	return tx.Where("user_id = ?", userId).Delete(&ExternalIdentityClaim{}).Error
}

// Builtin subjects remain unchanged in users. A digest fits the existing claim
// index even when an OIDC subject exceeds its 128-character storage limit.
var builtinIdentityColumns = []string{"github_id", "discord_id", "oidc_id", "linux_do_id", "wechat_id"}

func builtinIdentityValues(user *User) map[string]string {
	return map[string]string{"github_id": user.GitHubId, "discord_id": user.DiscordId, "oidc_id": user.OidcId, "linux_do_id": user.LinuxDOId, "wechat_id": user.WeChatId}
}

func claimBuiltinIdentityWithTx(tx *gorm.DB, userID int, column, subject string) error {
	if !userBindColumns[column] || subject == "" {
		return ErrAccountBindingChanged
	}
	digest := sha256.Sum256([]byte(subject))
	if err := ClaimExternalIdentityWithTx(tx, column, hex.EncodeToString(digest[:]), userID); err != nil {
		return err
	}
	// Include soft-deleted identities, matching existing IsTaken semantics. This
	// protects baseline owners whose claim has not yet been backfilled, using
	// the database's original comparison/collation rather than changing IDs.
	var owner User
	err := lockForUpdate(tx.Unscoped()).Select("id").Where(column+" = ? AND id <> ?", subject, userID).First(&owner).Error
	if err == nil {
		return ErrExternalIdentityAlreadyClaimed
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return nil
}

// updateBuiltinIdentityWithTx is the shared ownership boundary for binding,
// replacement and unlinking. The user's lock serializes its provider slot;
// the claim's unique indexes serialize competitors on other instances.
func updateBuiltinIdentityWithTx(tx *gorm.DB, userID int, column, subject string) error {
	if tx == nil || tx.Statement == nil {
		return ErrAccountBindingChanged
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountBindingChanged
	}
	if !userBindColumns[column] || userID <= 0 {
		return ErrAccountBindingChanged
	}
	var current User
	if err := lockForUpdate(tx).First(&current, userID).Error; err != nil {
		return err
	}
	old := builtinIdentityValues(&current)[column]
	if old != subject {
		if err := ReleaseExternalIdentityWithTx(tx, column, userID); err != nil {
			return err
		}
	}
	if subject != "" {
		if err := claimBuiltinIdentityWithTx(tx, userID, column, subject); err != nil {
			return err
		}
	} else {
		if err := ReleaseExternalIdentityWithTx(tx, column, userID); err != nil {
			return err
		}
	}
	return tx.Model(&User{}).Where("id = ?", userID).Update(column, subject).Error
}

// InitializeExternalIdentityClaims imports baseline provider bindings after the
// claim table is migrated. It never creates marketing facts or rewrites IDs.
// Existing duplicate ownership fails migration rather than preserving an
// ambiguous login identity. Deleted accounts retain ownership until hard delete.
func InitializeExternalIdentityClaims() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var users []User
		if err := tx.Unscoped().Select("id", "telegram_id", "github_id", "discord_id", "oidc_id", "linux_do_id", "wechat_id").Order("id").Find(&users).Error; err != nil {
			return err
		}
		for _, candidate := range users {
			var user User
			if err := lockForUpdate(tx.Unscoped()).First(&user, candidate.Id).Error; err != nil {
				return err
			}
			if user.TelegramId != "" {
				if err := ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.TelegramId, user.Id); err != nil {
					return fmt.Errorf("backfill Telegram identity for user %d: %w", user.Id, err)
				}
			}
			values := builtinIdentityValues(&user)
			for _, column := range builtinIdentityColumns {
				if values[column] == "" {
					continue
				}
				if err := claimBuiltinIdentityWithTx(tx, user.Id, column, values[column]); err != nil {
					return fmt.Errorf("backfill %s identity for user %d: %w", column, user.Id, err)
				}
			}
		}
		return nil
	})
}
