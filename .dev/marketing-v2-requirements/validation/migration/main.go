// This isolated migration probe is compiled once against the released baseline
// and once against the candidate. It never serves HTTP or calls payment routes.
package main

import (
	"bytes"
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
	for _, table := range []string{"marketing_v2_accounts", "marketing_v2_payments", "marketing_v2_gifts", "marketing_v2_funding", "marketing_v2_allocations", "marketing_v2_ledger", "marketing_v2_token_holds", "marketing_v2_funding_operations", "marketing_v2_debits", "marketing_v2_dispatches", "marketing_v2_usage_projections"} {
		if model.DB.Migrator().HasTable(table) != candidate {
			panic("unexpected migration state for " + table)
		}
	}
	if os.Args[1] == "seed" {
		// Synthetic disabled credentials. No customer data or usable account secrets.
		user := model.User{Id: 70001, Username: "mv2_baseline_fixture", Password: "disabled-test-fixture", DisplayName: "迁移保留", Status: 2, Quota: 123456, UsedQuota: 789, RequestCount: 9, AuthVersion: 1}
		must(model.DB.Create(&user).Error)
		must(model.DB.Create(&model.Token{Id: 70001, UserId: user.Id, Key: "mv2-disabled-fixture-token", Name: "migration token", Status: 2, RemainQuota: 333, UsedQuota: 55, ModelLimits: "model-a,model-b"}).Error)
		must(model.DB.Create(&model.Redemption{Id: 70001, UserId: user.Id, Key: "mv2-disabled-fixture-redemption", Name: "migration code", Status: 2, Quota: 456, CreatedTime: 1700000000}).Error)
		must(model.DB.Create(&model.TopUp{Id: 70001, UserId: user.Id, Amount: 900, Money: 8.75, TradeNo: "mv2-baseline-order", PaymentMethod: "alipay", PaymentProvider: "epay", CreateTime: 1700000000, CompleteTime: 1700000001, Status: "success"}).Error)
	} else if os.Args[1] != "verify" {
		panic("unknown operation")
	}
	var got snapshot
	must(model.DB.First(&got.User, 70001).Error)
	must(model.DB.First(&got.Token, 70001).Error)
	must(model.DB.First(&got.Code, 70001).Error)
	must(model.DB.First(&got.Payment, 70001).Error)
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
		if !bytes.Equal(previous, data) {
			panic("baseline records or indexes changed during migration")
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
