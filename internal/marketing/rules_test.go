package marketing

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRechargeFiveApprovedModes(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	window := EligibilityWindow{Period: Period{Start: start}}
	tests := []struct {
		name      string
		condition RechargeCondition
		frequency RewardFrequency
		amounts   []int64
		expected  []RechargeProgress
		awards    []int64
	}{
		{"first payment below threshold never recovers", FirstSuccessfulPayment, RewardOnce, []int64{3000, 10000}, []RechargeProgress{{0, 0}, {0, 0}}, []int64{0, 0}},
		{"first payment qualifies only once", FirstSuccessfulPayment, RewardOnce, []int64{10000, 10000}, []RechargeProgress{{1, 0}, {1, 0}}, []int64{1, 0}},
		{"first qualifying single payment", SinglePayment, RewardOnce, []int64{3000, 7000, 10000, 25000}, []RechargeProgress{{0, 0}, {0, 0}, {1, 0}, {1, 0}}, []int64{0, 0, 1, 0}},
		{"each qualifying payment not each multiple", SinglePayment, RewardEvery, []int64{3000, 7000, 10000, 25000}, []RechargeProgress{{0, 0}, {0, 0}, {1, 0}, {2, 0}}, []int64{0, 0, 1, 1}},
		{"first cumulative threshold", CumulativePayments, RewardOnce, []int64{3000, 7000, 25000}, []RechargeProgress{{0, 3000}, {1, 0}, {1, 0}}, []int64{0, 1, 0}},
		{"every cumulative threshold retains remainder", CumulativePayments, RewardEvery, []int64{3000, 7000, 25000, 5000}, []RechargeProgress{{0, 3000}, {1, 0}, {3, 5000}, {4, 0}}, []int64{0, 1, 2, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			progress := RechargeProgress{}
			for i, amount := range tt.amounts {
				// These are verified input facts. This unit test does not prove database
				// posting-order assignment or event deduplication, which come in later Issues.
				got, err := EvaluateRecharge(RechargeRule{tt.condition, tt.frequency, 10000}, window, SuccessfulPayment{amount, start.Add(time.Duration(i) * time.Second), i == 0}, progress)
				require.NoError(t, err)
				assert.True(t, got.Eligible)
				assert.Equal(t, tt.awards[i], got.NewAwards)
				assert.Equal(t, tt.expected[i], got.Next)
				progress = got.Next
			}
		})
	}
}

func TestRechargeUsesPostedTimeAndHistoricalPauses(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	pauseStart := start.Add(time.Hour)
	pauseEnd := start.Add(2 * time.Hour)
	window := EligibilityWindow{Period: Period{start, &end}, Pauses: []Period{{pauseStart, &pauseEnd}}}
	rule := RechargeRule{SinglePayment, RewardEvery, 10000}
	for _, tt := range []struct {
		name     string
		at       time.Time
		eligible bool
	}{
		{"before start", start.Add(-time.Second), false},
		{"at start", start, true},
		{"before pause still eligible when handled later", pauseStart.Add(-time.Second), true},
		{"pause start", pauseStart, false},
		{"during pause not eligible after resume", pauseEnd.Add(-time.Second), false},
		{"resume", pauseEnd, true},
		{"before end remains eligible after activity ends", end.Add(-time.Second), true},
		{"at end", end, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateRecharge(rule, window, SuccessfulPayment{10000, tt.at, false}, RechargeProgress{})
			require.NoError(t, err)
			assert.Equal(t, tt.eligible, got.Eligible)
			if tt.eligible {
				assert.EqualValues(t, 1, got.NewAwards)
			} else {
				assert.EqualValues(t, 0, got.NewAwards)
			}
		})
	}
	// Current publication and historical payment eligibility are separate decisions.
	display, err := DisplayActivity(window.Period, false, end.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, ActivityHidden, display)
	got, err := EvaluateRecharge(rule, window, SuccessfulPayment{10000, start, false}, RechargeProgress{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.NewAwards)
	// A first posting outside the activity cannot make the next posting first.
	firstRule := RechargeRule{FirstSuccessfulPayment, RewardOnce, 10000}
	got, err = EvaluateRecharge(firstRule, window, SuccessfulPayment{10000, start.Add(-time.Second), true}, RechargeProgress{})
	require.NoError(t, err)
	got, err = EvaluateRecharge(firstRule, window, SuccessfulPayment{10000, start, false}, got.Next)
	require.NoError(t, err)
	assert.Zero(t, got.NewAwards)
}

