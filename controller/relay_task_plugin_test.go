package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/volcengine"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func (b *taskSubmissionTestBilling) ReserveBeforeSubmit(quota int) error { return b.Reserve(quota) }

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

// The submit presenter may answer with the vendor's status; a native route that
// already relayed the vendor's event stream gets nothing more.
func TestPresentTaskSubmissionHonorsReplyWrapperAndRelayedStreams(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"accepted-test",name:"Accepted",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {__status:202,__body:{id:task.task_id}};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	present := func(recorder *httptest.ResponseRecorder, written bool) {
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
		c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
		c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})
		if written {
			_, _ = c.Writer.WriteString("data: {}\n\n")
		}
		presentTaskSubmission(c, &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: &model.Task{TaskID: "task_public"}, RelayInfo: &relaycommon.RelayInfo{}})
	}

	accepted := httptest.NewRecorder()
	present(accepted, false)
	assert.Equal(t, http.StatusAccepted, accepted.Code)
	assert.JSONEq(t, `{"id":"task_public"}`, accepted.Body.String())

	streamed := httptest.NewRecorder()
	present(streamed, true)
	assert.Equal(t, "data: {}\n\n", streamed.Body.String())
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionRefundsWhenInsertFails(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, false, &events)
	_ = database
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_insert_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, settleErr: errors.New("settlement failed")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_billing_settlement_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := taskSubmissionTestContext()
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{})
			previousDB := model.DB
			model.DB = database
			t.Cleanup(func() { model.DB = previousDB })
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

			c := taskSubmissionTestContext()
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
		})
	}
}

func TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 2)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		cancel()
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitted := false

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitted = true
		return nil, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.False(t, submitted)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitStarted := make(chan struct{})
	done := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError

	go func() {
		defer close(done)
		outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusInternalServerError)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not stop after disconnect")
	}

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	billing := &taskSubmissionTestBilling{
		events:   &events,
		onSettle: cancel,
	}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "task_public", outcome.Task.TaskID)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

