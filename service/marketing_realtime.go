package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// MarketingRealtimeCoordinator is used by a single event loop. Socket readers
// never mutate it. The database owns funds, round identities and actual usage.
type MarketingRealtimeCoordinator struct {
	replayLog               *model.Log
	vadMode                 string
	vadAuto                 bool
	vadIdle                 bool
	vadTranscription        bool
	vadTranscriptionKnown   bool
	vadSafe                 bool
	vadSegments             map[string]*marketingRealtimeInputSegment
	totalAudioBytes         int64
	inputAudioStart         int64
	manual                  map[string]*marketingRealtimeManual
	nextMetadataFingerprint string
	nextReference           string
	nextEvidence            string
	ctx                     *gin.Context
	session                 *MarketingBillingSession
	info                    *relaycommon.RelayInfo
	sequence                int64
	pending                 int64
	pendingCommitCount      int
	responses               map[string]int64
	estimates               map[int64]dto.RealtimeUsage
	total                   dto.RealtimeUsage
	input                   dto.RealtimeUsage
	inputAudioBytes         int64
	inputPending            bool
	committed               dto.RealtimeUsage
	committedPending        bool
	awaitingCommits         []dto.RealtimeUsage
	awaitingClears          []dto.RealtimeUsage
	outputAudioBytes        map[int64]int64
	outputSeen              map[int64]bool
	audioOutputSeen         map[int64]bool
	closed                  bool
}

type marketingRealtimeInputSegment struct {
	StartMS   int64
	Stopped   bool
	Committed bool
	Usage     dto.RealtimeUsage
}

type marketingRealtimeEvent struct {
	ItemID       string `json:"item_id"`
	AudioStartMS *int64 `json:"audio_start_ms"`
	AudioEndMS   *int64 `json:"audio_end_ms"`
	Type         string `json:"type"`
	EventID      string `json:"event_id"`
	ResponseID   string `json:"response_id"`
	Delta        string `json:"delta"`
	Audio        string `json:"audio"`
	Response     *struct {
		ID       string            `json:"id"`
		Status   string            `json:"status"`
		Usage    json.RawMessage   `json:"usage"`
		Metadata map[string]string `json:"metadata"`
	} `json:"response"`
	Session *struct {
		InputAudioFormat  string `json:"input_audio_format"`
		OutputAudioFormat string `json:"output_audio_format"`
		Audio             *struct {
			Input *struct {
				Format struct {
					Type string `json:"type"`
					Rate *int   `json:"rate"`
				} `json:"format"`
			} `json:"input"`
			Output *struct {
				Format struct {
					Type string `json:"type"`
					Rate *int   `json:"rate"`
				} `json:"format"`
			} `json:"output"`
		} `json:"audio"`
	} `json:"session"`
}

func NewMarketingRealtimeCoordinator(c *gin.Context, info *relaycommon.RelayInfo) (*MarketingRealtimeCoordinator, error) {
	session, ok := info.Billing.(*MarketingBillingSession)
	if !ok {
		return nil, model.ErrMarketingInput
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		funding, err := model.InitMarketingRealtimeFunding(tx, info.UserId, session.funding.ID)
		if err == nil {
			session.funding = *funding
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &MarketingRealtimeCoordinator{ctx: c, session: session, info: info, responses: map[string]int64{}, estimates: map[int64]dto.RealtimeUsage{}, outputAudioBytes: map[int64]int64{}, outputSeen: map[int64]bool{}, audioOutputSeen: map[int64]bool{}, manual: map[string]*marketingRealtimeManual{}, vadSegments: map[string]*marketingRealtimeInputSegment{}}, nil
}

func (r *MarketingRealtimeCoordinator) Usage() *dto.RealtimeUsage {
	result := r.total
	result.InputTokenDetails = result.InputTokenDetails.Clone()
	return &result
}

func (r *MarketingRealtimeCoordinator) begin(reference string, at time.Time, observed bool, responseReference string) (int64, error) {
	r.session.mu.Lock()
	defer r.session.mu.Unlock()
	selected := dto.RealtimeUsage{}
	// This is a provisional input admission transfer, not an input-item to
	// response identity. Only a confirmed, unambiguous automatic VAD cycle may
	// retire its input hold. The reply is still qualified at receipt of created.
	selectedVAD := ""
	if observed && r.vadSafe && r.pending == 0 && !r.committedPending && len(r.awaitingCommits) == 0 && len(r.awaitingClears) == 0 && len(r.vadSegments) == 1 {
		for key, segment := range r.vadSegments {
			if segment.Stopped && segment.Committed {
				selectedVAD = key
				selected = segment.Usage
			}
		}
	}
	selectedText := !observed && r.inputAudioBytes == 0 && len(r.awaitingCommits) == 0 && len(r.awaitingClears) == 0
	if !observed {
		selected = r.committed
	}
	if selectedText {
		var err error
		selected, err = SumMarketingRealtimeUsage([]dto.RealtimeUsage{selected, r.input})
		if err != nil {
			return 0, err
		}
	}
	var remainder []dto.RealtimeUsage
	pendingRemaining := false
	if !selectedText {
		remainder = append(remainder, r.input)
		pendingRemaining = r.inputPending
	}
	if observed {
		remainder = append(remainder, r.committed)
		pendingRemaining = pendingRemaining || r.committedPending
	}
	for key, segment := range r.vadSegments {
		if key != selectedVAD && segment.Stopped {
			remainder = append(remainder, segment.Usage)
			pendingRemaining = true
		}
	}
	remainder = append(remainder, r.awaitingCommits...)
	remainder = append(remainder, r.awaitingClears...)
	pendingRemaining = pendingRemaining || len(r.awaitingCommits) > 0 || len(r.awaitingClears) > 0
	remaining, err := SumMarketingRealtimeUsage(remainder)
	if err != nil {
		return 0, err
	}
	remainingQuota, err := PriceMarketingRealtimeUsage(r.session.funding.PricingEvidence, remaining, at)
	if err != nil {
		return 0, err
	}
	inputQuota, err := PriceMarketingRealtimeUsage(r.session.funding.PricingEvidence, selected, at)
	if err != nil {
		return 0, err
	}
	var result *model.MarketingRealtimeResult
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = model.BeginMarketingRealtimeRound(tx, model.MarketingRealtimeRoundInput{UserID: r.info.UserId, FundingID: r.session.funding.ID, Reference: reference, QualifiedAt: at.Unix(), ReserveQuota: max(int64(r.session.estimate), inputQuota), Observed: observed, PendingCommitAdmission: !observed && len(r.awaitingCommits) > 0, ResponseReference: responseReference, MetadataFingerprint: r.nextMetadataFingerprint, InputRemainingQuota: remainingQuota, InputRemainingPending: pendingRemaining, Evidence: common.GetStringIfEmpty(r.nextEvidence, "locally received response start")})
		return err
	})
	if err != nil {
		return 0, err
	}
	r.session.funding = *result.Funding
	if result.Round == nil {
		return 0, model.ErrMarketingInsufficient
	}
	if result.Stop && !observed {
		return result.Round.ID, model.ErrMarketingInsufficient
	}
	if !result.Created && !observed {
		return result.Round.ID, model.ErrMarketingConflict
	}
	r.estimates[result.Round.ID] = selected
	if selectedVAD != "" {
		delete(r.vadSegments, selectedVAD)
	}
	if !observed {
		r.committed = dto.RealtimeUsage{}
		r.committedPending = false
	}
	if selectedText {
		r.input = dto.RealtimeUsage{}
		r.inputAudioBytes = 0
		r.inputAudioStart = r.totalAudioBytes
		r.inputPending = false
	}
	// An observed response already exists upstream. Stop further generation,
	// but retain its local tracker so frames already read during shutdown still
	// record output evidence and cannot turn a contradictory zero into free use.
	if result.Stop {
		return result.Round.ID, model.ErrMarketingInsufficient
	}
	return result.Round.ID, nil
}