func TestActivityDisplayBoundaries(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(20 * 24 * time.Hour)
	for _, tt := range []struct {
		name   string
		at     time.Time
		end    *time.Time
		public bool
		want   ActivityDisplay
	}{
		{"not published", start, &end, false, ActivityHidden},
		{"future", start.Add(-time.Second), &end, true, ActivityUpcoming},
		{"start inclusive", start, &end, true, ActivityActive},
		{"before end", end.Add(-time.Second), &end, true, ActivityActive},
		{"end exclusive", end, &end, true, ActivityRecentlyEnded},
		{"one second before 72 hours", end.Add(72*time.Hour - time.Second), &end, true, ActivityRecentlyEnded},
		{"exactly 72 hours", end.Add(72 * time.Hour), &end, true, ActivityHidden},
		{"long running", end.AddDate(10, 0, 0), nil, true, ActivityActive},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DisplayActivity(Period{start, tt.end}, tt.public, tt.at)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
	// Same instant with another time-zone representation must not alter visibility.
	got, err := DisplayActivity(Period{start, &end}, true, end.Add(72*time.Hour).In(time.FixedZone("offset", 8*60*60)))
	require.NoError(t, err)
	assert.Equal(t, ActivityHidden, got)
}

func TestRechargeRejectsInvalidInputsAndPreventsOverflow(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	window := EligibilityWindow{Period: Period{Start: start}}
	rule := RechargeRule{CumulativePayments, RewardEvery, 10000}
	payment := SuccessfulPayment{10000, start, false}
	for _, tt := range []struct {
		name     string
		rule     RechargeRule
		payment  SuccessfulPayment
		progress RechargeProgress
		want     error
	}{
		{"negative payment", rule, SuccessfulPayment{-1, start, false}, RechargeProgress{}, ErrInvalidPayment},
		{"zero payment", rule, SuccessfulPayment{0, start, false}, RechargeProgress{}, ErrInvalidPayment},
		{"missing posting time", rule, SuccessfulPayment{10000, time.Time{}, false}, RechargeProgress{}, ErrInvalidPayment},
		{"zero threshold", RechargeRule{CumulativePayments, RewardEvery, 0}, payment, RechargeProgress{}, ErrInvalidRule},
		{"negative threshold", RechargeRule{CumulativePayments, RewardEvery, -1}, payment, RechargeProgress{}, ErrInvalidRule},
		{"unknown condition", RechargeRule{"other", RewardOnce, 1}, payment, RechargeProgress{}, ErrInvalidRule},
		{"unknown frequency", RechargeRule{SinglePayment, "other", 1}, payment, RechargeProgress{}, ErrInvalidRule},
		{"first cannot repeat", RechargeRule{FirstSuccessfulPayment, RewardEvery, 1}, payment, RechargeProgress{}, ErrInvalidRule},
		{"negative processed", rule, payment, RechargeProgress{-1, 0}, ErrInvalidProgress},
		{"negative remainder", rule, payment, RechargeProgress{0, -1}, ErrInvalidProgress},
		{"unprocessed full threshold", rule, payment, RechargeProgress{0, 10000}, ErrInvalidProgress},
		{"once processed twice", RechargeRule{SinglePayment, RewardOnce, 10000}, payment, RechargeProgress{2, 0}, ErrInvalidProgress},
		{"single cannot combine remainder", RechargeRule{SinglePayment, RewardEvery, 10000}, payment, RechargeProgress{0, 1}, ErrInvalidProgress},
		{"count overflow", rule, payment, RechargeProgress{math.MaxInt64, 0}, ErrProgressOverflow},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateRecharge(tt.rule, window, tt.payment, tt.progress)
			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, tt.progress, got.Next)
		})
	}
	// Total cents can exceed int64 while the quotient/remainder still fit.
	got, err := EvaluateRecharge(RechargeRule{CumulativePayments, RewardEvery, math.MaxInt64}, window, SuccessfulPayment{math.MaxInt64, start, false}, RechargeProgress{0, math.MaxInt64 - 1})
	require.NoError(t, err)
	assert.Equal(t, RechargeProgress{1, math.MaxInt64 - 1}, got.Next)
	got, err = EvaluateRecharge(RechargeRule{CumulativePayments, RewardEvery, 1}, window, SuccessfulPayment{math.MaxInt64, start, false}, RechargeProgress{})
	require.NoError(t, err)
	assert.EqualValues(t, math.MaxInt64, got.NewAwards)
	assert.Zero(t, got.Next.RemainderCents)
}

func TestInvalidActivityPeriods(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	earlier := start.Add(-time.Second)
	zero := time.Time{}
	for _, period := range []Period{{}, {start, &start}, {start, &earlier}, {start, &zero}} {
		_, err := DisplayActivity(period, true, start)
		require.ErrorIs(t, err, ErrInvalidWindow)
	}
	_, err := DisplayActivity(Period{Start: start}, true, zero)
	require.ErrorIs(t, err, ErrInvalidWindow)
	_, err = (EligibilityWindow{Period: Period{Start: start}, Pauses: []Period{{start, &earlier}}}).EligibleAt(start)
	require.ErrorIs(t, err, ErrInvalidWindow)
	openPause := EligibilityWindow{Period: Period{Start: start}, Pauses: []Period{{Start: start}}}
	eligible, err := openPause.EligibleAt(start.AddDate(1, 0, 0))
	require.NoError(t, err)
	assert.False(t, eligible)
}
