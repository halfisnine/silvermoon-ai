package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The same behavioral fixture runs against all supported business databases.
// Records are created through reservation/settlement APIs, not a mocked ledger.
func TestMarketingAccountingReads(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect != common.DatabaseTypeSQLite {
				env := "MV2_MYSQL_DSN"
				if dialect == common.DatabaseTypePostgreSQL {
					env = "MV2_POSTGRES_DSN"
				}
				dsn := os.Getenv(env)
				if dsn == "" {
					if os.Getenv("MV2_REQUIRE_DATABASES") == "1" {
						t.Fatal(env + " required")
					}
					t.Skip(env + " not configured")
				}
				if dialect == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			oldDB, oldLog := model.DB, model.LOG_DB
			oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB, model.LOG_DB = db, nil
			common.SetDatabaseTypes(dialect, dialect)
			t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLog; common.SetDatabaseTypes(oldMain, oldLogType) })
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.MarketingAccount{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{}, &model.MarketingDebit{}, &model.MarketingFundingOperation{}, &model.MarketingTokenHold{}, &model.MarketingUsageProjection{}, &model.MarketingActivityReward{}, &model.MarketingLotteryAward{}, &model.MarketingActivity{}, &model.MarketingLotteryPool{}, &model.MarketingDispatch{}, &model.MarketingRealtimeRound{}, &model.MarketingPayment{}, &model.TopUp{}, &model.Redemption{}, &model.MarketingActivityFact{}, &model.MarketingMaintenanceOperation{}, &model.MarketingFactSequence{}, &model.MarketingInvitation{}, &model.MarketingActivityDecision{}, &model.MarketingActivityDeliveryCursor{}, &model.MarketingActivityProgress{}, &model.Task{}, &model.Midjourney{}, &model.MarketingLotteryOperation{}, &model.MarketingLotteryChance{}, &model.MarketingLotteryDraw{}))
			oldUnits, oldRate, oldGeneral := common.QuotaPerUnit, operation_setting.USDExchangeRate, *operation_setting.GetGeneralSetting()
			common.QuotaPerUnit = 100
			operation_setting.USDExchangeRate = 1
			operation_setting.GetGeneralSetting().QuotaDisplayType = "CNY"
			t.Cleanup(func() {
				common.QuotaPerUnit = oldUnits
				operation_setting.USDExchangeRate = oldRate
				*operation_setting.GetGeneralSetting() = oldGeneral
			})
			tag := fmt.Sprint(time.Now().UnixNano())
			user := model.User{Username: "acc" + tag, AffCode: "a" + tag, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1000}
			other := model.User{Username: "other" + tag, AffCode: "o" + tag, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&other).Error)
			require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone}).Error)
			require.NoError(t, db.Create(&model.MarketingAccount{UserID: other.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "accounting fixture"}).Error)
			now := time.Now().Unix()
			location, err := time.LoadLocation("Asia/Shanghai")
			require.NoError(t, err)
			month := time.Unix(now, 0).In(location).Format("2006-01")
			start, err := time.ParseInLocation("2006-01", month, location)
			require.NoError(t, err)
			money := model.MarketingMoneySnapshot{Amount: "1.5", QuotaPerUnit: "100", CurrencyPerUnit: "1", Currency: "CNY"}
			var gift *model.MarketingGift
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				gift, e = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "registration", Reference: "accounting-gift-" + tag, GrantedAt: now - 100, ExpiresAt: now - 5, Money: money})
				return e
			}))
			pricing, err := common.Marshal(map[string]any{"model": "test-model", "display_money": model.MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: "100", CurrencyPerUnit: "1", Currency: "CNY"}})
			require.NoError(t, err)
			var funding *model.MarketingFunding
			ref := "accounting-request-" + tag
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				funding, e = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: user.Id, Playground: true, RequestReference: ref, QualifiedAt: now - 10, Quota: 300, PricingEvidence: string(pricing)})
				return e
			}))
			before, err := ReadMarketingPaidConsumption(context.Background(), user.Id, 0, false, month)
			require.NoError(t, err)
			assert.Empty(t, before.(map[string]any)["amounts"], "reservations never count as consumption")
			actual := int64(250)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				funding, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 1, ActualQuota: &actual, EvidenceKind: "upstream_progress", Evidence: "known cumulative fee"})
				return e
			}))
			f := MarketingAccountingFilter{Page: 1, PageSize: 20}
			detail, err := ReadMarketingAccounting(context.Background(), user.Id, false, funding.ID, f)
			require.NoError(t, err)
			view := detail.(MarketingAccountingRequest)
			assert.Equal(t, "2.5", view.Total.Amount)
			assert.Equal(t, "1", view.Paid.Amount)
			assert.Equal(t, "1.5", view.Gift.Amount)
			require.Len(t, view.GiftSources, 1)
			assert.Equal(t, gift.ID, view.GiftSources[0].GiftID)
			assert.Equal(t, "1.5", view.GiftSources[0].Money.Amount)
			assert.False(t, view.FinalObserved)
			_, err = ReadMarketingAccounting(context.Background(), other.Id, false, funding.ID, f)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			totals, err := ReadMarketingPaidConsumption(context.Background(), user.Id, 0, false, month)
			require.NoError(t, err)
			assert.Equal(t, []MarketingAccountingMoney{{"1", "CNY"}}, totals.(map[string]any)["amounts"])
			// Move the original debit to the previous accounting month. A current-month
			// reversal belongs to this month and retains the original money conversion.
			require.NoError(t, db.Model(&model.MarketingLedgerEntry{}).Where("funding_id = ?", funding.ID).Update("recorded_at", start.Unix()-1).Error)
			common.QuotaPerUnit = 1000
			operation_setting.USDExchangeRate = 7
			actual = 200
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				funding, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 2, ActualQuota: &actual, EvidenceKind: "upstream_progress", Evidence: "repriced cumulative fee"})
				return e
			}))
			totals, err = ReadMarketingPaidConsumption(context.Background(), user.Id, 0, false, month)
			require.NoError(t, err)
			assert.Equal(t, []MarketingAccountingMoney{{"-0.5", "CNY"}}, totals.(map[string]any)["amounts"])
			detail, err = ReadMarketingAccounting(context.Background(), user.Id, false, funding.ID, f)
			require.NoError(t, err)
			view = detail.(MarketingAccountingRequest)
			assert.Equal(t, "0.5", view.Paid.Amount)
			assert.Equal(t, "2", view.Total.Amount)
			actual = 80
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				funding, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: user.Id, FundingID: funding.ID, Version: 3, ActualQuota: &actual, Final: true, EvidenceKind: "upstream_terminal", Evidence: "complete actual fee"})
				return e
			}))
			totals, err = ReadMarketingPaidConsumption(context.Background(), user.Id, 0, false, month)
			require.NoError(t, err)
			assert.Equal(t, []MarketingAccountingMoney{{"-1", "CNY"}}, totals.(map[string]any)["amounts"], "gift reversals do not become paid rebates")
			detail, err = ReadMarketingAccounting(context.Background(), user.Id, false, funding.ID, f)
			require.NoError(t, err)
			view = detail.(MarketingAccountingRequest)
			assert.Equal(t, "0", view.Paid.Amount)
			assert.Equal(t, "0.8", view.Gift.Amount)
			assert.Equal(t, "settled", view.State)
			assert.True(t, view.FinalObserved)
			// The log database is deliberately absent throughout. The immutable
			// primary projection retains the request identity for recovery.
			projectionPayload, err := common.Marshal(model.Log{UserId: user.Id, RequestId: ref, UsageEventID: funding.RequestKey, Type: model.LogTypeConsume})
			require.NoError(t, err)
			require.NoError(t, db.Create(&model.MarketingUsageProjection{FundingID: funding.ID, Version: funding.Revision, Payload: string(projectionPayload), CreatedAt: now}).Error)
			f.RequestID = ref
			detail, err = ReadMarketingAccounting(context.Background(), user.Id, false, 0, f)
			require.NoError(t, err)
			assert.Equal(t, ref, detail.(MarketingAccountingRequest).RequestID)
			// Loss of an older conversion snapshot is explicit, never today's display.
			require.NoError(t, db.Model(&model.MarketingFunding{}).Where("id = ?", funding.ID).Update("pricing_evidence", `{"model":"test-model"}`).Error)
			detail, err = ReadMarketingAccounting(context.Background(), user.Id, false, funding.ID, f)
			require.NoError(t, err)
			view = detail.(MarketingAccountingRequest)
			assert.False(t, view.ConversionKnown)
			assert.Nil(t, view.Total)
			assert.Nil(t, view.Paid)
			totals, err = ReadMarketingPaidConsumption(context.Background(), user.Id, 0, false, month)
			require.NoError(t, err)
			assert.Equal(t, int64(2), totals.(map[string]any)["conversion_unknown_count"])
			assert.Empty(t, totals.(map[string]any)["amounts"])

			t.Run("single storage unit stays visible in historical source and monthly totals", func(t *testing.T) {
				tinyUser := model.User{Username: "tiny" + tag, AffCode: "t" + tag, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 10}
				require.NoError(t, db.Create(&tinyUser).Error)
				require.NoError(t, db.Create(&model.MarketingAccount{UserID: tinyUser.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone}).Error)
				tinyMoney := model.MarketingMoneySnapshot{Amount: "0.000002", QuotaPerUnit: "500000", CurrencyPerUnit: "1", Currency: "CNY"}
				tinyPricing, err := common.Marshal(map[string]any{"model": "tiny-cost-model", "display_money": tinyMoney})
				require.NoError(t, err)
				var tinyGift *model.MarketingGift
				var tinyFunding *model.MarketingFunding
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					tinyGift, e = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: tinyUser.Id, Kind: "registration", Reference: "tiny-gift-" + tag, GrantedAt: now, Money: tinyMoney})
					if e != nil {
						return e
					}
					tinyFunding, e = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: tinyUser.Id, Playground: true, RequestReference: "tiny-request-" + tag, QualifiedAt: now, Quota: 2, PricingEvidence: string(tinyPricing)})
					if e != nil {
						return e
					}
					cost := int64(2)
					tinyFunding, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: tinyUser.Id, FundingID: tinyFunding.ID, Version: 1, ActualQuota: &cost, Final: true, EvidenceKind: "upstream_terminal", Evidence: "one gift unit and one paid unit"})
					return e
				}))
				assert.EqualValues(t, 1, tinyGift.GrantedQuota)
				detail, err := ReadMarketingAccounting(context.Background(), tinyUser.Id, false, tinyFunding.ID, MarketingAccountingFilter{Page: 1, PageSize: 20})
				require.NoError(t, err)
				row := detail.(MarketingAccountingRequest)
				require.NotNil(t, row.Total)
				require.NotNil(t, row.Paid)
				require.NotNil(t, row.Gift)
				assert.Equal(t, "0.000004", row.Total.Amount)
				assert.Equal(t, "0.000002", row.Paid.Amount)
				assert.Equal(t, "0.000002", row.Gift.Amount)
				require.Len(t, row.GiftSources, 1)
				require.NotNil(t, row.GiftSources[0].Money)
				assert.Equal(t, "0.000002", row.GiftSources[0].Money.Amount)
				monthly, err := ReadMarketingPaidConsumption(context.Background(), tinyUser.Id, 0, false, month)
				require.NoError(t, err)
				assert.Equal(t, []MarketingAccountingMoney{{Amount: "0.000002", Currency: "CNY"}}, monthly.(map[string]any)["amounts"], "a single paid unit must not disappear through two-decimal formatting or current conversion settings")
			})

			t.Run("unknown terminal and confirmed paid overdraft remain distinct", func(t *testing.T) {
				var unknown, short *model.MarketingFunding
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					unknown, e = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: user.Id, Playground: true, RequestReference: "unknown-" + tag, QualifiedAt: now, Quota: 1, PricingEvidence: string(pricing)})
					return e
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					short, e = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: user.Id, Playground: true, RequestReference: "short-" + tag, QualifiedAt: now, Quota: 1, PricingEvidence: string(pricing)})
					return e
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					unknown, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: user.Id, FundingID: unknown.ID, Version: 1, Final: true, EvidenceKind: "unknown", Evidence: "upstream disconnected without terminal usage"})
					return e
				}))
				finalFee := int64(2000)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					short, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: user.Id, FundingID: short.ID, Version: 1, ActualQuota: &finalFee, Final: true, EvidenceKind: "upstream_terminal", Evidence: "known terminal above remaining funds"})
					return e
				}))
				filter := MarketingAccountingFilter{Page: 1, PageSize: 20}
				read, err := ReadMarketingAccounting(context.Background(), user.Id, false, unknown.ID, filter)
				require.NoError(t, err)
				v := read.(MarketingAccountingRequest)
				assert.Equal(t, "review", v.State)
				assert.False(t, v.CostKnown)
				assert.True(t, v.FinalObserved)
				assert.Nil(t, v.Actual)
				assert.Nil(t, v.Unsettled)
				assert.Equal(t, "0", v.Total.Amount)
				assert.Equal(t, "0.01", v.Reserved.Amount)
				read, err = ReadMarketingAccounting(context.Background(), user.Id, false, short.ID, filter)
				require.NoError(t, err)
				v = read.(MarketingAccountingRequest)
				assert.Equal(t, "settled", v.State)
				assert.True(t, v.CostKnown)
				assert.Equal(t, "20", v.Actual.Amount)
				assert.Equal(t, "20", v.Total.Amount)
				assert.Equal(t, "20", v.Paid.Amount)
				assert.Equal(t, "0", v.Unsettled.Amount)
				wallet, err := ReadMarketingWallet(context.Background(), user.Id)
				require.NoError(t, err)
				assert.Equal(t, "-7", wallet.(map[string]any)["paid"].(*MarketingAccountingMoney).Amount, "wallet uses current 1000 units / 7 CNY, while historical request retains its original conversion")
				assert.Equal(t, "0", wallet.(map[string]any)["gift"].(*MarketingAccountingMoney).Amount)
				own := &model.Log{Type: model.LogTypeConsume, UserId: user.Id, UsageEventID: short.RequestKey, Other: `{}`}
				foreign := &model.Log{Type: model.LogTypeConsume, UserId: other.Id, UsageEventID: short.RequestKey, Other: `{}`}
				require.NoError(t, EnrichMarketingUsageLogs(context.Background(), []*model.Log{own, foreign}))
				var ownOther, foreignOther map[string]any
				require.NoError(t, common.UnmarshalJsonStr(own.Other, &ownOther))
				require.NoError(t, common.UnmarshalJsonStr(foreign.Other, &foreignOther))
				assert.Contains(t, ownOther, "accounting")
				assert.NotContains(t, foreignOther, "accounting")
				// A malformed durable projection cannot bind a foreign user's log identity
				// to this account's sources, even though funding metadata itself is valid.
				badPayload, err := common.Marshal(model.Log{UserId: other.Id, RequestId: "private-other-request", UsageEventID: unknown.RequestKey})
				require.NoError(t, err)
				bad := model.MarketingUsageProjection{FundingID: unknown.ID, Version: 1, Payload: string(badPayload)}
				require.NoError(t, db.Create(&bad).Error)
				_, err = ReadMarketingAccounting(context.Background(), user.Id, false, unknown.ID, filter)
				require.ErrorIs(t, err, model.ErrMarketingReview)
				require.NoError(t, db.Delete(&bad).Error)
			})

			t.Run("reversal blocked by wallet capacity remains a pending source return", func(t *testing.T) {
				require.NoError(t, db.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
				owner := model.User{Username: "return" + tag, AffCode: "r" + tag, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100}
				require.NoError(t, db.Create(&owner).Error)
				require.NoError(t, db.Create(&model.MarketingAccount{UserID: owner.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "accounting capacity fixture"}).Error)
				var request *model.MarketingFunding
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					request, e = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: owner.Id, Playground: true, RequestReference: "pending-return-" + tag, QualifiedAt: now, Quota: 0, PricingEvidence: string(pricing)})
					return e
				}))
				actual := int64(20)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					request, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: owner.Id, FundingID: request.ID, Version: 1, ActualQuota: &actual, EvidenceKind: "upstream_progress", Evidence: "observed cumulative cost"})
					return e
				}))
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: owner.Id, Kind: "online", Reference: "capacity-topup-" + tag, PaidCents: 100, PostedAt: now, Money: model.MarketingMoneySnapshot{Amount: fmt.Sprint(common.MaxWalletQuota - 80), QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
					return e
				}))
				actual = 10
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					request, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: owner.Id, FundingID: request.ID, Version: 2, ActualQuota: &actual, Final: true, EvidenceKind: "upstream_terminal", Evidence: "final reduced fee"})
					return e
				}))
				require.Equal(t, "reversal_requires_reconciliation", request.ReviewReason)
				read, err := ReadMarketingAccounting(context.Background(), owner.Id, false, request.ID, MarketingAccountingFilter{Page: 1, PageSize: 20})
				require.NoError(t, err)
				payload, err := common.Marshal(read)
				require.NoError(t, err)
				var view map[string]any
				require.NoError(t, common.Unmarshal(payload, &view))
				assert.Equal(t, map[string]any{"amount": "0.1", "currency": "CNY"}, view["pending_return"])
				assert.Equal(t, map[string]any{"amount": "0", "currency": "CNY"}, view["unsettled"])
				assert.Equal(t, "review", view["state"])
			})

			t.Run("administrator provenance excludes code and maintenance secrets", func(t *testing.T) {
				admin := model.User{Username: "admin" + tag, AffCode: "d" + tag, Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
				require.NoError(t, db.Create(&admin).Error)
				require.NoError(t, db.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
				codeKey := fmt.Sprintf("%032d", time.Now().UnixNano())
				var code *model.Redemption
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					rows, e := model.CreatePaidRedemptionBatch(tx, admin.Id, []string{codeKey}, model.PaidRedemptionSpec{Name: "Paid test", Money: money, PaidCents: 123, SalesChannel: "shop", SalesBatch: "batch", SalesEvidence: "private-sales-evidence"}, now)
					if e == nil {
						code = rows[0]
					}
					return e
				}))
				var redeemed *model.PaidRedemptionResult
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					redeemed, e = model.RedeemPaidCode(tx, other.Id, codeKey, now)
					return e
				}))
				filter := MarketingAccountingFilter{Page: 1, PageSize: 20, UserID: other.Id}
				payment, err := ReadMarketingAccountingPayments(context.Background(), admin.Id, redeemed.PaymentID, filter)
				require.NoError(t, err)
				p := payment.(MarketingAccountingPayment)
				assert.Equal(t, "paid_code", p.Kind)
				assert.Equal(t, "1.23", p.PaidCNY)
				assert.Equal(t, "1.5", p.Credited.Amount)
				assert.Equal(t, code.Id, p.RedemptionID)
				assert.Equal(t, "shop", p.SalesChannel)
				assert.NotZero(t, p.FactID)
				encoded, err := common.Marshal(p)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), codeKey)
				assert.NotContains(t, string(encoded), "private-sales-evidence")
				_, err = ReadMarketingAccountingPayments(context.Background(), user.Id, redeemed.PaymentID, filter)
				require.ErrorIs(t, err, model.ErrMarketingAccess)
				// Safe cash-refund projection has its own completed-operation record. The
				// generic consume reversal endpoint never masquerades as cash paid out.
				evidence, err := common.Marshal(map[string]any{"DisplayConversion": money, "Input": map[string]any{"AuthorizationReference": "secret-authorization"}})
				require.NoError(t, err)
				details, err := common.Marshal(map[string]any{"Evidence": string(evidence), "LocalIdentity": "private-operator", "AuthorizationReference": "secret-authorization"})
				require.NoError(t, err)
				result, err := common.Marshal(model.MarketingAssetClearResult{PaidQuota: 150, GiftQuota: 50, ExactRefundCNY: "1.809", RefundedCents: 181, RoundingDifferenceCNY: "0.001"})
				require.NoError(t, err)
				operation := model.MarketingMaintenanceOperation{OperationKey: "cash-" + tag, ActorID: admin.Id, UserID: other.Id, Kind: "asset_clear", PayloadHash: "cash-" + tag, Details: string(details), Result: string(result), RecordedAt: now}
				require.NoError(t, db.Create(&operation).Error)
				refund, err := ReadMarketingAccountingCashRefunds(context.Background(), admin.Id, operation.ID, filter)
				require.NoError(t, err)
				r := refund.(MarketingAccountingCashRefund)
				assert.Equal(t, "1.81", r.RefundedCNY)
				assert.Equal(t, "0.001", r.RoundingDifferenceCNY)
				assert.Equal(t, "1.5", r.ClearedPaid.Amount)
				assert.Equal(t, "0.5", r.ClearedGift.Amount)
				encoded, err = common.Marshal(r)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), "secret-authorization")
				assert.NotContains(t, string(encoded), "private-operator")
				_, err = ReadMarketingAccountingCashRefunds(context.Background(), user.Id, operation.ID, filter)
				require.ErrorIs(t, err, model.ErrMarketingAccess)
				t.Run("real cash clear preserves expired gifts and consumption history", func(t *testing.T) {
					// Reuse the paid-code receipt above: cash paid is 1.23 CNY,
					// while its independently recorded credited face is 1.50.
					activeMoney, expiredMoney := money, money
					activeMoney.Amount, expiredMoney.Amount = "0.5", "0.3"
					var activeGift, expiredGift *model.MarketingGift
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var e error
						activeGift, e = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: other.Id, Kind: "recharge", Reference: "clear-active-" + tag, GrantedAt: now - 20, ExpiresAt: now + 3600, Money: activeMoney})
						if e != nil {
							return e
						}
						expiredGift, e = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: other.Id, Kind: "checkin", Reference: "clear-expired-" + tag, GrantedAt: now - 20, ExpiresAt: now - 10, Money: expiredMoney})
						return e
					}))
					var request *model.MarketingFunding
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var e error
						request, e = model.ReserveMarketingFunding(tx, model.MarketingReservationInput{UserID: other.Id, Playground: true, RequestReference: "before-clear-" + tag, QualifiedAt: now, Quota: 70, PricingEvidence: string(pricing)})
						if e != nil {
							return e
						}
						actual := int64(70)
						request, e = model.SettleMarketingFunding(tx, model.MarketingSettlementInput{UserID: other.Id, FundingID: request.ID, Version: 1, ActualQuota: &actual, Final: true, EvidenceKind: "upstream_terminal", Evidence: "complete fee before refund"})
						return e
					}))
					// Add a permanent gift that remains unused at refund. The
					// already expired 0.30 must not appear in the usable total.
					permanentMoney := money
					permanentMoney.Amount = "0.4"
					var permanent *model.MarketingGift
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var e error
						permanent, e = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: other.Id, Kind: "registration", Reference: "clear-permanent-" + tag, GrantedAt: now, Money: permanentMoney})
						return e
					}))
					// Current wallet display is still the deliberately changed
					// 1000 units / 7 CNY setting from the earlier test.
					before, err := ReadMarketingWallet(context.Background(), other.Id)
					require.NoError(t, err)
					assert.Equal(t, "0.91", before.(map[string]any)["paid"].(*MarketingAccountingMoney).Amount)
					assert.Equal(t, "0.28", before.(map[string]any)["gift"].(*MarketingAccountingMoney).Amount)
					historical, err := ReadMarketingAccounting(context.Background(), other.Id, false, request.ID, MarketingAccountingFilter{Page: 1, PageSize: 20})
					require.NoError(t, err)
					assert.Equal(t, "0.2", historical.(MarketingAccountingRequest).Paid.Amount)
					assert.Equal(t, "0.5", historical.(MarketingAccountingRequest).Gift.Amount)
					require.NoError(t, db.Model(&model.User{}).Where("id = ?", other.Id).Update("status", common.UserStatusDisabled).Error)
					var account model.MarketingAccount
					require.NoError(t, db.First(&account, "user_id = ?", other.Id).Error)
					clearInput := model.MarketingAssetClearInput{ActorID: admin.Id, UserID: other.Id, Reference: "actual-clear-" + tag, Evidence: string(evidence), ConversionEvidence: "paid-code cash 1.23 / credited 150; remaining paid 130", LocalIdentity: "local-test-operator", AuthorizationReference: "manual-refund-confirmation", ExpectedPaidQuota: 130, ExpectedRevision: account.Revision, ExactRefundCNY: "1.066", RefundedCents: 107}
					pending, err := model.PlanMarketingClearQualificationsAuthorized(context.Background(), clearInput, 100)
					require.NoError(t, err)
					require.False(t, pending)
					var clearOperation *model.MarketingMaintenanceOperation
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var e error
						clearOperation, e = model.ClearMarketingAssets(tx, clearInput, now+1)
						return e
					}))
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						replay, e := model.ClearMarketingAssets(tx, clearInput, now+1)
						if e == nil {
							assert.Equal(t, clearOperation.ID, replay.ID)
						}
						return e
					}))
					realRefund, err := ReadMarketingAccountingCashRefunds(context.Background(), admin.Id, clearOperation.ID, filter)
					require.NoError(t, err)
					real := realRefund.(MarketingAccountingCashRefund)
					assert.Equal(t, "1.07", real.RefundedCNY)
					assert.Equal(t, "0.004", real.RoundingDifferenceCNY)
					assert.Equal(t, "1.3", real.ClearedPaid.Amount)
					assert.Equal(t, "0.4", real.ClearedGift.Amount)
					require.NoError(t, db.First(expiredGift, expiredGift.ID).Error)
					assert.EqualValues(t, 30, expiredGift.ExpiredQuota)
					assert.Zero(t, expiredGift.ConsumedQuota)
					assert.Zero(t, expiredGift.ClearedQuota)
					require.NoError(t, db.First(activeGift, activeGift.ID).Error)
					assert.EqualValues(t, 50, activeGift.ConsumedQuota)
					require.NoError(t, db.First(permanent, permanent.ID).Error)
					assert.EqualValues(t, 40, permanent.ClearedQuota)
					// Re-enable through the existing administrative status path:
					// old assets remain cleared; it is not a refund reversal.
					require.NoError(t, db.Model(&model.User{}).Where("id = ?", other.Id).Update("status", common.UserStatusEnabled).Error)
					after, err := ReadMarketingWallet(context.Background(), other.Id)
					require.NoError(t, err)
					assert.Equal(t, "0", after.(map[string]any)["paid"].(*MarketingAccountingMoney).Amount)
					assert.Equal(t, "0", after.(map[string]any)["gift"].(*MarketingAccountingMoney).Amount)
					retained, err := ReadMarketingAccounting(context.Background(), other.Id, false, request.ID, MarketingAccountingFilter{Page: 1, PageSize: 20})
					require.NoError(t, err)
					assert.Equal(t, historical, retained)
					retainedPayment, err := ReadMarketingAccountingPayments(context.Background(), admin.Id, redeemed.PaymentID, filter)
					require.NoError(t, err)
					assert.Equal(t, payment, retainedPayment)
					monthly, err := ReadMarketingPaidConsumption(context.Background(), other.Id, 0, false, month)
					require.NoError(t, err)
					assert.Equal(t, []MarketingAccountingMoney{{Amount: "0.2", Currency: "CNY"}}, monthly.(map[string]any)["amounts"], "cash clear and expiry are not usage refunds")
				})
				filter.UserID = user.Id
				page, err := ReadMarketingAccounting(context.Background(), admin.Id, true, 0, filter)
				require.NoError(t, err)
				assert.NotEmpty(t, page.(MarketingAdminPage).Items)
			})
		})
	}
}