func (r *MarketingRealtimeCoordinator) clientEvent(raw []byte, at time.Time) error {
	if r.closed {
		return model.ErrMarketingConflict
	}
	var event marketingRealtimeEvent
	if err := common.Unmarshal(raw, &event); err != nil {
		return err
	}
	switch event.Type {
	case "response.create":
		// A pending create has no provider ID yet. Do not guess which of two
		// uncorrelated creations a later provider receipt belongs to.
		if r.pending != 0 {
			return model.ErrMarketingConflict
		}
		r.sequence++
		reference := r.nextReference
		if reference == "" {
			return model.ErrMarketingInput
		}
		id, err := r.begin(reference, at, false, "")
		if err != nil {
			return err
		}
		r.pending = id
		r.pendingCommitCount = len(r.awaitingCommits)
	case "input_audio_buffer.commit":
		r.awaitingCommits = append(r.awaitingCommits, r.input)
		r.input = dto.RealtimeUsage{}
		r.inputAudioBytes = 0
		r.inputAudioStart = r.totalAudioBytes
		r.inputPending = false
		return r.reserveInput(at)
	case "input_audio_buffer.append":
		r.inputPending = true
		if err := r.estimateAudio(event.Audio, true, 0); err != nil {
			return err
		}
		return r.reserveInput(at)
	case "conversation.item.create", "session.update":
		if event.Type == "session.update" {
			r.vadSafe = false
		}
		if event.Type == "conversation.item.create" {
			r.inputPending = true
		}
		var legacy dto.RealtimeEvent
		if err := common.Unmarshal(raw, &legacy); err != nil {
			return err
		}
		if event.Type == "conversation.item.create" {
			legacy.Type = dto.RealtimeEventConversationItemCreated
		}
		text, _, err := CountTokenRealtime(r.info, legacy, r.info.UpstreamModelName)
		if err != nil {
			return err
		}
		if text < 0 || text > math.MaxInt32-r.input.TotalTokens {
			return model.ErrMarketingInput
		}
		if text > 0 {
			r.inputPending = true
		}
		r.input.TotalTokens += text
		r.input.InputTokens += text
		r.input.InputTokenDetails.TextTokens += text
		return r.reserveInput(at, r.hasPendingInput())
	case "input_audio_buffer.clear":
		// Keep this exact input segment until its ordered acknowledgement. New
		// appends belong to the next segment and must survive a delayed clear ACK.
		r.awaitingClears = append(r.awaitingClears, r.input)
		r.input = dto.RealtimeUsage{}
		r.inputAudioBytes = 0
		r.inputAudioStart = r.totalAudioBytes
		r.inputPending = false
		return r.reserveInput(at)
	}
	return nil
}