// setupTaskSubmissionDatabase opens the dialect selected by
// TEST_TASK_DB_DIALECT (SQLite in memory by default) and records every task
// INSERT in events so tests can assert the reserve → insert → settle order.
// Without migrate the task table does not exist and inserts fail.
func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Unique table prefixes keep the fixture isolated from all existing tables.
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	models = append(models, &model.MarketingAccount{}, &model.MarketingFactSequence{})
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(":memory:")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, db.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.QuotaData{},
		&model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{},
		&model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{},
		&model.MarketingActivity{}, &model.MarketingActivityOperation{}, &model.MarketingInvitation{}, &model.MarketingActivityFact{},
		&model.MarketingActivityProgress{}, &model.MarketingActivityDecision{}, &model.MarketingActivityReward{}, &model.MarketingActivityDeliveryCursor{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status string
		actual       any
		count        float64
		review       bool
	}{
		{"fixed price without usage", "SUCCESS", nil, 3, false}, {"partial", "SUCCESS", 2, 2, false}, {"zero", "SUCCESS", 0, 0, false}, {"larger", "SUCCESS", 6, 6, false},
		{"invalid usage", "SUCCESS", -1, 4, true}, {"expression failure", "SUCCESS", 7, 4, true}, {"negative result", "SUCCESS", 8, 4, true}, {"missing usage", "SUCCESS", nil, 4, true}, {"failed", "FAILURE", 9, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixed := tc.name == "fixed price without usage"
			if fixed {
				withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": `tier("request", 0.03)`})
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
			now := time.Now().Unix()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "immediate-" + tc.name, PaidCents: 2000, PostedAt: now,
					Money: model.MarketingMoneySnapshot{Amount: "20", QuotaPerUnit: fmt.Sprint(common.QuotaPerUnit), CurrencyPerUnit: "1", Currency: "CNY"}})
				return err
			}))
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.StartTime, info.RequestId = time.Unix(now, 0), "immediate-http-"+tc.name
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.LockedChannel = &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			// The adapter may retain an estimate for diagnostics. Unknown usage
			// must never become an actual wallet debit or a settled task cost.
			charged := want
			if tc.review {
				charged = 0
			}
			assert.Equal(t, charged, stored.Quota)
			assert.False(t, stored.MarketingBillingPending)
			require.NotNil(t, stored.PrivateData.MarketingOutcome)
			var funding model.MarketingFunding
			require.NoError(t, db.First(&funding, stored.MarketingFundingID).Error)
			assert.Equal(t, int64(charged), funding.SettledQuota)
			assert.Equal(t, !tc.review, funding.CostKnown)
			if tc.review {
				assert.Equal(t, "review", funding.State)
				assert.Equal(t, int64(common.QuotaRound(0.04*common.QuotaPerUnit)), funding.ReservedQuota)
				assert.Nil(t, stored.PrivateData.MarketingOutcome.ActualQuota)
			} else {
				assert.Equal(t, "settled", funding.State)
				assert.Zero(t, funding.ReservedQuota)
				require.NotNil(t, stored.PrivateData.MarketingOutcome.ActualQuota)
				assert.Equal(t, int64(charged), *stored.PrivateData.MarketingOutcome.ActualQuota)
			}
			var rawResult map[string]any
			require.NoError(t, common.Unmarshal(stored.Data, &rawResult))
			if tc.actual != nil {
				assert.Equal(t, float64(tc.actual.(int)), rawResult["usage"].(map[string]any)["units"], "preserve actual provider facts even if billing requires review")
			} else {
				assert.NotContains(t, rawResult, "usage")
			}
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			estimatedUnits, usageUnits := float64(4), tc.count
			if fixed {
				estimatedUnits, usageUnits = 3, 4
			}
			assert.Equal(t, estimatedUnits, info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, usageUnits, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-charged, updated.Quota)
			assert.Equal(t, charged, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, charged, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			if tc.status == "SUCCESS" {
				assert.Equal(t, usageUnits, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			// Recovery replays the frozen outcome, not a fresh interpretation of
			// the adapter's estimate. Even a later refund cannot release review holds.
			require.NoError(t, service.FinalizeMarketingTask(&stored))
			info.Billing.Refund(c)
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-charged, updated.Quota, "terminal settlement is idempotent")
			assert.Equal(t, charged, updated.UsedQuota)
			var replayed model.MarketingFunding
			require.NoError(t, db.First(&replayed, funding.ID).Error)
			assert.Equal(t, funding.State, replayed.State)
			assert.Equal(t, funding.ReservedQuota, replayed.ReservedQuota)
			assert.Equal(t, funding.SettledQuota, replayed.SettledQuota)
			var logCount int64
			require.NoError(t, db.Model(&model.Log{}).Where("user_id = ?", user.Id).Count(&logCount).Error)
			assert.Equal(t, int64(1), logCount, "recovery must not duplicate the request log")
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "task_accepted", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
}

// Real HTTP submission, plugin parsing, durable funding, controller persistence
// and task recovery. Only the external model provider is an HTTP fixture.
func TestMarketingTaskSubmissionDatabase(t *testing.T) {
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
	})
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.QuotaData{},
		&model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{},
		&model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{},
		&model.MarketingActivity{}, &model.MarketingActivityOperation{}, &model.MarketingInvitation{}, &model.MarketingActivityFact{},
		&model.MarketingActivityProgress{}, &model.MarketingActivityDecision{}, &model.MarketingActivityReward{}, &model.MarketingActivityDeliveryCursor{})
	previousDB, previousLogs := model.DB, model.LOG_DB
	mainType, logType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() { model.DB, model.LOG_DB = previousDB, previousLogs; common.SetDatabaseTypes(mainType, logType) })
	withTieredBillingConfig(t, map[string]string{"marketing-video": "tiered_expr"}, map[string]string{"marketing-video": `tier("usage", u("units") * 0.01)`})
	plugin, err := pluginruntime.CompilePlugin(`
export const meta={apiVersion:1,key:"marketing-submit",name:"Marketing submit",version:"1.0.0",author:{name:"Test"},models:["marketing-video"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/job",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:resp.body.pending ? undefined : {status:"SUCCESS"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return {units:body.units};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export function buildQueryRequest(ctx){return {url:ctx.baseUrl+"/status"};}
`, pluginruntime.Options{})
	require.NoError(t, err)
	for index, scenario := range []string{"immediate", "unknown_usage", "pending", "cancel_after_accept", "ambiguous_response"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			ctx := taskSubmissionTestContext()
			requestContext, cancel := context.WithCancel(ctx.Request.Context())
			defer cancel()
			ctx.Request = ctx.Request.WithContext(requestContext)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if scenario == "ambiguous_response" {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				body := `{"units":2}`
				if scenario == "unknown_usage" {
					body = `{"units":-1}`
				}
				if scenario == "pending" || scenario == "cancel_after_accept" {
					body = `{"pending":true}`
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			user := model.User{Username: fmt.Sprintf("marketing_task_%d", index), AffCode: fmt.Sprintf("mkta%d", index), Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
			now := time.Now().Unix()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: scenario, PaidCents: 100, PostedAt: now,
					Money: model.MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: fmt.Sprint(common.QuotaPerUnit), CurrencyPerUnit: "1", Currency: "CNY"}})
				if err != nil {
					return err
				}
				_, err = model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "recharge", Reference: scenario, GrantedAt: now, ExpiresAt: now + 3600,
					Money: model.MarketingMoneySnapshot{Amount: "0.01", QuotaPerUnit: fmt.Sprint(common.QuotaPerUnit), CurrencyPerUnit: "1", Currency: "CNY"}})
				return err
			}))
			ch := model.Channel{Name: "marketing provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			ctx.Set("group", "default")
			ctx.Set("username", user.Username)
			ctx.Set("task_request", map[string]any{"model": "marketing-video"})
			ctx.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "marketing-video")
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(ctx, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId, info.IsPlayground = user.Id, true
			info.StartTime, info.RequestId = time.Unix(now, 0), "marketing-http-"+scenario
			info.OriginModelName, info.UserGroup = "marketing-video", "default"
			info.PublicTaskID, info.LockedChannel = model.GenerateTaskID(), &ch
			outcome, taskErr := executeTaskSubmissionWith(ctx, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				result, err := relay.RelayTaskSubmit(c, info)
				if scenario == "cancel_after_accept" {
					cancel()
				}
				return result, err
			})
			assert.Equal(t, 1, calls, "ambiguous execution must not submit another billable operation")
			var funding model.MarketingFunding
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&funding).Error)
			if scenario == "ambiguous_response" {
				require.NotNil(t, taskErr)
				assert.Equal(t, "review", funding.State)
				assert.Positive(t, funding.ReservedQuota)
				return
			}
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			require.Positive(t, outcome.Task.ID)
			require.NoError(t, db.First(&user, user.Id).Error)
			if scenario == "pending" || scenario == "cancel_after_accept" {
				assert.Equal(t, "reserved", funding.State)
				assert.True(t, outcome.Task.MarketingBillingPending)
				assert.Equal(t, common.QuotaRound(common.QuotaPerUnit), user.Quota)
				assert.Zero(t, user.UsedQuota)
				return
			}
			assert.False(t, outcome.Task.MarketingBillingPending)
			if scenario == "unknown_usage" {
				assert.Equal(t, "review", funding.State)
				assert.Zero(t, user.UsedQuota)
			} else {
				assert.Equal(t, "settled", funding.State)
				assert.Equal(t, common.QuotaRound(0.02*common.QuotaPerUnit), user.UsedQuota)
				assert.Equal(t, common.QuotaRound(0.99*common.QuotaPerUnit), user.Quota)
			}
		})
	}
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

