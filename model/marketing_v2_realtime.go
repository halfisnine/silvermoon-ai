package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
)

// A connection owns the cumulative price; a round owns qualification and its
// unpaid charge. Reply identity never doubles as permission to resend a reply.
type MarketingRealtimeRound struct {
	SupersededBy        int64              `gorm:"not null;index"`
	ResponseOrder       int64              `gorm:"not null;index"`
	AudioOutputObserved bool               `gorm:"not null"`
	OutputObserved      bool               `gorm:"not null"`
	ID                  int64              `gorm:"primaryKey"`
	FundingID           int64              `gorm:"not null;index"`
	ReferenceKey        string             `gorm:"size:64;not null;uniqueIndex"`
	PayloadHash         string             `gorm:"size:64;not null"`
	ResponseKey         string             `gorm:"size:64;not null;index"`
	QualifiedAt         int64              `gorm:"not null"`
	Evidence            string             `gorm:"type:text;not null"`
	Observed            bool               `gorm:"not null"`
	Terminal            bool               `gorm:"not null"`
	Unknown             bool               `gorm:"not null"`
	UsageJSON           string             `gorm:"type:text;not null"`
	PricedUsageJSON     string             `gorm:"type:text;not null"`
	Usage               *dto.RealtimeUsage `gorm:"-"`
	TargetQuota         int64              `gorm:"not null"`
}

func (MarketingRealtimeRound) TableName() string { return "marketing_v2_realtime_rounds" }

type MarketingRealtimeEvent struct {
	ID           int64  `gorm:"primaryKey"`
	FundingID    int64  `gorm:"not null;index"`
	RoundID      int64  `gorm:"not null"`
	ReferenceKey string `gorm:"size:64;not null;uniqueIndex"`
	PayloadHash  string `gorm:"size:64;not null"`
	Evidence     string `gorm:"type:text;not null"`
	Outcome      string `gorm:"type:text;not null"`
}

func (MarketingRealtimeEvent) TableName() string { return "marketing_v2_realtime_events" }

type MarketingRealtimeSnapshot struct {
	Funding *MarketingFunding
	Rounds  []MarketingRealtimeRound
}
type MarketingRealtimeResult struct {
	Funding *MarketingFunding
	Round   *MarketingRealtimeRound
	Created bool
	Stop    bool
}
type MarketingRealtimeRoundInput struct {
	PendingCommitAdmission bool
	ResponseReference      string
	MetadataFingerprint    string
	// Inputs not reliably attributed to this response keep their own admission
	// hold and unknown-on-disconnect marker. A response cannot consume a newer buffer.
	InputRemainingQuota   int64
	InputRemainingPending bool
	UserID                int
	FundingID             int64
	Reference             string
	QualifiedAt           int64
	ReserveQuota          int64
	Observed              bool
	Evidence              string
}
type MarketingRealtimeFactInput struct {
	AudioOutputObserved bool
	OutputObserved      bool
	UserID              int
	FundingID           int64
	RoundID             int64
	EventReference      string
	ResponseReference   string
	ExpectedRevision    int64
	Usage               *dto.RealtimeUsage
	CumulativeQuota     *int64
	Terminal            bool
	Evidence            string
}