func (r *MarketingRealtimeCoordinator) reserveInput(at time.Time, pending ...bool) error {
	r.session.mu.Lock()
	defer r.session.mu.Unlock()
	parts := append([]dto.RealtimeUsage{r.input, r.committed}, r.awaitingCommits...)
	parts = append(parts, r.awaitingClears...)
	for _, segment := range r.vadSegments {
		if segment.Stopped {
			parts = append(parts, segment.Usage)
		}
	}
	total, err := SumMarketingRealtimeUsage(parts)
	if err != nil {
		return err
	}
	quota, err := PriceMarketingRealtimeUsage(r.session.funding.PricingEvidence, total, at)
	if err != nil {
		return err
	}
	var result *model.MarketingRealtimeResult
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = model.ReserveMarketingRealtimeInput(tx, r.info.UserId, r.session.funding.ID, quota, at.Unix(), pending...)
		return err
	})
	if err != nil {
		return err
	}
	r.session.funding = *result.Funding
	if result.Stop {
		return model.ErrMarketingInsufficient
	}
	return nil
}

// estimateAudio reserves against cumulative bytes, not independently rounded
// chunks. Even tiny chunks therefore cannot accumulate unbounded free audio.
// This is only an admission estimate, never a settlement fact.
func (r *MarketingRealtimeCoordinator) estimateAudio(encoded string, input bool, roundID int64) error {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return model.ErrMarketingInput
	}
	format := r.info.OutputAudioFormat
	count := r.outputAudioBytes[roundID]
	if input {
		format = r.info.InputAudioFormat
		count = r.inputAudioBytes
	}
	if int64(len(data)) > math.MaxInt32-count {
		return model.ErrMarketingInput
	}
	count += int64(len(data))
	bytesPerSecond := int64(48000)
	switch format {
	case "pcm16", "":
	case "g711_ulaw", "g711_alaw":
		bytesPerSecond = 8000
	default:
		return model.ErrMarketingInput
	}
	// Existing estimator rates: input 100/3.6 tokens/s, output 200/14.4.
	rate := 100.0 / 3.6
	if !input {
		rate = 200.0 / 14.4
	}
	estimated, clamp := common.QuotaRoundChecked(math.Ceil(float64(count) / float64(bytesPerSecond) * rate))
	if clamp != nil {
		return model.ErrMarketingInput
	}
	usage := r.estimates[roundID]
	if input {
		usage = r.input
	}
	if input {
		usage.InputTokenDetails.AudioTokens = estimated
		if estimated > math.MaxInt32-usage.InputTokenDetails.TextTokens {
			return model.ErrMarketingInput
		}
		usage.InputTokens = estimated + usage.InputTokenDetails.TextTokens
		r.inputAudioBytes = count
		if int64(len(data)) > math.MaxInt64-r.totalAudioBytes {
			return model.ErrMarketingInput
		}
		r.totalAudioBytes += int64(len(data))
	} else {
		usage.OutputTokenDetails.AudioTokens = estimated
		if estimated > math.MaxInt32-usage.OutputTokenDetails.TextTokens {
			return model.ErrMarketingInput
		}
		usage.OutputTokens = estimated + usage.OutputTokenDetails.TextTokens
		r.outputAudioBytes[roundID] = count
	}
	if usage.InputTokens > math.MaxInt32-usage.OutputTokens {
		return model.ErrMarketingInput
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	if input {
		r.input = usage
	} else {
		r.estimates[roundID] = usage
	}
	return nil
}

