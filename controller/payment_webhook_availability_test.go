package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unsupportedPaymentTransport struct{ calls atomic.Int64 }

func (transport *unsupportedPaymentTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("unsupported provider must not receive a payment request")
}

func TestUnsupportedSubscriptionPaymentCannotCreateCheckout(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.TopUp{}, &model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingFactSequence{}, &model.MarketingActivityFact{}))
	require.NoError(t, db.FirstOrCreate(&model.MarketingFactSequence{ID: 1}).Error)
	user := model.User{Username: "payment-scope-user", Quota: 12345, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	plan := model.SubscriptionPlan{Title: "Configured plan", Enabled: true, StripePriceId: "price_test", CreemProductId: "prod_test", WaffoPancakeProductId: "product_test"}
	require.NoError(t, db.Create(&plan).Error)
	previousStripeAPI, previousStripeHook, previousCreemAPI, previousCreemHook := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.CreemApiKey, setting.CreemWebhookSecret
	previousMerchant, previousPrivate := setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey
	previousTransport := http.DefaultTransport
	transport := &unsupportedPaymentTransport{}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.CreemApiKey, setting.CreemWebhookSecret = previousStripeAPI, previousStripeHook, previousCreemAPI, previousCreemHook
		setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey = previousMerchant, previousPrivate
		http.DefaultTransport = previousTransport
	})
	setting.StripeApiSecret, setting.StripeWebhookSecret = "sk_test_isolated", "whsec_isolated"
	setting.CreemApiKey, setting.CreemWebhookSecret = "isolated-key", "isolated-secret"
	setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey = "isolated-merchant", "isolated-private"
	body, err := common.Marshal(map[string]int{"plan_id": plan.Id})
	require.NoError(t, err)
	for name, handler := range map[string]gin.HandlerFunc{"stripe": SubscriptionRequestStripePay, "creem": SubscriptionRequestCreemPay, "pancake": SubscriptionRequestWaffoPancakePay} {
		t.Run(name, func(t *testing.T) {
			router := gin.New()
			router.POST("/pay", func(c *gin.Context) { c.Set("id", user.Id) }, handler)
			request := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"message_key":"Unsupported payment channel"`)
			assert.Zero(t, transport.calls.Load())
			for _, record := range []any{&model.SubscriptionOrder{}, &model.TopUp{}, &model.MarketingPayment{}, &model.MarketingGift{}, &model.MarketingActivityFact{}} {
				var count int64
				require.NoError(t, db.Model(record).Count(&count).Error)
				assert.Zero(t, count)
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, 12345, user.Quota)
		})
	}
}

func confirmPaymentComplianceForTest(t *testing.T) {
	t.Helper()
	paymentSetting := operation_setting.GetPaymentSetting()
	originalConfirmed := paymentSetting.ComplianceConfirmed
	originalTermsVersion := paymentSetting.ComplianceTermsVersion
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = originalConfirmed
		paymentSetting.ComplianceTermsVersion = originalTermsVersion
	})
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
}

func TestPaymentInfoExcludesUnsupportedConfiguredChannels(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	previousMethods := operation_setting.PayMethods
	t.Cleanup(func() { operation_setting.PayMethods = previousMethods })
	operation_setting.PayMethods = []map[string]string{
		{"type": "alipay"}, {"type": "wxpay"}, {"type": "stripe"},
		{"type": "creem"}, {"type": "waffo"}, {"type": "waffo_pancake"},
	}
	router := gin.New()
	router.GET("/info", GetTopUpInfo)
	for range 2 {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/info", nil))
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				PayMethods []map[string]string `json:"pay_methods"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success)
		assert.Equal(t, []map[string]string{{"type": "alipay"}, {"type": "wxpay"}}, result.Data.PayMethods)
		assert.Len(t, operation_setting.PayMethods, 6, "reading supported methods must not mutate administrator settings")
	}
}

func TestStripeWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalPriceID := setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		setting.StripePriceId = originalPriceID
	})

	setting.StripeWebhookSecret = ""
	setting.StripeApiSecret = "sk_test_123"
	setting.StripePriceId = "price_123"
	require.False(t, isStripeWebhookEnabled())

	setting.StripeWebhookSecret = "whsec_test"
	require.False(t, isStripeWebhookEnabled(), "credentials must not enable an unsupported payment route")
	assertUnsupportedPaymentHTTP(t, RequestStripePay, StripeWebhook, `{"amount":10,"payment_method":"stripe"}`)

	setting.StripePriceId = ""
	require.False(t, isStripeWebhookEnabled())
}

func TestCreemWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalAPIKey := setting.CreemApiKey
	originalProducts := setting.CreemProducts
	originalWebhookSecret := setting.CreemWebhookSecret
	t.Cleanup(func() {
		setting.CreemApiKey = originalAPIKey
		setting.CreemProducts = originalProducts
		setting.CreemWebhookSecret = originalWebhookSecret
	})

	setting.CreemWebhookSecret = ""
	setting.CreemApiKey = "creem_api_key"
	setting.CreemProducts = `[{"productId":"prod_123"}]`
	require.False(t, isCreemWebhookEnabled())

	setting.CreemWebhookSecret = "creem_secret"
	require.False(t, isCreemWebhookEnabled(), "credentials must not enable an unsupported payment route")
	assertUnsupportedPaymentHTTP(t, RequestCreemPay, CreemWebhook, `{"product_id":"prod_123","payment_method":"creem"}`)

	setting.CreemProducts = "[]"
	require.False(t, isCreemWebhookEnabled())
}

func TestWaffoWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalEnabled := setting.WaffoEnabled
	originalSandbox := setting.WaffoSandbox
	originalAPIKey := setting.WaffoApiKey
	originalPrivateKey := setting.WaffoPrivateKey
	originalPublicCert := setting.WaffoPublicCert
	originalSandboxAPIKey := setting.WaffoSandboxApiKey
	originalSandboxPrivateKey := setting.WaffoSandboxPrivateKey
	originalSandboxPublicCert := setting.WaffoSandboxPublicCert
	t.Cleanup(func() {
		setting.WaffoEnabled = originalEnabled
		setting.WaffoSandbox = originalSandbox
		setting.WaffoApiKey = originalAPIKey
		setting.WaffoPrivateKey = originalPrivateKey
		setting.WaffoPublicCert = originalPublicCert
		setting.WaffoSandboxApiKey = originalSandboxAPIKey
		setting.WaffoSandboxPrivateKey = originalSandboxPrivateKey
		setting.WaffoSandboxPublicCert = originalSandboxPublicCert
	})

	setting.WaffoEnabled = true
	setting.WaffoSandbox = false
	setting.WaffoApiKey = ""
	setting.WaffoPrivateKey = "private"
	setting.WaffoPublicCert = "public"
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoApiKey = "api"
	require.False(t, isWaffoWebhookEnabled(), "credentials must not enable an unsupported payment route")
	assertUnsupportedPaymentHTTP(t, RequestWaffoPay, WaffoWebhook, `{"amount":10}`)

	setting.WaffoEnabled = false
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoEnabled = true
	setting.WaffoSandbox = true
	setting.WaffoSandboxApiKey = ""
	setting.WaffoSandboxPrivateKey = "sandbox_private"
	setting.WaffoSandboxPublicCert = "sandbox_public"
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoSandboxApiKey = "sandbox_api"
	require.False(t, isWaffoWebhookEnabled(), "sandbox credentials must not bypass the supported payment routes")
}

func TestWaffoPancakeWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalMerchantID := setting.WaffoPancakeMerchantID
	originalPrivateKey := setting.WaffoPancakePrivateKey
	originalProductID := setting.WaffoPancakeProductID
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = originalMerchantID
		setting.WaffoPancakePrivateKey = originalPrivateKey
		setting.WaffoPancakeProductID = originalProductID
	})

	// Complete credentials do not enable a channel outside this site's ledger.
	setting.WaffoPancakeMerchantID = ""
	setting.WaffoPancakePrivateKey = "private"
	setting.WaffoPancakeProductID = "product"
	require.False(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeMerchantID = "merchant"
	require.False(t, isWaffoPancakeWebhookEnabled(), "credentials must not enable an unsupported payment route")
	assertUnsupportedPaymentHTTP(t, RequestWaffoPancakePay, WaffoPancakeWebhook, `{"amount":10}`)

	setting.WaffoPancakeProductID = ""
	require.False(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeProductID = "product"
	setting.WaffoPancakePrivateKey = ""
	require.False(t, isWaffoPancakeWebhookEnabled())
}

// Unsupported routes must stop before creating a local or provider order. A
// fully configured gateway is deliberately exercised with no database handle.
func assertUnsupportedPaymentHTTP(t *testing.T, create, notify gin.HandlerFunc, body string) {
	t.Helper()
	previousDB := model.DB
	model.DB = nil
	defer func() { model.DB = previousDB }()
	router := gin.New()
	router.POST("/create", create)
	router.POST("/notify", notify)
	request := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	require.NotPanics(t, func() { router.ServeHTTP(response, request) })
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"message":"error"`)
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(`{"status":"paid"}`))
	require.NotPanics(t, func() { router.ServeHTTP(response, request) })
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestEpayWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalPayAddress := operation_setting.PayAddress
	originalEpayID := operation_setting.EpayId
	originalEpayKey := operation_setting.EpayKey
	originalPayMethods := operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.PayAddress = originalPayAddress
		operation_setting.EpayId = originalEpayID
		operation_setting.EpayKey = originalEpayKey
		operation_setting.PayMethods = originalPayMethods
	})

	operation_setting.PayAddress = "https://pay.example.com"
	operation_setting.EpayId = "epay_id"
	operation_setting.EpayKey = ""
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	require.False(t, isEpayWebhookEnabled())

	operation_setting.EpayKey = "epay_key"
	require.True(t, isEpayWebhookEnabled())

	operation_setting.PayMethods = nil
	require.False(t, isEpayWebhookEnabled())
}

func TestEpayCallbacksVerifyBeforeSettlement(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalPayAddress := operation_setting.PayAddress
	originalEpayID := operation_setting.EpayId
	originalEpayKey := operation_setting.EpayKey
	originalPayMethods := operation_setting.PayMethods
	originalServerAddress := system_setting.ServerAddress
	originalDB := model.DB
	t.Cleanup(func() {
		operation_setting.PayAddress = originalPayAddress
		operation_setting.EpayId = originalEpayID
		operation_setting.EpayKey = originalEpayKey
		operation_setting.PayMethods = originalPayMethods
		system_setting.ServerAddress = originalServerAddress
		model.DB = originalDB
	})
	operation_setting.PayAddress = "https://pay.example.com"
	operation_setting.EpayId = "epay_id"
	operation_setting.EpayKey = "test-key"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	system_setting.ServerAddress = "https://dashboard.example.com"
	// Rejected callbacks and valid non-success events must never reach settlement.
	// A nil database makes an accidental attempt to settle fail the test.
	model.DB = nil
	require.True(t, isEpayWebhookEnabled())

	// Both ambiguous maps below had this same valid signing string in v0.0.4.
	// This must fail because of parameter ambiguity, not a mismatched signature.
	ambiguousSign := epay.MD5String("money=1.00&name=item&out_trade_no=other&out_trade_no=order&pid=epay_id&trade_status=TRADE_SUCCESS&type=alipay", "test-key")
	pendingSign := epay.MD5String("money=1.00&name=item%26other&out_trade_no=order&pid=epay_id&trade_status=WAIT_BUYER_PAY&type=alipay", "test-key")
	cases := []struct {
		name         string
		productName  string
		tradeNo      string
		status       string
		sign         string
		validPending bool
	}{
		{"separator in product name", "item&out_trade_no=other", "order", epay.StatusTradeSuccess, ambiguousSign, false},
		{"separator in order number", "item", "other&out_trade_no=order", epay.StatusTradeSuccess, ambiguousSign, false},
		{"invalid signature", "item", "order", epay.StatusTradeSuccess, "invalid", false},
		{"missing signature", "item", "order", epay.StatusTradeSuccess, "", false},
		{"valid pending with literal percent escape", "item%26other", "order", "WAIT_BUYER_PAY", pendingSign, true},
	}
	endpoints := []struct {
		name             string
		path             string
		handler          gin.HandlerFunc
		status           int
		rejectedBody     string
		pendingBody      string
		rejectedLocation string
		pendingLocation  string
	}{
		{
			name: "topup notify", path: "/api/user/epay/notify", handler: EpayNotify,
			status: http.StatusOK, rejectedBody: "fail", pendingBody: "success",
		},
		{
			name: "subscription notify", path: "/api/subscription/epay/notify", handler: SubscriptionEpayNotify,
			status: http.StatusOK, rejectedBody: "fail", pendingBody: "fail",
		},
		{
			name: "subscription return", path: "/api/subscription/epay/return", handler: SubscriptionEpayReturn,
			status:           http.StatusFound,
			rejectedLocation: "https://dashboard.example.com/wallet?pay=fail",
			pendingLocation:  "https://dashboard.example.com/wallet?pay=pending",
		},
	}
	router := gin.New()
	for _, endpoint := range endpoints {
		router.Any(endpoint.path, endpoint.handler)
	}
	for _, endpoint := range endpoints {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, tc := range cases {
				t.Run(endpoint.name+"/"+method+"/"+tc.name, func(t *testing.T) {
					params := url.Values{
						"pid": {"epay_id"}, "type": {"alipay"}, "money": {"1.00"},
						"name": {tc.productName}, "out_trade_no": {tc.tradeNo},
						"trade_status": {tc.status}, "sign_type": {"MD5"},
					}
					if tc.sign != "" {
						params.Set("sign", tc.sign)
					}
					request := httptest.NewRequest(method, endpoint.path+"?"+params.Encode(), nil)
					if method == http.MethodPost {
						request = httptest.NewRequest(method, endpoint.path, strings.NewReader(params.Encode()))
						request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					}
					recorder := httptest.NewRecorder()
					require.NotPanics(t, func() { router.ServeHTTP(recorder, request) })
					assert.Equal(t, endpoint.status, recorder.Code)
					wantBody, wantLocation := endpoint.rejectedBody, endpoint.rejectedLocation
					if tc.validPending {
						wantBody, wantLocation = endpoint.pendingBody, endpoint.pendingLocation
					}
					if wantBody != "" {
						assert.Equal(t, wantBody, recorder.Body.String())
					}
					assert.Equal(t, wantLocation, recorder.Header().Get("Location"))
				})
			}
		}
	}
}