func InitMarketingRealtimeFunding(tx *gorm.DB, userID int, fundingID int64) (*MarketingFunding, error) {
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if funding.Realtime {
		return funding, nil
	}
	if err = marketingRequestCanDispatch(tx, funding); err != nil {
		return nil, err
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	if err = book.releaseHolds(funding, funding.ReservedQuota, -1); err != nil {
		return nil, err
	}
	funding.Realtime = true
	funding.State = "active"
	funding.Revision++
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	return funding, nil
}

func GetMarketingRealtimeSnapshot(tx *gorm.DB, userID int, fundingID int64) (*MarketingRealtimeSnapshot, error) {
	_, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime {
		return nil, ErrMarketingConflict
	}
	rounds, err := loadMarketingRealtimeRounds(tx, fundingID)
	if err != nil {
		return nil, err
	}
	return &MarketingRealtimeSnapshot{Funding: funding, Rounds: rounds}, nil
}

func loadMarketingRealtimeRounds(tx *gorm.DB, fundingID int64) ([]MarketingRealtimeRound, error) {
	var rounds []MarketingRealtimeRound
	if err := lockForUpdate(tx).Where("funding_id = ? AND superseded_by = 0", fundingID).Order("response_order, id").Find(&rounds).Error; err != nil {
		return nil, err
	}
	for i := range rounds {
		if rounds[i].UsageJSON != "" {
			rounds[i].Usage = &dto.RealtimeUsage{}
			if err := common.UnmarshalJsonStr(rounds[i].UsageJSON, rounds[i].Usage); err != nil {
				return nil, err
			}
		}
	}
	return rounds, nil
}

// All returned Stop outcomes must COMMIT. Only actual database/contract errors
// are returned as errors; an already observed upstream reply cannot be erased
// by insufficient funds or by account revocation.
func BeginMarketingRealtimeRound(tx *gorm.DB, input MarketingRealtimeRoundInput) (*MarketingRealtimeResult, error) {
	if (input.PendingCommitAdmission && (input.Observed || !input.InputRemainingPending)) || input.Observed != (strings.TrimSpace(input.ResponseReference) != "") || len(input.ResponseReference) > 512 {
		return nil, ErrMarketingInput
	}
	if input.MetadataFingerprint != "" {
		var proof struct {
			Kind        string `json:"kind"`
			Fingerprint string `json:"metadata_fingerprint"`
		}
		if len(input.MetadataFingerprint) != 64 || common.UnmarshalJsonStr(input.Evidence, &proof) != nil || proof.Kind != "manual_metadata" || proof.Fingerprint != input.MetadataFingerprint {
			return nil, ErrMarketingInput
		}
	}
	if input.InputRemainingQuota < 0 || input.InputRemainingQuota > common.MaxWalletQuota || (!input.InputRemainingPending && input.InputRemainingQuota != 0) {
		return nil, ErrMarketingInput
	}
	if input.QualifiedAt <= 0 || input.QualifiedAt > common.GetTimestamp() || input.ReserveQuota < 0 || input.ReserveQuota > common.MaxWalletQuota || strings.TrimSpace(input.Evidence) == "" || len(input.Evidence) > 65536 {
		return nil, ErrMarketingInput
	}
	key, err := marketingDigest([]any{"realtime_round", input.FundingID, input.Reference})
	if err != nil || strings.TrimSpace(input.Reference) == "" || len(input.Reference) > 512 {
		return nil, ErrMarketingInput
	}
	hash, err := marketingDigest(input)
	if err != nil {
		return nil, err
	}
	book, funding, err := lockMarketingRequest(tx, input.UserID, input.FundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime || funding.State == "settled" || funding.RealtimeClosed {
		return nil, ErrMarketingConflict
	}
	var prior MarketingRealtimeRound
	err = lockForUpdate(tx).Where("reference_key = ?", key).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != hash {
			return nil, ErrMarketingConflict
		}
		return &MarketingRealtimeResult{Funding: funding, Round: &prior, Stop: funding.State == "review"}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if input.MetadataFingerprint != "" {
		candidates, err := loadMarketingRealtimeMetadataCandidates(tx, funding.ID, input.MetadataFingerprint)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if candidate.ResponseKey == "" || !candidate.Terminal || candidate.Unknown || candidate.UsageJSON == "" {
				return nil, ErrMarketingConflict
			}
		}
	}

	stopped := funding.State == "review"
	if err = book.authorizeRequest(); err != nil {
		if !input.Observed {
			return nil, err
		}
		stopped = true
	}
	var unresolved []MarketingFunding
	if err = lockForUpdate(tx).Where("user_id = ? AND state = ?", input.UserID, "review").Limit(1).Find(&unresolved).Error; err != nil {
		return nil, err
	}
	stopped = stopped || len(unresolved) > 0
	if stopped && !input.Observed {
		return &MarketingRealtimeResult{Funding: funding, Stop: true}, nil
	}
	// A rejected client start has sent no business frame. Its allocation attempt
	// must leave previously forwarded input reservations exactly as they were.
	const admissionSavepoint = "mv2_rt_admission"
	if err = tx.SavePoint(admissionSavepoint).Error; err != nil {
		return nil, err
	}
	round := MarketingRealtimeRound{FundingID: funding.ID, ReferenceKey: key, PayloadHash: hash, QualifiedAt: input.QualifiedAt, Evidence: input.Evidence, Observed: input.Observed}
	if input.Observed {
		// Save the real upstream identity with the observed round itself. A crash
		// before any later start/output/terminal fact must not make this reply
		// unrecognizable to a different instance performing reconciliation.
		round.ResponseKey, err = marketingDigest([]any{"realtime_reply", funding.ID, input.ResponseReference})
		if err != nil {
			return nil, err
		}
		var existing MarketingRealtimeRound
		if err = lockForUpdate(tx).Where("funding_id = ? AND response_key = ?", funding.ID, round.ResponseKey).Limit(1).Find(&existing).Error; err != nil {
			return nil, err
		}
		if existing.ID != 0 {
			return nil, ErrMarketingConflict
		}
		var last MarketingRealtimeRound
		if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Order("response_order DESC").Limit(1).Find(&last).Error; err != nil {
			return nil, err
		}
		if last.ResponseOrder == math.MaxInt64 {
			return nil, ErrMarketingInput
		}
		round.ResponseOrder = last.ResponseOrder + 1
	}
	if err = tx.Create(&round).Error; err != nil {
		return nil, err
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	book.roundID = 0
	if err = book.releaseHolds(funding, book.realtimeRoundReserved(), -1); err != nil {
		return nil, err
	}
	book.qualifiedAt = common.GetTimestamp()
	enoughInput, reserveErr := book.reserveRealtimeRound(funding, input.InputRemainingQuota)
	if reserveErr != nil {
		return nil, reserveErr
	}
	stopped = stopped || !enoughInput
	funding.RealtimeInputPending = input.InputRemainingPending
	book.roundID, book.qualifiedAt = round.ID, round.QualifiedAt
	if !stopped {
		enough, reserveErr := book.reserveRealtimeRound(funding, input.ReserveQuota)
		if reserveErr != nil {
			return nil, reserveErr
		}
		stopped = !enough
	}
	if !stopped {
		// Zero estimated usage is not proof of a free response. The normal API
		// token gate rejects an exhausted finite token even for free models;
		// long-lived connections must recheck that rule at each generation.
		if book.hasToken && !book.token.UnlimitedQuota && book.token.RemainQuota <= 0 {
			stopped = true
		}
		var frozen struct {
			Price struct{ FreeModel bool } `json:"price"`
		}
		// Only the stored server pricing snapshot can establish a free model.
		// Opaque or malformed evidence confers no free-model exemption.
		freeModel := common.UnmarshalJsonStr(funding.PricingEvidence, &frozen) == nil && frozen.Price.FreeModel
		if !freeModel {
			held := book.realtimeRoundReserved()
			available := int64(book.user.Quota) > book.account.PaidReserved
			eligible := map[int64]bool{0: true}
			for _, gift := range book.gifts {
				if gift.GrantedAt <= round.QualifiedAt && (gift.ExpiresAt == 0 || gift.ExpiresAt > round.QualifiedAt) {
					eligible[gift.ID] = true
					available = available || gift.RemainingQuota > gift.ReservedQuota
				}
			}
			// A manual create may follow a commit already sent to the provider,
			// before its ACK transfers that input to the round. Keep its eligible
			// input hold as admission backing only; this neither spends nor binds
			// the input. An automatic reply may not borrow unrelated input holds.
			if input.PendingCommitAdmission {
				for _, allocation := range book.allocations {
					if allocation.RoundID == 0 && eligible[allocation.GiftID] {
						held += allocation.ReservedQuota
					}
				}
			}
			if book.hasToken && !book.token.UnlimitedQuota && int64(book.token.RemainQuota)-book.tokenHold.ReservedQuota+held <= 0 {
				stopped = true
			}
			stopped = stopped || (!available && held == 0)
		}
	}

	if stopped && !input.Observed {
		if err = tx.RollbackTo(admissionSavepoint).Error; err != nil {
			return nil, err
		}
		_, original, reloadErr := lockMarketingRequest(tx, input.UserID, input.FundingID)
		if reloadErr != nil {
			return nil, reloadErr
		}
		return &MarketingRealtimeResult{Funding: original, Stop: true}, nil
	}
	if stopped && input.Observed {
		funding.State = "review"
		funding.ReviewReason = "realtime_observed_without_funds"
	}
	if err = advanceMarketingRealtimeRevision(funding); err != nil {
		return nil, err
	}
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	result := &MarketingRealtimeResult{Funding: funding, Stop: stopped, Created: round.ID != 0}
	if round.ID != 0 {
		result.Round = &round
	}
	return result, nil
}

func advanceMarketingRealtimeRevision(funding *MarketingFunding) error {
	if funding.Revision == math.MaxInt64 {
		return ErrMarketingInput
	}
	funding.Revision++
	return nil
}

func (book *fundingBook) realtimeRoundReserved() int64 {
	var held int64
	for _, a := range book.allocations {
		if a.RoundID == book.roundID {
			held += a.ReservedQuota
		}
	}
	return held
}

func (book *fundingBook) reserveRealtimeRound(funding *MarketingFunding, target int64) (bool, error) {
	held := book.realtimeRoundReserved()
	if target <= held {
		return true, nil
	}
	needed := target - held
	tokenAvailable := common.MaxWalletQuota - funding.ReservedQuota
	if book.hasToken {
		tokenAvailable = min(tokenAvailable, common.MaxWalletQuota-book.tokenHold.ReservedQuota)
		if !book.token.UnlimitedQuota {
			tokenAvailable = min(tokenAvailable, int64(book.token.RemainQuota)-book.tokenHold.ReservedQuota)
		}
	}
	remaining := min(needed, max(int64(0), tokenAvailable))
	for i := 0; i <= len(book.gifts) && remaining > 0; i++ {
		var gift *MarketingGift
		var giftID int64
		available := int64(book.user.Quota) - book.account.PaidReserved
		if i < len(book.gifts) {
			gift = &book.gifts[i]
			if gift.GrantedAt > book.qualifiedAt || (gift.ExpiresAt != 0 && gift.ExpiresAt <= book.qualifiedAt) {
				continue
			}
			giftID = gift.ID
			available = gift.RemainingQuota - gift.ReservedQuota
		}
		take := min(remaining, available)
		if take <= 0 {
			continue
		}
		book.allocation(funding.ID, giftID).ReservedQuota += take
		if gift != nil {
			gift.ReservedQuota += take
		} else {
			book.account.PaidReserved += take
		}
		funding.ReservedQuota += take
		if book.hasToken {
			book.tokenHold.ReservedQuota += take
		}
		remaining -= take
		needed -= take
	}
	return needed == 0, nil
}

func ReserveMarketingRealtimeRound(tx *gorm.DB, userID int, fundingID, roundID, target int64) (*MarketingRealtimeResult, error) {
	if target < 0 || target > common.MaxWalletQuota {
		return nil, ErrMarketingInput
	}
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime || funding.State == "settled" || funding.RealtimeClosed {
		return nil, ErrMarketingConflict
	}
	var round MarketingRealtimeRound
	if err = lockForUpdate(tx).Where("id = ? AND funding_id = ?", roundID, fundingID).First(&round).Error; err != nil {
		return nil, err
	}
	if round.Terminal {
		return nil, ErrMarketingConflict
	}
	if funding.State == "review" {
		return &MarketingRealtimeResult{Funding: funding, Round: &round, Stop: true}, nil
	}
	if err = book.authorizeRequest(); err != nil {
		return &MarketingRealtimeResult{Funding: funding, Round: &round, Stop: true}, nil
	}
	var unresolved []MarketingFunding
	if err = lockForUpdate(tx).Where("user_id = ? AND state = ?", userID, "review").Limit(1).Find(&unresolved).Error; err != nil {
		return nil, err
	}
	if len(unresolved) > 0 {
		return &MarketingRealtimeResult{Funding: funding, Round: &round, Stop: true}, nil
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	book.roundID, book.qualifiedAt = round.ID, round.QualifiedAt
	enough, err := book.reserveRealtimeRound(funding, target)
	if err != nil {
		return nil, err
	}
	if err = advanceMarketingRealtimeRevision(funding); err != nil {
		return nil, err
	}
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	return &MarketingRealtimeResult{Funding: funding, Round: &round, Stop: !enough}, nil
}

// Actual counters are absolute per response. The protocol layer must establish
// their presence/completeness; the ledger rejects decreasing or unsafe facts.
func validMarketingRealtimeUsage(previous, current *dto.RealtimeUsage) bool {
	if current == nil {
		return true
	}
	values := []int{current.TotalTokens, current.InputTokens, current.OutputTokens, current.InputTokenDetails.TextTokens, current.InputTokenDetails.AudioTokens, current.InputTokenDetails.CachedTokens, current.OutputTokenDetails.TextTokens, current.OutputTokenDetails.AudioTokens, current.InputTokenDetails.ImageTokens, current.OutputTokenDetails.ImageTokens}
	before := make([]int, len(values))
	if previous != nil {
		before = []int{previous.TotalTokens, previous.InputTokens, previous.OutputTokens, previous.InputTokenDetails.TextTokens, previous.InputTokenDetails.AudioTokens, previous.InputTokenDetails.CachedTokens, previous.OutputTokenDetails.TextTokens, previous.OutputTokenDetails.AudioTokens, previous.InputTokenDetails.ImageTokens, previous.OutputTokenDetails.ImageTokens}
	}
	for i, v := range values {
		if v < 0 || int64(v) > common.MaxQuota || v < before[i] {
			return false
		}
	}
	if current.InputTokens > common.MaxQuota-current.OutputTokens || current.TotalTokens != current.InputTokens+current.OutputTokens {
		return false
	}
	input, output := current.InputTokenDetails, current.OutputTokenDetails
	if input.AudioTokens > current.InputTokens || input.TextTokens > current.InputTokens-input.AudioTokens || input.ImageTokens > current.InputTokens-input.AudioTokens-input.TextTokens || input.CachedTokens > current.InputTokens || output.AudioTokens > current.OutputTokens || output.TextTokens > current.OutputTokens-output.AudioTokens || output.ImageTokens > current.OutputTokens-output.AudioTokens-output.TextTokens {
		return false
	}
	var previousDetails *dto.CachedTokenDetails
	if previous != nil {
		previousDetails = previous.InputTokenDetails.CachedTokensDetails
	}
	if input.CachedTokensDetails == nil {
		return previousDetails == nil
	}
	details := input.CachedTokensDetails
	currentCache := []*int{details.TextTokens, details.AudioTokens, details.ImageTokens}
	previousCache := []*int{nil, nil, nil}
	if previousDetails != nil {
		previousCache = []*int{previousDetails.TextTokens, previousDetails.AudioTokens, previousDetails.ImageTokens}
	}
	remaining := input.CachedTokens
	for i, value := range currentCache {
		if value == nil {
			if previousCache[i] != nil {
				return false
			}
			continue
		}
		if *value < 0 || *value > remaining || (previousCache[i] != nil && *value < *previousCache[i]) {
			return false
		}
		remaining -= *value
	}
	return true
}

func ApplyMarketingRealtimeFact(tx *gorm.DB, input MarketingRealtimeFactInput) (*MarketingRealtimeResult, error) {
	if strings.TrimSpace(input.Evidence) == "" || len(input.Evidence) > 65536 || strings.TrimSpace(input.EventReference) == "" || len(input.EventReference) > 512 || len(input.ResponseReference) > 512 {
		return nil, ErrMarketingInput
	}
	if input.CumulativeQuota != nil && (*input.CumulativeQuota < 0 || *input.CumulativeQuota > common.MaxWalletQuota) {
		return nil, ErrMarketingInput
	}
	key, err := marketingDigest([]any{"realtime_fact", input.FundingID, input.EventReference})
	if err != nil {
		return nil, err
	}
	// A transaction revision is a concurrency guard, not part of event identity.
	hashInput := input
	hashInput.ExpectedRevision = 0
	hashInput.CumulativeQuota = nil
	hash, err := marketingDigest(hashInput)
	if err != nil {
		return nil, err
	}
	book, funding, err := lockMarketingRequest(tx, input.UserID, input.FundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime {
		return nil, ErrMarketingConflict
	}
	var event MarketingRealtimeEvent
	err = lockForUpdate(tx).Where("reference_key = ?", key).First(&event).Error
	if err == nil {
		if event.PayloadHash != hash {
			return nil, ErrMarketingConflict
		}
		var result MarketingRealtimeResult
		if err = common.UnmarshalJsonStr(event.Outcome, &result); err != nil {
			return nil, err
		}
		// Replaying an old event must never re-enable a connection that a newer
		// event stopped. Financial idempotency and current admission differ.
		if result.Round != nil {
			var current MarketingRealtimeRound
			if err = lockForUpdate(tx).First(&current, result.Round.ID).Error; err != nil {
				return nil, err
			}
			if current.SupersededBy != 0 {
				canonicalID := current.SupersededBy
				current = MarketingRealtimeRound{}
				if err = lockForUpdate(tx).First(&current, canonicalID).Error; err != nil {
					return nil, err
				}
			}
			result.Round = &current
		}
		result.Funding = funding
		result.Created = false
		result.Stop = funding.State == "review" || funding.RealtimeClosed
		return &result, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if funding.State == "settled" || funding.Revision != input.ExpectedRevision {
		return nil, ErrMarketingConflict
	}
	rounds, err := loadMarketingRealtimeRounds(tx, funding.ID)
	if err != nil {
		return nil, err
	}
	var round *MarketingRealtimeRound
	for i := range rounds {
		if rounds[i].ID == input.RoundID {
			round = &rounds[i]
			break
		}
	}
	if round == nil {
		return nil, ErrMarketingAccess
	}
	round.AudioOutputObserved = round.AudioOutputObserved || input.AudioOutputObserved
	round.OutputObserved = round.OutputObserved || input.OutputObserved || round.AudioOutputObserved
	contradictoryOutput := round.OutputObserved && ((input.Usage != nil && input.Usage.OutputTokens == 0) || (input.OutputObserved && input.Usage == nil && round.Terminal && round.Usage != nil && round.Usage.OutputTokens == 0))
	contradictoryAudio := round.AudioOutputObserved && ((input.Usage != nil && input.Usage.OutputTokenDetails.AudioTokens == 0) || (input.AudioOutputObserved && input.Usage == nil && round.Terminal && round.Usage != nil && round.Usage.OutputTokenDetails.AudioTokens == 0))
	contradictoryOutput = contradictoryOutput || contradictoryAudio
	if !contradictoryOutput && !validMarketingRealtimeUsage(round.Usage, input.Usage) {
		return nil, ErrMarketingInput
	}
	safeEvidence := input.Evidence
	if contradictoryOutput {
		// Preserve the contradictory numeric report as evidence, not as a zero
		// settlement. The original safe protocol evidence remains in its hash.
		reason := "output_observed_with_zero_reported_output"
		if contradictoryAudio {
			reason = "audio_observed_with_zero_reported_audio_output"
		}
		encoded, encodeErr := common.Marshal(map[string]any{"reason": reason, "reported_usage": input.Usage, "output_observed": true, "audio_output_observed": round.AudioOutputObserved, "protocol_evidence_hash": hash})
		if encodeErr != nil {
			return nil, encodeErr
		}
		safeEvidence = string(encoded)
	}
	if input.ResponseReference != "" {
		responseKey, keyErr := marketingDigest([]any{"realtime_reply", funding.ID, input.ResponseReference})
		if keyErr != nil {
			return nil, keyErr
		}
		if round.ResponseKey != "" && round.ResponseKey != responseKey {
			return nil, ErrMarketingConflict
		}
		for _, r := range rounds {
			if r.ID != round.ID && r.ResponseKey == responseKey {
				return nil, ErrMarketingConflict
			}
		}
		if round.ResponseOrder == 0 {
			var last MarketingRealtimeRound
			err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Order("response_order DESC").First(&last).Error
			if err != nil {
				return nil, err
			}
			if last.ResponseOrder == math.MaxInt64 {
				return nil, ErrMarketingInput
			}
			round.ResponseOrder = last.ResponseOrder + 1
		}
		round.ResponseKey = responseKey
	}
	if !contradictoryOutput && round.Terminal && !round.Unknown && round.PricedUsageJSON == round.UsageJSON && funding.ActualQuota == funding.SettledQuota {
		if !input.Terminal || input.Usage == nil {
			return nil, ErrMarketingConflict
		}
		encoded, encodeErr := common.Marshal(input.Usage)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if string(encoded) != round.UsageJSON {
			return nil, ErrMarketingConflict
		}
		// A repeated terminal response with another transport event id is still
		// the same supplier fact, not another billable response.
		return &MarketingRealtimeResult{Funding: funding, Round: round, Stop: funding.State == "review"}, nil
	}
	if input.Usage != nil && !contradictoryOutput {
		encoded, encodeErr := common.Marshal(input.Usage)
		if encodeErr != nil {
			return nil, encodeErr
		}
		round.UsageJSON = string(encoded)
		round.Usage = input.Usage
	}
	round.Terminal = round.Terminal || input.Terminal
	if input.Terminal {
		round.Unknown = input.Usage == nil || contradictoryOutput
	}
	if contradictoryOutput {
		round.Unknown = true
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	if err = advanceMarketingRealtimeRevision(funding); err != nil {
		return nil, err
	}
	unknown := false
	for _, r := range rounds {
		unknown = unknown || r.Unknown
	}
	reversalPending := false
	if input.CumulativeQuota != nil && !contradictoryOutput {
		if unknown {
			return nil, ErrMarketingConflict
		}
		target := *input.CumulativeQuota
		var assigned int64
		originalTargets := make([]int64, len(rounds))
		for i, r := range rounds {
			if r.TargetQuota < 0 || r.TargetQuota > common.MaxWalletQuota-assigned {
				return nil, ErrMarketingReview
			}
			assigned += r.TargetQuota
			originalTargets[i] = r.TargetQuota
		}
		delta := target - assigned
		if delta > 0 {
			if round.TargetQuota > common.MaxWalletQuota-delta {
				return nil, ErrMarketingInput
			}
			round.TargetQuota += delta
		} else if delta < 0 {
			reduction := -delta
			// Erase unpaid receivables first. Only money actually debited can be
			// returned to a wallet, and then strictly in actual debit LIFO order.
			for i := len(rounds) - 1; i >= 0 && reduction > 0; i-- {
				var paid int64
				for _, a := range book.allocations {
					if a.RoundID == rounds[i].ID {
						paid += a.ConsumedQuota
					}
				}
				take := min(reduction, max(int64(0), rounds[i].TargetQuota-paid))
				rounds[i].TargetQuota -= take
				reduction -= take
			}
			if reduction > 0 {
				before := map[int64]int64{}
				for _, a := range book.allocations {
					before[a.RoundID] += a.ConsumedQuota
				}
				savepoint := fmt.Sprintf("mv2_rt_reverse_%d_%d", funding.ID, funding.Revision)
				if err = tx.SavePoint(savepoint).Error; err != nil {
					return nil, err
				}
				oldSettled, oldReserved := funding.SettledQuota, funding.ReservedQuota
				err = book.reverse(tx, funding, reduction)
				if errors.Is(err, ErrMarketingInput) || errors.Is(err, ErrMarketingReview) {
					if rollbackErr := tx.RollbackTo(savepoint).Error; rollbackErr != nil {
						return nil, rollbackErr
					}
					book, err = loadMarketingFundingBook(tx, input.UserID, funding.TokenID)
					if err != nil {
						return nil, err
					}
					if err = lockForUpdate(tx).Where("funding_id = ?", funding.ID).Find(&book.allocations).Error; err != nil {
						return nil, err
					}
					funding.SettledQuota, funding.ReservedQuota = oldSettled, oldReserved
					for i := range rounds {
						rounds[i].TargetQuota = originalTargets[i]
					}
					reversalPending = true
				} else if err != nil {
					return nil, err
				}
				for _, a := range book.allocations {
					before[a.RoundID] -= a.ConsumedQuota
				}
				for i := range rounds {
					if reversalPending {
						break
					}
					rounds[i].TargetQuota -= before[rounds[i].ID]
					if rounds[i].TargetQuota < 0 {
						return nil, ErrMarketingReview
					}
				}
			}
		}
		funding.ActualQuota = target
		round.PricedUsageJSON = round.UsageJSON
		// Reconcile old receivables with their ORIGINAL qualification. Never let
		// the current round's new gift eligibility pay an older round's deficit.
		for i := range rounds {
			if reversalPending {
				break
			}
			var charged int64
			for _, a := range book.allocations {
				if a.RoundID == rounds[i].ID {
					charged += a.ConsumedQuota
				}
			}
			book.roundID, book.qualifiedAt = rounds[i].ID, rounds[i].QualifiedAt
			if rounds[i].TargetQuota > charged {
				if err = book.consume(tx, funding, rounds[i].TargetQuota-charged); err != nil {
					return nil, err
				}
			}
		}
	}
	// A completed, fully funded reply no longer needs its unused admission
	// estimate. Release only this round: unrelated input and other rounds,
	// including their original-source reversal holds, remain untouched.
	if round.Terminal && !round.Unknown && round.UsageJSON != "" && round.UsageJSON == round.PricedUsageJSON && !reversalPending {
		var charged int64
		for _, allocation := range book.allocations {
			if allocation.RoundID == round.ID {
				charged += allocation.ConsumedQuota
			}
		}
		if charged == round.TargetQuota {
			book.roundID = round.ID
			if err = book.releaseHolds(funding, book.realtimeRoundReserved(), -1); err != nil {
				return nil, err
			}
		}
	}

	unpriced := false
	for _, r := range rounds {
		unpriced = unpriced || (r.Terminal && r.UsageJSON != r.PricedUsageJSON)
	}
	previousReason := funding.ReviewReason
	previousReview := funding.State == "review"
	// A late terminal can resolve its own generation while independent input
	// on the closed socket is still unresolved. That input must keep the account
	// blocked, but it is not an excuse to discard or defer this known charge.
	closedInputUnknown := funding.RealtimeClosed && funding.RealtimeInputPending
	funding.CostKnown = !unknown && !unpriced && !closedInputUnknown
	funding.State = "active"
	funding.ReviewReason = ""
	if unknown || closedInputUnknown {
		funding.State = "review"
		funding.ReviewReason = "unknown_realtime_round"
	} else if unpriced {
		funding.State = "review"
		funding.ReviewReason = "unpriced_realtime_usage"
	} else if input.CumulativeQuota == nil && previousReview {
		funding.State = "review"
		funding.ReviewReason = previousReason
	} else if funding.SettledQuota != funding.ActualQuota {
		funding.State = "review"
		funding.ReviewReason = "insufficient_funds_or_token_limit"
		if reversalPending {
			funding.ReviewReason = "reversal_requires_reconciliation"
		}
	}
	funding.UsageEvidence = safeEvidence
	for i := range rounds {
		if err = tx.Save(&rounds[i]).Error; err != nil {
			return nil, err
		}
	}
	if funding.RealtimeClosed && funding.State != "review" {
		allFinal := !funding.RealtimeInputPending
		for _, r := range rounds {
			allFinal = allFinal && r.Terminal && !r.Unknown && r.Usage != nil
		}
		if allFinal {
			if err = book.releaseRealtimeHolds(funding); err != nil {
				return nil, err
			}
			funding.State = "settled"
			funding.FinalObserved = true
		}
	}
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	result := &MarketingRealtimeResult{Funding: funding, Round: round, Created: true, Stop: funding.State == "review" || funding.RealtimeClosed}
	outcome, err := common.Marshal(result)
	if err != nil {
		return nil, err
	}
	event = MarketingRealtimeEvent{FundingID: funding.ID, RoundID: round.ID, ReferenceKey: key, PayloadHash: hash, Evidence: safeEvidence, Outcome: string(outcome)}
	if err = tx.Create(&event).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (book *fundingBook) releaseRealtimeHolds(funding *MarketingFunding) error {
	rounds := map[int64]bool{}
	for _, a := range book.allocations {
		rounds[a.RoundID] = true
	}
	for id := range rounds {
		book.roundID = id
		if err := book.releaseHolds(funding, book.realtimeRoundReserved(), -1); err != nil {
			return err
		}
	}
	return nil
}

func CloseMarketingRealtimeFunding(tx *gorm.DB, userID int, fundingID int64, evidence string) (*MarketingRealtimeResult, error) {
	if strings.TrimSpace(evidence) == "" || len(evidence) > 65536 {
		return nil, ErrMarketingInput
	}
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime {
		return nil, ErrMarketingConflict
	}
	if funding.RealtimeClosed {
		return &MarketingRealtimeResult{Funding: funding, Stop: funding.State != "settled"}, nil
	}
	rounds, err := loadMarketingRealtimeRounds(tx, fundingID)
	if err != nil {
		return nil, err
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	book.roundID = 0
	unknown := funding.RealtimeInputPending || book.realtimeRoundReserved() > 0
	for i := range rounds {
		if !rounds[i].Terminal || rounds[i].Usage == nil || rounds[i].Unknown || rounds[i].UsageJSON != rounds[i].PricedUsageJSON {
			rounds[i].Unknown = true
			unknown = true
			if err = tx.Save(&rounds[i]).Error; err != nil {
				return nil, err
			}
		}
	}
	funding.RealtimeClosed = true
	funding.FinalObserved = true
	funding.UsageEvidence = evidence
	funding.CostKnown = !unknown
	if unknown {
		funding.State = "review"
		funding.ReviewReason = "unknown_realtime_round"
	} else if funding.ActualQuota != funding.SettledQuota {
		funding.State = "review"
		funding.ReviewReason = "insufficient_funds_or_token_limit"
	} else {
		if err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&book.allocations).Error; err != nil {
			return nil, err
		}
		if err = book.releaseRealtimeHolds(funding); err != nil {
			return nil, err
		}
		funding.State = "settled"
		funding.ReviewReason = ""
	}
	if err = advanceMarketingRealtimeRevision(funding); err != nil {
		return nil, err
	}
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	return &MarketingRealtimeResult{Funding: funding, Stop: funding.State == "review"}, nil
}

// Input is provisional admission, never a response start or actual consumption.
// Rechecking current time prevents an idle buffer from retaining expired gifts.
func ReserveMarketingRealtimeInput(tx *gorm.DB, userID int, fundingID, target, qualifiedAt int64, pending ...bool) (*MarketingRealtimeResult, error) {
	if target < 0 || target > common.MaxWalletQuota || qualifiedAt <= 0 || qualifiedAt > common.GetTimestamp() {
		return nil, ErrMarketingInput
	}
	hasInput := len(pending) == 0 || pending[0]
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime || funding.RealtimeClosed || funding.State == "settled" {
		return nil, ErrMarketingConflict
	}
	if hasInput {
		if err = book.authorizeRequest(); err != nil {
			return &MarketingRealtimeResult{Funding: funding, Stop: true}, nil
		}
		var unresolved []MarketingFunding
		if err = lockForUpdate(tx).Where("user_id = ? AND state = ?", userID, "review").Limit(1).Find(&unresolved).Error; err != nil {
			return nil, err
		}
		if len(unresolved) > 0 {
			return &MarketingRealtimeResult{Funding: funding, Stop: true}, nil
		}
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	book.roundID, book.qualifiedAt = 0, qualifiedAt
	if err = book.releaseHolds(funding, book.realtimeRoundReserved(), -1); err != nil {
		return nil, err
	}
	enough, err := book.reserveRealtimeRound(funding, target)
	if err != nil {
		return nil, err
	}
	funding.RealtimeInputPending = hasInput
	if err = advanceMarketingRealtimeRevision(funding); err != nil {
		return nil, err
	}
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	return &MarketingRealtimeResult{Funding: funding, Stop: !enough}, nil
}

// AttachMarketingRealtimeInput is called only for a trusted provider acknowledgement
// linking previously forwarded input to an existing pending response. Qualification
// stays at that response's original start; newer unassociated input stays separate.
// Stop is a committed admission outcome, never an instruction to roll back the ack.
func AttachMarketingRealtimeInput(tx *gorm.DB, userID int, fundingID, roundID, roundTarget, remainingInputQuota int64, remainingPending bool) (*MarketingRealtimeResult, error) {
	if roundTarget < 0 || roundTarget > common.MaxWalletQuota || remainingInputQuota < 0 || remainingInputQuota > common.MaxWalletQuota || (!remainingPending && remainingInputQuota != 0) {
		return nil, ErrMarketingInput
	}
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime || funding.RealtimeClosed || funding.State == "settled" {
		return nil, ErrMarketingConflict
	}
	var round MarketingRealtimeRound
	if err = lockForUpdate(tx).Where("id = ? AND funding_id = ?", roundID, fundingID).First(&round).Error; err != nil {
		return nil, err
	}
	if round.Terminal {
		return nil, ErrMarketingConflict
	}
	stopped := funding.State == "review"
	if err = book.authorizeRequest(); err != nil {
		stopped = true
	}
	var unresolved []MarketingFunding
	if err = lockForUpdate(tx).Where("user_id = ? AND state = ?", userID, "review").Limit(1).Find(&unresolved).Error; err != nil {
		return nil, err
	}
	stopped = stopped || len(unresolved) > 0
	if err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	book.roundID, book.qualifiedAt = 0, common.GetTimestamp()
	if err = book.releaseHolds(funding, book.realtimeRoundReserved(), -1); err != nil {
		return nil, err
	}
	enoughInput, err := book.reserveRealtimeRound(funding, remainingInputQuota)
	if err != nil {
		return nil, err
	}
	funding.RealtimeInputPending = remainingPending
	book.roundID, book.qualifiedAt = round.ID, round.QualifiedAt
	enoughRound, err := book.reserveRealtimeRound(funding, roundTarget)
	if err != nil {
		return nil, err
	}
	if err = advanceMarketingRealtimeRevision(funding); err != nil {
		return nil, err
	}
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	return &MarketingRealtimeResult{Funding: funding, Round: &round, Stop: stopped || !enoughInput || !enoughRound}, nil
}

// CorrelateMarketingRealtimeResponse applies an exact provider metadata echo.
// The observed reply remains the canonical row and retains its generation order.
// Only an unpriced observation may inherit a proven client start qualification.
// The old observation time and both identities remain in a durable event.
func CorrelateMarketingRealtimeResponse(tx *gorm.DB, userID int, fundingID, manualID, observedID, target int64, evidence string) (*MarketingRealtimeResult, error) {
	if manualID == observedID || target < 0 || target > common.MaxWalletQuota || strings.TrimSpace(evidence) == "" || len(evidence) > 8192 {
		return nil, ErrMarketingInput
	}
	book, funding, err := lockMarketingRequest(tx, userID, fundingID)
	if err != nil {
		return nil, err
	}
	if !funding.Realtime {
		return nil, ErrMarketingConflict
	}
	var manual, observed MarketingRealtimeRound
	if err = lockForUpdate(tx).Where("id = ? AND funding_id = ?", manualID, fundingID).First(&manual).Error; err != nil {
		return nil, err
	}
	if err = lockForUpdate(tx).Where("id = ? AND funding_id = ?", observedID, fundingID).First(&observed).Error; err != nil {
		return nil, err
	}
	if manual.SupersededBy == observed.ID {
		return &MarketingRealtimeResult{Funding: funding, Round: &observed, Stop: funding.State == "review"}, nil
	}
	if funding.State == "settled" || manual.Observed || manual.SupersededBy != 0 || observed.SupersededBy != 0 || !observed.Observed || manual.ResponseOrder != 0 || manual.ResponseKey != "" || observed.ResponseKey == "" || observed.ResponseOrder == 0 || manual.UsageJSON != "" || observed.UsageJSON != "" || manual.PricedUsageJSON != "" || observed.PricedUsageJSON != "" || manual.TargetQuota != 0 || observed.TargetQuota != 0 {
		return nil, ErrMarketingConflict
	}
	if err = lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&book.allocations).Error; err != nil {
		return nil, err
	}
	for _, a := range book.allocations {
		if (a.RoundID == manualID || a.RoundID == observedID) && a.ConsumedQuota != 0 {
			return nil, ErrMarketingConflict
		}
	}
	oldQualifiedAt := observed.QualifiedAt
	book.roundID = manualID
	manualHeld := book.realtimeRoundReserved()
	if err = book.releaseHolds(funding, manualHeld, -1); err != nil {
		return nil, err
	}
	book.roundID = observedID
	observedHeld := book.realtimeRoundReserved()
	if err = book.releaseHolds(funding, observedHeld, -1); err != nil {
		return nil, err
	}
	observed.QualifiedAt = manual.QualifiedAt
	observed.OutputObserved = observed.OutputObserved || manual.OutputObserved
	observed.AudioOutputObserved = observed.AudioOutputObserved || manual.AudioOutputObserved
	observed.Unknown = observed.Unknown || manual.Unknown
	observed.Terminal = observed.Terminal || manual.Terminal
	manual.SupersededBy = observedID
	book.qualifiedAt = observed.QualifiedAt
	enough, err := book.reserveRealtimeRound(funding, max(target, manualHeld, observedHeld))
	if err != nil {
		return nil, err
	}
	if !enough {
		funding.State = "review"
		funding.ReviewReason = "realtime_correlated_without_funds"
	}
	if err = advanceMarketingRealtimeRevision(funding); err != nil {
		return nil, err
	}
	if err = tx.Save(&manual).Error; err != nil {
		return nil, err
	}
	if err = tx.Save(&observed).Error; err != nil {
		return nil, err
	}
	if err = book.save(tx, funding); err != nil {
		return nil, err
	}
	receipt, err := common.Marshal(map[string]any{"manual_round": manualID, "observed_round": observedID, "observed_at": oldQualifiedAt, "qualified_at": observed.QualifiedAt, "evidence": evidence})
	if err != nil {
		return nil, err
	}
	key, err := marketingDigest([]any{"realtime_correlation", fundingID, manualID, observedID})
	if err != nil {
		return nil, err
	}
	hash, err := marketingDigest(string(receipt))
	if err != nil {
		return nil, err
	}
	result := &MarketingRealtimeResult{Funding: funding, Round: &observed, Created: true, Stop: funding.State == "review"}
	outcome, err := common.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err = tx.Create(&MarketingRealtimeEvent{FundingID: fundingID, RoundID: observedID, ReferenceKey: key, PayloadHash: hash, Evidence: string(receipt), Outcome: string(outcome)}).Error; err != nil {
		return nil, err
	}
	return result, nil
}

// FindMarketingRealtimeRoundByReference resolves a server-issued correlation
// reference, including after process restart, without persisting raw metadata.
func FindMarketingRealtimeRoundByReference(tx *gorm.DB, userID int, fundingID int64, reference string) (*MarketingRealtimeRound, error) {
	if _, _, err := lockMarketingRequest(tx, userID, fundingID); err != nil {
		return nil, err
	}
	key, err := marketingDigest([]any{"realtime_round", fundingID, reference})
	if err != nil {
		return nil, err
	}
	var round MarketingRealtimeRound
	if err = lockForUpdate(tx).Where("funding_id = ? AND reference_key = ?", fundingID, key).First(&round).Error; err != nil {
		return nil, err
	}
	return &round, nil
}

// FindMarketingRealtimeRoundByResponse restores an observed provider identity;
// it never grants an unknown response an older round's qualification.
func FindMarketingRealtimeRoundByResponse(tx *gorm.DB, userID int, fundingID int64, response string) (*MarketingRealtimeRound, error) {
	if _, _, err := lockMarketingRequest(tx, userID, fundingID); err != nil {
		return nil, err
	}
	key, err := marketingDigest([]any{"realtime_reply", fundingID, response})
	if err != nil {
		return nil, err
	}
	var round MarketingRealtimeRound
	if err = lockForUpdate(tx).Where("funding_id = ? AND response_key = ? AND superseded_by = 0", fundingID, key).First(&round).Error; err != nil {
		return nil, err
	}
	if round.UsageJSON != "" {
		round.Usage = &dto.RealtimeUsage{}
		if err = common.UnmarshalJsonStr(round.UsageJSON, round.Usage); err != nil {
			return nil, err
		}
	}
	return &round, nil
}

// A metadata fingerprint identifies a manual intent only within its ambiguity
// window. Historical terminal response IDs remain authoritative after reuse.
func loadMarketingRealtimeMetadataCandidates(tx *gorm.DB, fundingID int64, fingerprint string) ([]MarketingRealtimeRound, error) {
	var rounds []MarketingRealtimeRound
	if err := lockForUpdate(tx).Where("funding_id = ?", fundingID).Find(&rounds).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]MarketingRealtimeRound, len(rounds))
	for _, round := range rounds {
		byID[round.ID] = round
	}
	var matches []MarketingRealtimeRound
	seen := map[int64]bool{}
	for _, original := range rounds {
		if original.Observed || !strings.HasPrefix(strings.TrimSpace(original.Evidence), "{") {
			continue
		}
		var proof struct {
			Kind        string `json:"kind"`
			Fingerprint string `json:"metadata_fingerprint"`
		}
		if common.UnmarshalJsonStr(original.Evidence, &proof) != nil || proof.Kind != "manual_metadata" || proof.Fingerprint != fingerprint {
			continue
		}
		round := original
		if original.SupersededBy != 0 {
			var exists bool
			round, exists = byID[original.SupersededBy]
			if !exists || round.SupersededBy != 0 {
				return nil, ErrMarketingConflict
			}
		}
		if !seen[round.ID] {
			matches = append(matches, round)
			seen[round.ID] = true
		}
	}
	return matches, nil
}

func FindMarketingRealtimeRoundByMetadata(tx *gorm.DB, userID int, fundingID int64, fingerprint, response string) (*MarketingRealtimeRound, error) {
	if len(fingerprint) != 64 || response == "" {
		return nil, ErrMarketingInput
	}
	if _, _, err := lockMarketingRequest(tx, userID, fundingID); err != nil {
		return nil, err
	}
	candidates, err := loadMarketingRealtimeMetadataCandidates(tx, fundingID, fingerprint)
	if err != nil {
		return nil, err
	}
	responseKey, err := marketingDigest([]any{"realtime_reply", fundingID, response})
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if candidate.ResponseKey == responseKey {
			return &candidate, nil
		}
	}
	var pending *MarketingRealtimeRound
	for _, candidate := range candidates {
		if candidate.ResponseKey == "" {
			if pending != nil {
				return nil, ErrMarketingConflict
			}
			pending = &candidate
		} else if !candidate.Terminal || candidate.Unknown {
			return nil, ErrMarketingConflict
		}
	}
	if pending == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return pending, nil
}