func (r *MarketingRealtimeCoordinator) serverEvent(raw []byte, at time.Time) error {
	var event marketingRealtimeEvent
	if err := common.Unmarshal(raw, &event); err != nil {
		return err
	}
	switch event.Type {
	case "session.created", "session.updated":
		if err := r.confirmVADConfiguration(raw); err != nil {
			return err
		}
		if event.Session == nil {
			return nil
		}
		if event.Session.InputAudioFormat != "" {
			r.info.InputAudioFormat = event.Session.InputAudioFormat
		}
		if event.Session.OutputAudioFormat != "" {
			r.info.OutputAudioFormat = event.Session.OutputAudioFormat
		}
		if audio := event.Session.Audio; audio != nil {
			if audio.Input != nil && audio.Input.Format.Type != "" {
				format, err := marketingRealtimeAudioFormat(audio.Input.Format.Type, audio.Input.Format.Rate)
				if err != nil {
					return err
				}
				r.info.InputAudioFormat = format
			}
			if audio.Output != nil && audio.Output.Format.Type != "" {
				format, err := marketingRealtimeAudioFormat(audio.Output.Format.Type, audio.Output.Format.Rate)
				if err != nil {
					return err
				}
				r.info.OutputAudioFormat = format
			}
		}
	case "input_audio_buffer.speech_started":
		if event.ItemID == "" || event.AudioStartMS == nil || *event.AudioStartMS < 0 {
			return model.ErrMarketingInput
		}
		if _, exists := r.vadSegments[event.ItemID]; !exists {
			r.vadSegments[event.ItemID] = &marketingRealtimeInputSegment{StartMS: *event.AudioStartMS}
		}
	case "input_audio_buffer.speech_stopped":
		return r.stopVADInput(event, at)
	case "input_audio_buffer.committed":
		if segment := r.vadSegments[event.ItemID]; segment != nil && segment.Stopped {
			segment.Committed = true
			return nil
		}
		// A local explicit commit still uses its own ordered acknowledgement;
		// automatic VAD commits with a known item never consume that queue.
		if len(r.awaitingCommits) == 0 {
			return nil
		}
		merged, err := SumMarketingRealtimeUsage([]dto.RealtimeUsage{r.committed, r.awaitingCommits[0]})
		if err != nil {
			return err
		}
		r.committed = merged
		r.committedPending = true
		r.awaitingCommits = r.awaitingCommits[1:]
		delete(r.vadSegments, event.ItemID)
		if r.pending != 0 && r.pendingCommitCount > 0 {
			r.pendingCommitCount--
			return r.attachCommittedInput(at)
		}
	case "input_audio_buffer.cleared":
		if len(r.awaitingClears) == 0 {
			return nil
		}
		r.awaitingClears = r.awaitingClears[1:]
		return r.reserveInput(at, r.hasPendingInput())
	case "response.created":
		if event.Response == nil || event.Response.ID == "" {
			return model.ErrMarketingInput
		}
		key := marketingRealtimeResponseKey(event.Response.ID)
		if _, ok := r.responses[key]; ok {
			return nil
		}
		id, admissionErr := r.begin("provider/"+key, at, true, event.Response.ID)
		if id == 0 {
			return admissionErr
		}
		r.responses[key] = id
		// Stop is a committed admission outcome, not permission to discard an
		// already observed provider identity. Persist it before returning the
		// stop signal so a late terminal can recover this exact round on any node.
		factErr := r.fact(id, event.Response.ID, nil, false, "start/"+key, at)
		return errors.Join(admissionErr, factErr)
	case "response.done":
		if event.Response == nil || event.Response.ID == "" {
			return model.ErrMarketingInput
		}
		key := marketingRealtimeResponseKey(event.Response.ID)
		id, ok := r.responses[key]
		if !ok {
			return model.ErrMarketingConflict
		}
		usage, err := ParseMarketingRealtimeUsage(event.Response.Usage)
		if err != nil || (usage != nil && ((usage.OutputTokens == 0 && r.outputSeen[id]) || (usage.OutputTokenDetails.AudioTokens == 0 && r.audioOutputSeen[id]))) {
			usage = nil
		}
		// Cancellation and failure may still have actual charged tokens. Status
		// does not make a missing usage object trustworthy, nor erase known usage.
		if err = r.fact(id, event.Response.ID, usage, true, "terminal/"+key, at); err != nil {
			return err
		}
		delete(r.estimates, id)
		delete(r.outputAudioBytes, id)
	case "response.audio.delta", "response.output_audio.delta", "response.text.delta", "response.output_text.delta", "response.function_call_arguments.delta":
		if event.ResponseID == "" {
			return model.ErrMarketingInput
		}
		id, ok := r.responses[marketingRealtimeResponseKey(event.ResponseID)]
		if !ok {
			return model.ErrMarketingConflict
		}
		if _, active := r.estimates[id]; !active {
			return nil
		}
		audioOutput := event.Type == "response.audio.delta" || event.Type == "response.output_audio.delta"
		if event.Delta != "" && (!r.outputSeen[id] || (audioOutput && !r.audioOutputSeen[id])) {
			r.outputSeen[id] = true
			if audioOutput {
				r.audioOutputSeen[id] = true
			}
			if err := r.fact(id, event.ResponseID, nil, false, fmt.Sprintf("output/%s/audio/%t", marketingRealtimeResponseKey(event.ResponseID), audioOutput), at, true, audioOutput); err != nil {
				return err
			}
		}
		if event.Type == "response.audio.delta" || event.Type == "response.output_audio.delta" {
			if err := r.estimateAudio(event.Delta, false, id); err != nil {
				return err
			}
		} else {
			tokens := CountTokenInput(event.Delta, r.info.UpstreamModelName)
			usage := r.estimates[id]
			if tokens < 0 || tokens > math.MaxInt32-usage.TotalTokens {
				return model.ErrMarketingInput
			}
			usage.OutputTokenDetails.TextTokens += tokens
			usage.OutputTokens += tokens
			usage.TotalTokens += tokens
			r.estimates[id] = usage
		}
		return r.reserveRound(id, at)
	}
	return nil
}

func marketingRealtimeResponseKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func marketingRealtimeAudioFormat(kind string, rate *int) (string, error) {
	switch kind {
	case "audio/pcm":
		if rate != nil && *rate != 24000 {
			return "", model.ErrMarketingInput
		}
		return "pcm16", nil
	case "audio/pcmu":
		return "g711_ulaw", nil
	case "audio/pcma":
		return "g711_alaw", nil
	default:
		return "", model.ErrMarketingInput
	}
}

func (r *MarketingRealtimeCoordinator) reserveRound(id int64, at time.Time) error {
	r.session.mu.Lock()
	defer r.session.mu.Unlock()
	usage, err := SumMarketingRealtimeUsage([]dto.RealtimeUsage{r.total, r.estimates[id]})
	if err != nil {
		return err
	}
	quota, err := PriceMarketingRealtimeUsage(r.session.funding.PricingEvidence, usage, at)
	if err != nil {
		return err
	}
	target := max(int64(0), quota-r.session.funding.ActualQuota)
	var result *model.MarketingRealtimeResult
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = model.ReserveMarketingRealtimeRound(tx, r.info.UserId, r.session.funding.ID, id, target)
		return err
	})
	if err != nil {
		return err
	}
	r.session.funding = *result.Funding
	if result.Stop {
		return model.ErrMarketingInsufficient
	}
	return nil
}

