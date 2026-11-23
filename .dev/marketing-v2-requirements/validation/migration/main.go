// This isolated migration probe is compiled once against the released baseline
// and once against the candidate. It never serves HTTP or calls payment routes.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type indexSnapshot struct {
	Name    string
	Columns []string
	Unique  bool
}
type snapshot struct {
	User        model.User
	AuthVersion int64
	Token       model.Token
	Code        model.Redemption
	Payment     model.TopUp
	Checkin     model.Checkin
	Indexes     map[string][]indexSnapshot
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) != 4 {
		panic("usage: migration-probe seed|verify snapshot-path baseline|candidate")
	}
	common.IsMasterNode = true
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	if path := os.Getenv("SQLITE_PATH"); path != "" {
		common.SQLitePath = path
	}
	must(model.InitDB())
	sqlDB, err := model.DB.DB()
	must(err)
	defer sqlDB.Close()
	candidate := os.Args[3] == "candidate"
	for _, table := range []string{"marketing_v2_accounts", "marketing_v2_payments", "marketing_v2_gifts", "marketing_v2_funding", "marketing_v2_allocations", "marketing_v2_ledger", "marketing_v2_token_holds", "marketing_v2_funding_operations", "marketing_v2_debits", "marketing_v2_dispatches", "marketing_v2_usage_projections", "marketing_v2_realtime_rounds", "marketing_v2_realtime_events", "marketing_v2_lottery_pools", "marketing_v2_lottery_prizes", "marketing_v2_lottery_issues", "marketing_v2_lottery_chances", "marketing_v2_lottery_draws", "marketing_v2_lottery_awards", "marketing_v2_lottery_operations", "marketing_v2_activities", "marketing_v2_activity_operations", "marketing_v2_invitations", "marketing_v2_activity_facts", "marketing_v2_activity_progress", "marketing_v2_activity_decisions", "marketing_v2_activity_rewards", "marketing_v2_activity_delivery_cursors", "marketing_v2_checkin_days", "marketing_v2_checkin_claims"} {
		if model.DB.Migrator().HasTable(table) != candidate {
			panic("unexpected migration state for " + table)
		}
	}
	if os.Args[1] == "seed" {
		// Synthetic disabled credentials. No customer data or usable account secrets.
		user := model.User{Id: 70001, Username: "mv2_baseline_fixture", Password: "disabled-test-fixture", DisplayName: "迁移保留", Status: 2, Quota: 123456, UsedQuota: 789, RequestCount: 9, AuthVersion: 1, GitHubId: "legacy-github-login", DiscordId: "migration-discord", OidcId: "migration-oidc", LinuxDOId: "migration-linuxdo", WeChatId: "migration-wechat"}
		must(model.DB.Create(&user).Error)
		deleted := model.User{Id: 70002, Username: "mv2_deleted_identity", AffCode: "mv2-deleted-identity", GitHubId: "deleted-github-subject", Status: 2}
		must(model.DB.Create(&deleted).Error)
		must(model.DB.Delete(&deleted).Error)
		must(model.DB.Create(&model.Token{Id: 70001, UserId: user.Id, Key: "mv2-disabled-fixture-token", Name: "migration token", Status: 2, RemainQuota: 333, UsedQuota: 55, ModelLimits: "model-a,model-b"}).Error)
		must(model.DB.Create(&model.Redemption{Id: 70001, UserId: user.Id, Key: "mv2-disabled-fixture-redemption", Name: "migration code", Status: 2, Quota: 456, CreatedTime: 1700000000}).Error)
		must(model.DB.Create(&model.Checkin{UserId: user.Id, CheckinDate: "2025-01-01", QuotaAwarded: 321, CreatedAt: 1735689600}).Error)
		must(model.DB.Create(&model.TopUp{Id: 70001, UserId: user.Id, Amount: 900, Money: 8.75, TradeNo: "mv2-baseline-order", PaymentMethod: "alipay", PaymentProvider: "epay", CreateTime: 1700000000, CompleteTime: 1700000001, Status: "success"}).Error)
	} else if os.Args[1] != "verify" {
		panic("unknown operation")
	}
	var got snapshot
	must(model.DB.First(&got.User, 70001).Error)
	must(model.DB.First(&got.Token, 70001).Error)
	must(model.DB.First(&got.Code, 70001).Error)
	must(model.DB.First(&got.Payment, 70001).Error)
	must(model.DB.Where("user_id = ? AND checkin_date = ?", 70001, "2025-01-01").First(&got.Checkin).Error)
	got.AuthVersion = got.User.AuthVersion
	got.Indexes = make(map[string][]indexSnapshot)
	for _, table := range []string{"users", "tokens", "redemptions", "top_ups"} {
		indexes, err := model.DB.Migrator().GetIndexes(table)
		must(err)
		for _, index := range indexes {
			unique, _ := index.Unique()
			got.Indexes[table] = append(got.Indexes[table], indexSnapshot{Name: index.Name(), Columns: index.Columns(), Unique: unique})
		}
		sort.Slice(got.Indexes[table], func(i, j int) bool { return got.Indexes[table][i].Name < got.Indexes[table][j].Name })
	}
	data, err := common.Marshal(got)
	must(err)
	if os.Args[1] == "seed" {
		must(os.WriteFile(os.Args[2], data, 0600))
	} else {
		previous, err := os.ReadFile(os.Args[2])
		must(err)
		// Preserve every released JSON field, including any no longer present
		// in today's structs. Only the explicitly added sale metadata may be
		// absent from an old snapshot, and it must remain empty after upgrade.
		var expected, actual map[string]any
		must(common.Unmarshal(previous, &expected))
		must(common.Unmarshal(data, &actual))
		for name, keys := range map[string][]string{
			"Code":    {"paid_cents", "sales_channel", "sales_batch", "sales_evidence", "payment_id"},
			"Payment": {"expected_paid_cents", "merchant_id", "payment_id", "provider_reference", "completion_actor_id"},
		} {
			expectedRecord := expected[name].(map[string]any)
			actualRecord := actual[name].(map[string]any)
			for _, key := range keys {
				if _, existed := expectedRecord[key]; existed {
					continue
				}
				if value, exists := actualRecord[key]; exists {
					if value != "" && value != float64(0) {
						panic("historical payment received fabricated sale metadata")
					}
					expectedRecord[key] = value
				}
			}
		}
		expectedData, err := common.Marshal(expected)
		must(err)
		actualData, err := common.Marshal(actual)
		must(err)
		if !bytes.Equal(expectedData, actualData) {
			panic("baseline records or indexes changed during migration")
		}
		if candidate {
			// Internal fields excluded from JSON must not fabricate payment
			// evidence for the released baseline's ordinary redemption code.
			for _, table := range []string{"redemptions", "top_ups"} {
				var internal struct{ CreditedConversion *string }
				must(model.DB.Table(table).Select("credited_conversion").Where("id = ?", 70001).Scan(&internal).Error)
				if internal.CreditedConversion != nil && *internal.CreditedConversion != "" {
					panic("historical payment received fabricated payment evidence")
				}
			}
		}
	}
	if candidate && os.Args[1] == "verify" {
		// Existing authentication ownership is backfilled; no marketing account,
		// registration fact or reward is invented for a historical account.
		for _, identity := range []struct {
			provider, subject string
			userID            int
		}{
			{"github_id", "legacy-github-login", 70001}, {"discord_id", "migration-discord", 70001},
			{"oidc_id", "migration-oidc", 70001}, {"linux_do_id", "migration-linuxdo", 70001},
			{"wechat_id", "migration-wechat", 70001}, {"github_id", "deleted-github-subject", 70002},
		} {
			key := fmt.Sprintf("%x", sha256.Sum256([]byte(identity.subject)))
			var claim model.ExternalIdentityClaim
			must(model.DB.Where("provider = ? AND subject = ?", identity.provider, key).First(&claim).Error)
			if claim.UserId != identity.userID {
				panic("historical identity ownership changed")
			}
			tx := model.DB.Begin()
			must(tx.Error)
			err := model.ClaimExternalIdentityWithTx(tx, identity.provider, key, 79999)
			must(tx.Rollback().Error)
			if err == nil {
				panic("historical identity ownership can be duplicated")
			}
		}
		var deleted model.User
		must(model.DB.Unscoped().First(&deleted, 70002).Error)
		if !deleted.DeletedAt.Valid || deleted.GitHubId != "deleted-github-subject" {
			panic("deleted historical identity changed")
		}
		for _, table := range []string{"marketing_v2_accounts", "marketing_v2_activity_facts", "marketing_v2_gifts", "marketing_v2_checkin_days", "marketing_v2_checkin_claims"} {
			var count int64
			must(model.DB.Table(table).Count(&count).Error)
			if count != 0 {
				panic("identity migration fabricated marketing history")
			}
		}
	}
	if candidate {
		// A calendar day and a continuous-reward ordinal must each remain
		// globally unique after every fresh/upgrade startup. Map rows keep the
		// same probe compilable against the released baseline without new types.
		for table, row := range map[string]map[string]any{
			"marketing_v2_checkin_days":   {"user_id": 79999, "activity_id": 79999, "day": "2025-01-02", "makeup": false, "recorded_at": 1735776000, "fact_id": 79999, "qualification_day": "", "qualified_paid_cents": 0},
			"marketing_v2_checkin_claims": {"user_id": 79999, "activity_id": 79999, "milestone_days": 7, "ordinal": 1, "anchor_day": "2025-01-02", "fact_id": 79999, "recorded_at": 1735776000},
		} {
			tx := model.DB.Begin()
			must(tx.Error)
			must(tx.Table(table).Create(row).Error)
			// GORM may append an auto-generated ID to the map. A second fresh
			// map ensures we test the business key rather than primary-key reuse.
			duplicate := make(map[string]any, len(row))
			for key, value := range row {
				if key != "id" {
					duplicate[key] = value
				}
			}
			err := tx.Table(table).Create(duplicate).Error
			must(tx.Rollback().Error)
			if err == nil {
				panic("check-in business uniqueness missing: " + table)
			}
		}
	}
	// Check behavior, not just index names: duplicate historical keys must fail.
	for _, duplicate := range []any{
		&model.User{Username: "mv2_baseline_fixture", AffCode: "mv2-duplicate-aff"},
		&model.Token{Key: "mv2-disabled-fixture-token"},
		&model.Redemption{Key: "mv2-disabled-fixture-redemption"},
		&model.TopUp{TradeNo: "mv2-baseline-order"},
	} {
		tx := model.DB.Begin()
		must(tx.Error)
		err := tx.Create(duplicate).Error
		must(tx.Rollback().Error)
		if err == nil {
			panic("historical unique constraint missing")
		}
	}
	var version string
	switch common.MainDatabaseType() {
	case common.DatabaseTypeSQLite:
		must(model.DB.Raw("select sqlite_version()").Scan(&version).Error)
	default:
		must(model.DB.Raw("select version()").Scan(&version).Error)
	}
	fmt.Printf("MIGRATION_PROBE_OK mode=%s target=%s engine=%s version=%s\n", os.Args[1], os.Args[3], common.MainDatabaseType(), version)
}