func TestExecuteTaskSubmissionRefundsWhenFinalReserveFails(t *testing.T) {
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, reserveErr: errors.New("insufficient funds")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: "SUCCESS"}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.Equal(t, []string{"reserve", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

// Exercise the Midjourney submission and polling bypass using real HTTP and
// the shared funding ledger. Unknown provider outcomes must not look refunded.
func TestMarketingMidjourneySubmissionDatabase(t *testing.T) {
	service.InitHttpClient()
	oldRedis, oldMemory, oldBatch, oldLogs := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, true
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldLogs
	})
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Midjourney{}, &model.Log{}, &model.QuotaData{},
		&model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{},
		&model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{},
		&model.MarketingActivity{}, &model.MarketingActivityOperation{}, &model.MarketingInvitation{}, &model.MarketingActivityFact{},
		&model.MarketingActivityProgress{}, &model.MarketingActivityDecision{}, &model.MarketingActivityReward{}, &model.MarketingActivityDeliveryCursor{})
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLog; common.SetDatabaseTypes(oldMainType, oldLogType) })
	for index, scenario := range []string{"success", "provider_failure", "timeout", "ambiguous", "swap_face_gift_only"} {
		t.Run(scenario, func(t *testing.T) {
			var submits atomic.Int32
			upstreamID := "mj-" + scenario
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/mj/task/list-by-condition" {
					status, progress := "SUCCESS", "100%"
					if scenario == "provider_failure" {
						status = "FAILURE"
					}
					if scenario == "timeout" {
						status, progress = "IN_PROGRESS", "50%"
					}
					_, _ = fmt.Fprintf(w, `[{"id":%q,"status":%q,"progress":%q}]`, upstreamID, status, progress)
					return
				}
				submits.Add(1)
				if scenario == "ambiguous" {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"code":4,"description":"unknown"}`))
					return
				}
				_, _ = fmt.Fprintf(w, `{"code":1,"result":%q}`, upstreamID)
			}))
			defer server.Close()
			user := model.User{Username: fmt.Sprintf("marketing_mj_%d", index), AffCode: fmt.Sprintf("mkmj%d", index), Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
			token := model.Token{UserId: user.Id, Key: "mj-token-" + scenario, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: common.QuotaRound(10 * common.QuotaPerUnit)}
			require.NoError(t, db.Create(&token).Error)
			now := time.Now().Unix()
			giftAmount := "0.001"
			if scenario == "swap_face_gift_only" {
				giftAmount = "10"
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				if scenario != "swap_face_gift_only" {
					_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "mj-" + scenario, PaidCents: 100, PostedAt: now,
						Money: model.MarketingMoneySnapshot{Amount: "1", QuotaPerUnit: fmt.Sprint(common.QuotaPerUnit), CurrencyPerUnit: "1", Currency: "CNY"}})
					if err != nil {
						return err
					}
				}
				_, err := model.GrantMarketingGift(tx, model.MarketingGiftInput{UserID: user.Id, Kind: "recharge", Reference: "mj-" + scenario, GrantedAt: now, ExpiresAt: now + 3600,
					Money: model.MarketingMoneySnapshot{Amount: giftAmount, QuotaPerUnit: fmt.Sprint(common.QuotaPerUnit), CurrencyPerUnit: "1", Currency: "CNY"}})
				return err
			}))
			baseURL := server.URL
			channel := model.Channel{Name: "MJ fixture", Type: constant.ChannelTypeMidjourney, BaseURL: &baseURL, Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest("POST", "/mj/submit/imagine", strings.NewReader(`{"prompt":"test image"}`))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("base_url", server.URL)
			ctx.Set("channel_id", channel.Id)
			ctx.Set("username", user.Username)
			ctx.Set("group", "default")
			common.SetContextKey(ctx, constant.ContextKeyChannelType, channel.Type)
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(ctx, constant.ContextKeyChannelId, channel.Id)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, RequestId: "mj-http-" + scenario, StartTime: time.Unix(now, 0), OriginModelName: "mj_imagine", UsingGroup: "default", UserGroup: "default", RelayMode: relayconstant.RelayModeMidjourneyImagine}
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, info.OriginModelName)
			var mjErr *dto.MidjourneyResponse
			if scenario == "swap_face_gift_only" {
				info.OriginModelName = "swap_face"
				ctx.Request = httptest.NewRequest("POST", "/mj/insight-face/swap", strings.NewReader(`{"sourceBase64":"a","targetBase64":"b"}`))
				ctx.Request.Header.Set("Content-Type", "application/json")
				mjErr = relay.RelaySwapFace(ctx, info)
			} else {
				mjErr = relay.RelayMidjourneySubmit(ctx, info)
			}
			require.Nil(t, mjErr)
			assert.EqualValues(t, 1, submits.Load())
			var task model.Midjourney
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&task).Error)
			require.Positive(t, task.MarketingFundingID)
			var funding model.MarketingFunding
			require.NoError(t, db.First(&funding, task.MarketingFundingID).Error)
			if scenario == "ambiguous" {
				assert.Equal(t, "review", funding.State)
				assert.Positive(t, funding.ReservedQuota)
				assert.Zero(t, funding.SettledQuota)
				return
			}
			assert.Equal(t, "reserved", funding.State)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Zero(t, user.UsedQuota)
			// A late trusted success remains billable; elapsed time alone cannot
			// override a final provider result into a timeout refund.
			require.NoError(t, db.Model(&task).Update("submit_time", (now-7200)*1000).Error)
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			require.NoError(t, service.RecoverMarketingMidjourney(context.Background()))
			require.NoError(t, db.First(&funding, task.MarketingFundingID).Error)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			var rows []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&rows).Error)
			require.Len(t, rows, 1)
			assert.Equal(t, 1, user.RequestCount)
			assert.EqualValues(t, 1, submits.Load(), "polling/recovery must never resubmit")
			if scenario == "timeout" {
				assert.Equal(t, "review", funding.State)
				assert.Positive(t, funding.ReservedQuota)
				assert.Zero(t, user.UsedQuota)
			} else if scenario == "provider_failure" {
				assert.Equal(t, "settled", funding.State)
				assert.Zero(t, funding.SettledQuota)
				assert.Zero(t, funding.ReservedQuota)
			} else {
				assert.Equal(t, "settled", funding.State)
				assert.EqualValues(t, info.PriceData.Quota, funding.SettledQuota)
				assert.Equal(t, info.PriceData.Quota, user.UsedQuota)
				assert.Equal(t, info.PriceData.Quota, rows[0].Quota)
				assert.Equal(t, common.QuotaRound(10*common.QuotaPerUnit)-info.PriceData.Quota, token.RemainQuota)
				if scenario == "swap_face_gift_only" {
					assert.Zero(t, user.Quota)
				}
			}
		})
	}
}

// Single-response TTS uses a WebSocket transport but is not a Realtime session.
// Its actual adapter must preserve the same dispatch and unknown-cost boundary.
func TestMarketingSingleWebSocketDatabase(t *testing.T) {
	require.NoError(t, i18n.Init())
	oldRedis, oldMemory, oldBatch, oldLogs := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, true
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldLogs
	})
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Log{}, &model.QuotaData{},
		&model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFunding{}, &model.MarketingAllocation{}, &model.MarketingLedgerEntry{},
		&model.MarketingTokenHold{}, &model.MarketingFundingOperation{}, &model.MarketingDebit{}, &model.MarketingDispatch{}, &model.MarketingUsageProjection{},
		&model.MarketingActivity{}, &model.MarketingActivityOperation{}, &model.MarketingInvitation{}, &model.MarketingActivityFact{},
		&model.MarketingActivityProgress{}, &model.MarketingActivityDecision{}, &model.MarketingActivityReward{}, &model.MarketingActivityDeliveryCursor{})
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLog; common.SetDatabaseTypes(oldMain, oldLogType) })
	for index, scenario := range []string{"confirmed", "disconnect", "normal_close_without_terminal"} {
		t.Run(scenario, func(t *testing.T) {
			var accepted atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, err := volcengine.ReceiveMessage(conn); err != nil {
					return
				}
				accepted.Add(1)
				if scenario == "normal_close_without_terminal" {
					_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
				if scenario == "confirmed" {
					msg, err := volcengine.NewMessage(volcengine.MsgTypeAudioOnlyServer, volcengine.MsgTypeFlagNegativeSeq)
					if err != nil {
						t.Error(err)
						return
					}
					msg.Sequence, msg.Payload = -1, []byte("confirmed-audio")
					frame, err := msg.Marshal()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.WriteMessage(websocket.BinaryMessage, frame)
				}
			}))
			defer server.Close()
			user := model.User{Username: fmt.Sprintf("single_ws_%d", index), AffCode: fmt.Sprintf("sw%d", index), Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&model.MarketingAccount{UserID: user.Id, OriginState: "verified", PaymentHistory: model.MarketingPaymentHistoryNone, PaymentHistoryEvidence: "test registration"}).Error)
			token := model.Token{UserId: user.Id, Key: "single-ws-" + scenario, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
			require.NoError(t, db.Create(&token).Error)
			now := common.GetTimestamp()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := model.CreditMarketingPayment(tx, model.MarketingPaymentInput{UserID: user.Id, Kind: "online", Reference: "single-ws-" + scenario, PaidCents: 100, PostedAt: now,
					Money: model.MarketingMoneySnapshot{Amount: "100", QuotaPerUnit: "1", CurrencyPerUnit: "1", Currency: "CNY"}})
				return err
			}))
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, RequestId: "single-ws-" + scenario, StartTime: time.Unix(now, 0),
				IsStream: true, RequestUsageMeasured: true, RelayMode: relayconstant.RelayModeAudioSpeech, RelayFormat: kittypes.RelayFormatOpenAIAudio,
				OriginModelName: "tts-fixture", ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "ws" + strings.TrimPrefix(server.URL, "http"), ApiKey: "fixture-app|fixture-token"},
				PriceData: types.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}}
			info.SetEstimatePromptTokens(4)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
			session, err := service.NewMarketingBillingSession(info, 20)
			require.NoError(t, err)
			info.Billing = session
			adaptor := &volcengine.Adaptor{}
			_, err = adaptor.ConvertAudioRequest(ctx, info, kitdto.AudioRequest{Model: "tts-fixture", Input: "test", Voice: "alloy"})
			require.NoError(t, err)
			usage, apiErr := adaptor.DoResponse(ctx, nil, info)
			if scenario == "disconnect" {
				require.NotNil(t, apiErr)
				session.Refund(ctx)
			} else {
				require.Nil(t, apiErr)
				service.PostTextConsumeQuota(ctx, info, usage.(*kitdto.Usage), nil)
			}
			require.EqualValues(t, 1, accepted.Load())
			var funding model.MarketingFunding
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&funding).Error)
			var dispatches int64
			require.NoError(t, db.Model(&model.MarketingDispatch{}).Where("funding_id = ?", funding.ID).Count(&dispatches).Error)
			assert.EqualValues(t, 1, dispatches)
			require.NoError(t, db.First(&token, token.Id).Error)
			charge := int64(4)
			if scenario == "disconnect" {
				charge = 0
			}
			assert.Equal(t, "settled", funding.State)
			assert.True(t, funding.CostKnown)
			assert.Equal(t, charge, funding.SettledQuota)
			assert.Zero(t, funding.ReservedQuota)
			assert.EqualValues(t, 100-charge, token.RemainQuota)
			var hold model.MarketingTokenHold
			require.NoError(t, db.Where("token_id = ?", token.Id).First(&hold).Error)
			assert.Zero(t, hold.ReservedQuota)

			_, apiErr = adaptor.DoResponse(ctx, nil, info)
			require.NotNil(t, apiErr)
			assert.EqualValues(t, 1, accepted.Load(), "retries must not send another synthesis request")
		})
	}
}