func (r *MarketingRealtimeCoordinator) fact(id int64, responseID string, usage *dto.RealtimeUsage, terminal bool, eventKey string, at time.Time, observedOutput ...bool) error {
	r.session.mu.Lock()
	defer r.session.mu.Unlock()
	record := r.logRecord(r.total)
	var result *model.MarketingRealtimeResult
	var total dto.RealtimeUsage
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		snapshot, err := model.GetMarketingRealtimeSnapshot(tx, r.info.UserId, r.session.funding.ID)
		if err != nil {
			return err
		}
		outputObserved := len(observedOutput) > 0 && observedOutput[0]
		audioObserved := len(observedOutput) > 1 && observedOutput[1]
		evidence, err := common.Marshal(map[string]any{"response_key": marketingRealtimeResponseKey(responseID), "terminal": terminal, "usage": usage, "output_observed": outputObserved, "audio_output_observed": audioObserved})
		if err != nil {
			return err
		}
		digest := sha256.Sum256(evidence)
		result, err = model.ApplyMarketingRealtimeFact(tx, model.MarketingRealtimeFactInput{UserID: r.info.UserId, FundingID: r.session.funding.ID, RoundID: id, EventReference: eventKey + "/" + hex.EncodeToString(digest[:]), ResponseReference: responseID, ExpectedRevision: snapshot.Funding.Revision, Usage: usage, Terminal: terminal, OutputObserved: outputObserved, AudioOutputObserved: audioObserved, Evidence: string(evidence)})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	r.session.funding = *result.Funding
	// Supplier facts commit before pricing or log projection. A pricing failure
	// must leave replayable actual usage, rather than roll it back with money.
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		snapshot, err := model.GetMarketingRealtimeSnapshot(tx, r.info.UserId, r.session.funding.ID)
		if err != nil {
			return err
		}
		result.Funding = snapshot.Funding
		// Retain all observed facts for display, but only a complete ordered prefix
		// may affect cumulative pricing. Later replies never inherit old debt.
		var all, prefix []dto.RealtimeUsage
		complete := true
		hasUnknown := false
		for _, round := range snapshot.Rounds {
			hasUnknown = hasUnknown || round.Unknown
			if round.Usage != nil {
				all = append(all, *round.Usage)
			}
		}
		total, err = SumMarketingRealtimeUsage(all)
		if err != nil {
			return err
		}
		r.total = total // Facts were committed by the preceding transaction.
		for _, round := range snapshot.Rounds {
			if round.ResponseOrder == 0 {
				continue
			}
			complete = complete && round.Terminal && !round.Unknown && round.Usage != nil
			if !complete || hasUnknown {
				continue
			}
			prefix = append(prefix, *round.Usage)
			if round.UsageJSON == round.PricedUsageJSON {
				continue
			}
			cumulative, err := SumMarketingRealtimeUsage(prefix)
			if err != nil {
				return err
			}
			quota, err := PriceMarketingRealtimeUsage(snapshot.Funding.PricingEvidence, cumulative, at)
			if err != nil {
				return err
			}
			encoded, err := common.Marshal(map[string]any{"usage": round.Usage, "evaluation_time": at})
			if err != nil {
				return err
			}
			usageHash := sha256.Sum256([]byte(round.UsageJSON))
			result, err = model.ApplyMarketingRealtimeFact(tx, model.MarketingRealtimeFactInput{UserID: r.info.UserId, FundingID: r.session.funding.ID, RoundID: round.ID, EventReference: fmt.Sprintf("price/%d/%x", round.ID, usageHash), ExpectedRevision: result.Funding.Revision, Usage: round.Usage, CumulativeQuota: &quota, Terminal: true, Evidence: string(encoded)})
			if err != nil {
				return err
			}
		}
		if terminal {
			record.PromptTokens = total.InputTokens
			record.CompletionTokens = total.OutputTokens
			var other map[string]any
			if err = common.UnmarshalJsonStr(record.Other, &other); err != nil {
				return err
			}
			other["audio_input"], other["audio_output"] = total.InputTokenDetails.AudioTokens, total.OutputTokenDetails.AudioTokens
			other["text_input"], other["text_output"] = total.InputTokenDetails.TextTokens, total.OutputTokenDetails.TextTokens
			other["cache_tokens"] = total.InputTokenDetails.CachedTokens
			encoded, encodeErr := common.Marshal(other)
			if encodeErr != nil {
				return encodeErr
			}
			record.Other = string(encoded)
			_, err = model.RecordMarketingFundingUsage(tx, result.Funding, record)
		}
		return err
	})
	if err != nil {
		return err
	}
	r.total = total
	r.session.funding = *result.Funding
	if result.Stop && result.Funding.State != "settled" {
		return model.ErrMarketingReview
	}
	return nil
}

func (r *MarketingRealtimeCoordinator) logRecord(usage dto.RealtimeUsage) model.Log {
	if r.replayLog != nil {
		return *r.replayLog
	}
	p := r.info.PriceData
	other := GenerateWssOtherInfo(r.ctx, r.info, &usage, p.ModelRatio, p.GroupRatioInfo.GroupRatio, p.CompletionRatio, p.AudioRatio, p.AudioCompletionRatio, p.ModelPrice, p.GroupRatioInfo.GroupSpecialRatio)
	other.SetPublic("cache_tokens", usage.InputTokenDetails.CachedTokens)
	record := model.BuildConsumeLog(r.ctx, r.info.UserId, model.RecordConsumeLogParams{ChannelId: r.info.ChannelId, ModelName: r.info.OriginModelName, TokenId: r.info.TokenId, TokenName: r.ctx.GetString("token_name"), PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, IsStream: true, UseTimeSeconds: int(time.Since(r.info.StartTime).Seconds()), Group: r.info.UsingGroup, Other: other})
	record.RequestId = r.info.RequestId
	return *record
}

func (r *MarketingRealtimeCoordinator) Close(reason string) error {
	r.session.mu.Lock()
	defer r.session.mu.Unlock()
	if r.closed {
		return nil
	}
	record := r.logRecord(r.total)
	var result *model.MarketingRealtimeResult
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = model.CloseMarketingRealtimeFunding(tx, r.info.UserId, r.session.funding.ID, reason)
		if err != nil {
			return err
		}
		record.RequestId = r.info.RequestId
		_, err = model.RecordMarketingFundingUsage(tx, result.Funding, record)
		return err
	})
	if err != nil {
		return err
	}
	r.closed = true
	r.session.funding = *result.Funding
	return nil
}

// Attach a delayed acknowledgement only to the local create that included that
// commit. It never gives a later uncommitted segment the earlier qualification.
func (r *MarketingRealtimeCoordinator) attachCommittedInput(at time.Time) error {
	r.session.mu.Lock()
	defer r.session.mu.Unlock()
	id := r.pending
	assigned, err := SumMarketingRealtimeUsage([]dto.RealtimeUsage{r.estimates[id], r.committed})
	if err != nil {
		return err
	}
	remainingParts := append([]dto.RealtimeUsage{r.input}, r.awaitingCommits...)
	remainingParts = append(remainingParts, r.awaitingClears...)
	for _, segment := range r.vadSegments {
		if segment.Stopped {
			remainingParts = append(remainingParts, segment.Usage)
		}
	}
	remaining, err := SumMarketingRealtimeUsage(remainingParts)
	if err != nil {
		return err
	}
	target, err := PriceMarketingRealtimeUsage(r.session.funding.PricingEvidence, assigned, at)
	if err != nil {
		return err
	}
	remainingQuota, err := PriceMarketingRealtimeUsage(r.session.funding.PricingEvidence, remaining, at)
	if err != nil {
		return err
	}
	pending := r.inputPending || len(r.awaitingCommits) > 0 || len(r.awaitingClears) > 0 || len(r.vadSegments) > 0
	var result *model.MarketingRealtimeResult
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = model.AttachMarketingRealtimeInput(tx, r.info.UserId, r.session.funding.ID, id, max(target, int64(r.session.estimate)), remainingQuota, pending)
		return err
	})
	if err != nil {
		return err
	}
	r.session.funding = *result.Funding
	r.estimates[id] = assigned
	r.committed = dto.RealtimeUsage{}
	r.committedPending = false
	if result.Stop {
		return model.ErrMarketingInsufficient
	}
	return nil
}

// Client metadata is kept intact. Only a server-issued marker (or a nonempty,
// unambiguous complete metadata fingerprint) can identify a manual response.
type marketingRealtimeManual struct {
	RoundID         int64
	MarkerKey       string
	MarkerValue     string
	MetadataPresent bool
	ResponseKey     string
}

func marketingRealtimeMetadataFingerprint(metadata map[string]string) (string, error) {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([][2]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, [2]string{key, metadata[key]})
	}
	encoded, err := common.Marshal(pairs)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (r *MarketingRealtimeCoordinator) ClientEvent(raw []byte, at time.Time) error {
	_, err := r.ProcessClientEvent(raw, at)
	return err
}
func (r *MarketingRealtimeCoordinator) ServerEvent(raw []byte, at time.Time) error {
	_, err := r.ProcessServerEvent(raw, at)
	return err
}

func (r *MarketingRealtimeCoordinator) ProcessClientEvent(raw []byte, at time.Time) ([]byte, error) {
	var event marketingRealtimeEvent
	if err := common.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	if event.Type != "response.create" {
		return raw, r.clientEvent(raw, at)
	}
	var envelope map[string]json.RawMessage
	if err := common.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	response := map[string]json.RawMessage{}
	if value, ok := envelope["response"]; ok && string(value) != "null" {
		if err := common.Unmarshal(value, &response); err != nil {
			return nil, err
		}
	}
	metadata := map[string]string{}
	value, present := response["metadata"]
	if present && string(value) != "null" {
		if err := common.Unmarshal(value, &metadata); err != nil {
			return nil, err
		}
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	if len(metadata) > 16 {
		return nil, model.ErrMarketingInput
	}
	manual := &marketingRealtimeManual{MetadataPresent: present}
	var reference, fingerprint string
	if len(metadata) < 16 {
		marker := "_silvermoon_request"
		for suffix := 1; ; suffix++ {
			if _, exists := metadata[marker]; !exists {
				break
			}
			marker = fmt.Sprintf("_silvermoon_request_%d", suffix)
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		manual.MarkerKey, manual.MarkerValue = marker, hex.EncodeToString(random[:])
		metadata[marker] = manual.MarkerValue
		reference = "manual/" + manual.MarkerValue
	} else {
		var err error
		fingerprint, err = marketingRealtimeMetadataFingerprint(metadata)
		if err != nil {
			return nil, err
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return nil, err
		}
		reference = "metadata/" + fingerprint + "/" + hex.EncodeToString(random[:])
	}
	encodedMetadata, err := common.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	response["metadata"] = encodedMetadata
	encodedResponse, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	envelope["response"] = encodedResponse
	forwarded, err := common.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	proof, err := common.Marshal(map[string]any{"kind": "manual_metadata", "marker_key": manual.MarkerKey, "reference_hash": marketingRealtimeResponseKey(reference), "metadata_present": manual.MetadataPresent, "metadata_fingerprint": fingerprint})
	if err != nil {
		return nil, err
	}
	r.nextReference, r.nextEvidence, r.nextMetadataFingerprint = reference, string(proof), fingerprint
	err = r.clientEvent(forwarded, at)
	r.nextReference, r.nextEvidence, r.nextMetadataFingerprint = "", "", ""
	if err != nil {
		return nil, err
	}
	manual.RoundID = r.pending
	r.manual[reference] = manual
	return forwarded, nil
}

func (r *MarketingRealtimeCoordinator) echoedManual(metadata map[string]string, responseID string) (*marketingRealtimeManual, error) {
	var matched *marketingRealtimeManual
	for key, value := range metadata {
		if !strings.HasPrefix(key, "_silvermoon_request") || len(value) != 32 {
			continue
		}
		reference := "manual/" + value
		candidate := r.manual[reference]
		if candidate == nil {
			var round *model.MarketingRealtimeRound
			err := model.DB.Transaction(func(tx *gorm.DB) error {
				var err error
				round, err = model.FindMarketingRealtimeRoundByReference(tx, r.info.UserId, r.session.funding.ID, reference)
				return err
			})
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var proof struct {
				MarkerKey       string `json:"marker_key"`
				MetadataPresent bool   `json:"metadata_present"`
			}
			if err = common.UnmarshalJsonStr(round.Evidence, &proof); err != nil || proof.MarkerKey != key {
				continue
			}
			roundID := round.ID
			if round.SupersededBy != 0 {
				roundID = round.SupersededBy
			}
			candidate = &marketingRealtimeManual{RoundID: roundID, MarkerKey: key, MarkerValue: value, MetadataPresent: proof.MetadataPresent}
			r.manual[reference] = candidate
		}
		if candidate.MarkerKey != key || candidate.MarkerValue != value {
			continue
		}
		if matched != nil && matched != candidate {
			return nil, model.ErrMarketingConflict
		}
		matched = candidate
	}
	if matched != nil {
		return matched, nil
	}
	if len(metadata) == 0 {
		return nil, nil
	}
	fingerprint, err := marketingRealtimeMetadataFingerprint(metadata)
	if err != nil {
		return nil, err
	}
	var round *model.MarketingRealtimeRound
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		round, err = model.FindMarketingRealtimeRoundByMetadata(tx, r.info.UserId, r.session.funding.ID, fingerprint, responseID)
		return err
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	candidate := &marketingRealtimeManual{RoundID: round.ID, MetadataPresent: true}
	if round.ResponseKey != "" {
		candidate.ResponseKey = marketingRealtimeResponseKey(responseID)
	}
	return candidate, nil
}

func (r *MarketingRealtimeCoordinator) ProcessServerEvent(raw []byte, at time.Time) ([]byte, error) {
	var event marketingRealtimeEvent
	if err := common.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	if (event.Type != "response.created" && event.Type != "response.done") || event.Response == nil {
		return raw, r.serverEvent(raw, at)
	}
	key := marketingRealtimeResponseKey(event.Response.ID)
	if _, exists := r.responses[key]; !exists {
		var persisted *model.MarketingRealtimeRound
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			persisted, err = model.FindMarketingRealtimeRoundByResponse(tx, r.info.UserId, r.session.funding.ID, event.Response.ID)
			return err
		})
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			r.responses[key] = persisted.ID
			r.outputSeen[persisted.ID] = persisted.OutputObserved
			r.audioOutputSeen[persisted.ID] = persisted.AudioOutputObserved
			if !persisted.Terminal {
				r.estimates[persisted.ID] = dto.RealtimeUsage{}
			}
		}
	}
	manual, err := r.echoedManual(event.Response.Metadata, event.Response.ID)
	if err != nil {
		return nil, err
	}
	if manual != nil {
		if manual.ResponseKey != "" && manual.ResponseKey != key {
			return nil, model.ErrMarketingConflict
		}
		id, exists := r.responses[key]
		if exists && id != manual.RoundID {
			combined, err := SumMarketingRealtimeUsage([]dto.RealtimeUsage{r.estimates[manual.RoundID], r.estimates[id]})
			if err != nil {
				return nil, err
			}
			quota, err := PriceMarketingRealtimeUsage(r.session.funding.PricingEvidence, combined, at)
			if err != nil {
				return nil, err
			}
			proof := marketingRealtimeResponseKey(manual.MarkerValue + key)
			var result *model.MarketingRealtimeResult
			err = model.DB.Transaction(func(tx *gorm.DB) error {
				var err error
				result, err = model.CorrelateMarketingRealtimeResponse(tx, r.info.UserId, r.session.funding.ID, manual.RoundID, id, quota, proof)
				return err
			})
			if err != nil {
				return nil, err
			}
			oldID := manual.RoundID
			r.session.funding = *result.Funding
			manual.RoundID = id
			r.estimates[id] = combined
			r.outputSeen[id] = r.outputSeen[id] || r.outputSeen[oldID]
			r.audioOutputSeen[id] = r.audioOutputSeen[id] || r.audioOutputSeen[oldID]
			delete(r.estimates, oldID)
			if r.pending == oldID {
				r.pending = 0
			}
			// The following actual terminal is still recorded even when current funds
			// cannot cover the now proven response qualification.
		} else if !exists {
			r.responses[key] = manual.RoundID
			if r.pending == manual.RoundID {
				r.pending = 0
			}
			if event.Type == "response.created" {
				if err = r.fact(manual.RoundID, event.Response.ID, nil, false, "start/"+key, at); err != nil {
					return nil, err
				}
			}
		}
		manual.ResponseKey = key
	}
	if err = r.serverEvent(raw, at); err != nil {
		return nil, err
	}
	if manual == nil || manual.MarkerKey == "" {
		return raw, nil
	}
	var envelope map[string]json.RawMessage
	if err = common.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	var response map[string]json.RawMessage
	if err = common.Unmarshal(envelope["response"], &response); err != nil {
		return nil, err
	}
	metadata := event.Response.Metadata
	delete(metadata, manual.MarkerKey)
	if len(metadata) == 0 && !manual.MetadataPresent {
		delete(response, "metadata")
	} else {
		encoded, encodeErr := common.Marshal(metadata)
		if encodeErr != nil {
			return nil, encodeErr
		}
		response["metadata"] = encoded
	}
	encoded, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	envelope["response"] = encoded
	return common.Marshal(envelope)
}

// Session creation supplies the protocol baseline: server_vad automatically
// creates responses unless disabled, and idle replies are opt-in. Official
// session.created examples omit both optional fields. Later partial server
// updates retain confirmed state; client requests alone never change it.
func (r *MarketingRealtimeCoordinator) confirmVADConfiguration(raw []byte) error {
	var event struct {
		Type    string                     `json:"type"`
		Session map[string]json.RawMessage `json:"session"`
	}
	if err := common.Unmarshal(raw, &event); err != nil {
		return err
	}
	if event.Type == "session.created" {
		r.vadMode, r.vadAuto, r.vadIdle = "", true, false
		r.vadTranscription, r.vadTranscriptionKnown = false, false
	}
	r.vadSafe = false
	input := event.Session
	transcriptionKey := "input_audio_transcription"
	if value, ok := input["audio"]; ok && string(value) != "null" {
		var audio struct {
			Input map[string]json.RawMessage `json:"input"`
		}
		if err := common.Unmarshal(value, &audio); err != nil {
			return err
		}
		if audio.Input != nil {
			input, transcriptionKey = audio.Input, "transcription"
		}
	}
	if value, exists := input[transcriptionKey]; exists {
		r.vadTranscriptionKnown = true
		r.vadTranscription = strings.TrimSpace(string(value)) != "null"
	}
	if value, exists := input["turn_detection"]; exists {
		if strings.TrimSpace(string(value)) == "null" {
			r.vadMode = ""
		} else {
			var turn map[string]json.RawMessage
			if err := common.Unmarshal(value, &turn); err != nil {
				return err
			}
			if value, exists := turn["type"]; exists {
				if err := common.Unmarshal(value, &r.vadMode); err != nil {
					return err
				}
			}
			if value, exists := turn["create_response"]; exists {
				var automatic *bool
				if err := common.Unmarshal(value, &automatic); err != nil || automatic == nil {
					return model.ErrMarketingInput
				}
				r.vadAuto = *automatic
			}
			if value, exists := turn["idle_timeout_ms"]; exists {
				var timeout *int64
				if err := common.Unmarshal(value, &timeout); err != nil || (timeout != nil && *timeout < 0) {
					return model.ErrMarketingInput
				}
				r.vadIdle = timeout != nil && *timeout > 0
			}
		}
	}
	r.vadSafe = r.vadMode == "server_vad" && r.vadAuto && !r.vadIdle && r.vadTranscriptionKnown && !r.vadTranscription
	return nil
}

// audio_end_ms is a session audio offset, not a response identity or a billing
// timestamp. Split exactly at that boundary; a newer buffer remains reserved.
func (r *MarketingRealtimeCoordinator) stopVADInput(event marketingRealtimeEvent, at time.Time) error {
	segment := r.vadSegments[event.ItemID]
	if segment == nil || event.AudioEndMS == nil || *event.AudioEndMS < segment.StartMS {
		return nil
	}
	if segment.Stopped {
		return nil
	}
	bytesPerSecond := int64(48000)
	switch r.info.InputAudioFormat {
	case "pcm16", "":
	case "g711_ulaw", "g711_alaw":
		bytesPerSecond = 8000
	default:
		return nil
	}
	if *event.AudioEndMS > math.MaxInt64/bytesPerSecond {
		return model.ErrMarketingInput
	}
	end := *event.AudioEndMS * bytesPerSecond / 1000
	if end <= r.inputAudioStart || end > r.totalAudioBytes || end-r.inputAudioStart > r.inputAudioBytes {
		return nil
	}
	selectedBytes := end - r.inputAudioStart
	remainingBytes := r.inputAudioBytes - selectedBytes
	selectedTokens, clamp := common.QuotaRoundChecked(math.Ceil(float64(selectedBytes) / float64(bytesPerSecond) * (100.0 / 3.6)))
	if clamp != nil {
		return model.ErrMarketingInput
	}
	remainingTokens, clamp := common.QuotaRoundChecked(math.Ceil(float64(remainingBytes) / float64(bytesPerSecond) * (100.0 / 3.6)))
	if clamp != nil || remainingTokens > math.MaxInt32-r.input.InputTokenDetails.TextTokens {
		return model.ErrMarketingInput
	}
	segment.Usage = dto.RealtimeUsage{InputTokens: selectedTokens, TotalTokens: selectedTokens, InputTokenDetails: dto.InputTokenDetails{AudioTokens: selectedTokens}}
	segment.Stopped = true
	r.input.InputTokenDetails.AudioTokens = remainingTokens
	r.input.InputTokens = remainingTokens + r.input.InputTokenDetails.TextTokens
	r.input.TotalTokens = r.input.InputTokens
	r.inputAudioBytes, r.inputAudioStart = remainingBytes, end
	r.inputPending = r.input.InputTokens > 0 || remainingBytes > 0
	return r.reserveInput(at)
}

func (r *MarketingRealtimeCoordinator) hasPendingInput() bool {
	if r.inputPending || r.committedPending || len(r.awaitingCommits) > 0 || len(r.awaitingClears) > 0 {
		return true
	}
	for _, segment := range r.vadSegments {
		if segment.Stopped {
			return true
		}
	}
	return false
}
