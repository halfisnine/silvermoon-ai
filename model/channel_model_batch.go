package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"gorm.io/gorm"
)

var ErrChannelModelBatchConflict = errors.New("Channel model data changed. Reload before saving.")

type ChannelModelBatchChannel struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Type         int     `json:"type"`
	Models       string  `json:"models"`
	ModelMapping *string `json:"model_mapping"`
}

type ChannelModelBatchRow struct {
	SourceName   string `json:"source_name"`
	UpstreamName string `json:"upstream_name"`
	Metadata     *Model `json:"metadata"`
}

type ChannelModelBatchSnapshot struct {
	Channel  ChannelModelBatchChannel   `json:"channel"`
	Rows     []ChannelModelBatchRow     `json:"rows"`
	Vendors  []Vendor                   `json:"vendors"`
	Metadata []Model                    `json:"metadata"`
	Channels []ChannelModelBatchChannel `json:"channels"`
	Version  string                     `json:"version"`
}

type ChannelModelBatchChange struct {
	SourceName             string         `json:"source_name"`
	ModelName              string         `json:"model_name"`
	VendorID               *int           `json:"vendor_id,omitempty"`
	Pricing                *PricingValues `json:"pricing,omitempty"`
	ExpectedPricingVersion string         `json:"expected_pricing_version,omitempty"`
}

type ChannelModelBatchRequest struct {
	ExpectedVersion string                    `json:"expected_version"`
	Changes         []ChannelModelBatchChange `json:"changes"`
}

// Only public routing/metadata fields are read or serialized; credentials and
// channel settings never enter the browser snapshot or its version hash.
func GetChannelModelBatch(id int) (*ChannelModelBatchSnapshot, error) {
	var snapshot *ChannelModelBatchSnapshot
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = readChannelModelBatch(tx, id)
		return err
	})
	return snapshot, err
}

func readChannelModelBatch(tx *gorm.DB, id int) (*ChannelModelBatchSnapshot, error) {
	snapshot := &ChannelModelBatchSnapshot{Rows: []ChannelModelBatchRow{}, Vendors: []Vendor{}, Metadata: []Model{}}
	if err := tx.Model(&Channel{}).Select("id", "name", "type", "models", "model_mapping").Order("id").Find(&snapshot.Channels).Error; err != nil {
		return nil, err
	}
	found := false
	for _, channel := range snapshot.Channels {
		if channel.ID == id {
			snapshot.Channel, found = channel, true
			break
		}
	}
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	if err := tx.Order("id").Find(&snapshot.Metadata).Error; err != nil {
		return nil, err
	}
	if err := tx.Order("id").Find(&snapshot.Vendors).Error; err != nil {
		return nil, err
	}
	// Reuse New API's built-in display catalog without creating records on reads.
	vendorMap := make(map[int]*Vendor, len(snapshot.Vendors))
	for i := range snapshot.Vendors {
		vendorMap[snapshot.Vendors[i].Id] = &snapshot.Vendors[i]
	}
	namesOfVendors := make([]string, 0, len(defaultVendorDisplayIDs))
	for name := range defaultVendorDisplayIDs {
		namesOfVendors = append(namesOfVendors, name)
	}
	slices.Sort(namesOfVendors)
	for _, name := range namesOfVendors {
		getDisplayVendor(name, vendorMap)
	}
	snapshot.Vendors = make([]Vendor, 0, len(vendorMap))
	for _, vendor := range vendorMap {
		snapshot.Vendors = append(snapshot.Vendors, *vendor)
	}
	slices.SortFunc(snapshot.Vendors, func(a, b Vendor) int { return strings.Compare(a.Name, b.Name) })
	mapping := map[string]string{}
	if raw := snapshot.Channel.ModelMapping; raw != nil && strings.TrimSpace(*raw) != "" {
		if err := common.UnmarshalJsonStr(*raw, &mapping); err != nil || mapping == nil {
			return nil, errors.New("Model mapping must be a valid JSON object")
		}
	}
	names := []string{}
	seen := map[string]bool{}
	for name := range strings.SplitSeq(snapshot.Channel.Models, ",") {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	metadata := resolveModelMetadata(snapshot.Metadata, names)
	for _, name := range names {
		upstream, err := channelBatchUpstream(name, mapping)
		if err != nil {
			return nil, err
		}
		snapshot.Rows = append(snapshot.Rows, ChannelModelBatchRow{SourceName: name, UpstreamName: upstream, Metadata: metadata[name]})
	}
	encoded, err := common.Marshal([]any{snapshot.Channel, snapshot.Metadata, snapshot.Vendors, snapshot.Channels})
	if err != nil {
		return nil, err
	}
	snapshot.Version = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return snapshot, nil
}

// Match the relay's chained redirects, including reasoning suffix fallback.
func channelBatchUpstream(name string, mapping map[string]string) (string, error) {
	seen := map[string]bool{name: true}
	for {
		next := mapping[name]
		if next == "" {
			next = mapping[reasoning.BaseModelName(name)]
		}
		if next == "" || next == name {
			return name, nil
		}
		if seen[next] {
			return "", errors.New("Model mapping contains a cycle")
		}
		seen[next], name = true, next
	}
}

func ApplyChannelModelBatch(id int, request ChannelModelBatchRequest) error {
	if len(request.Changes) == 0 || len(request.Changes) > 1000 {
		return errors.New("Select between 1 and 1000 models")
	}
	// Same lock order as model deletion with pricing: metadata mutex, pricing
	// mutex/options, metadata anchor, channel row. Publish caches after commit.
	metadataMutationMu.Lock()
	defer metadataMutationMu.Unlock()
	err := mutateModelPricingOptions(func(tx *gorm.DB, values map[string]map[string]any) error {
		if err := lockMetadataMutation(tx); err != nil {
			return err
		}
		var channel Channel
		if err := lockForUpdate(tx).Omit("key", "other", "other_info", "settings", "setting", "param_override", "header_override").First(&channel, id).Error; err != nil {
			return err
		}
		snapshot, err := readChannelModelBatch(tx, id)
		if err != nil {
			return err
		}
		if request.ExpectedVersion == "" || request.ExpectedVersion != snapshot.Version {
			return ErrChannelModelBatchConflict
		}
		rows := map[string]ChannelModelBatchRow{}
		exact := map[string]Model{}
		vendors := map[int]Vendor{}
		for _, row := range snapshot.Rows {
			rows[row.SourceName] = row
		}
		for _, record := range snapshot.Metadata {
			if record.NameRule == NameRuleExact {
				exact[record.ModelName] = record
			}
		}
		for _, vendor := range snapshot.Vendors {
			vendors[vendor.Id] = vendor
		}
		mapping := map[string]string{}
		if channel.ModelMapping != nil && strings.TrimSpace(*channel.ModelMapping) != "" {
			if err := common.UnmarshalJsonStr(*channel.ModelMapping, &mapping); err != nil {
				return err
			}
		}
		seen, finalNames := map[string]bool{}, map[string]bool{}
		changed := map[string]string{}
		for _, change := range request.Changes {
			row, exists := rows[change.SourceName]
			if !exists || seen[change.SourceName] {
				return errors.New("Selected channel models changed. Reload before saving.")
			}
			seen[change.SourceName] = true
			name := change.ModelName
			if name == "" || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > 128 || strings.ContainsFunc(name, func(r rune) bool { return r == ',' || unicode.IsSpace(r) || unicode.IsControl(r) }) {
				return errors.New("Enter a model name of up to 128 characters without commas or whitespace.")
			}
			if finalNames[name] {
				return errors.New("Duplicate customer model name")
			}
			finalNames[name], changed[change.SourceName] = true, name
			if name != change.SourceName {
				if _, exists := rows[name]; exists {
					return errors.New("Customer model name is already used by this channel")
				}
				if _, exists := mapping[name]; exists {
					return errors.New("Customer model name already exists in the channel mapping")
				}
				// Retain the old redirect key for other chains. Only the exposed name
				// changes; other models' upstream targets are never rewritten.
				mapping[name] = change.SourceName
			}
			if change.VendorID != nil || name != change.SourceName {
				record, exists := exact[name]
				if !exists {
					if row.Metadata != nil {
						record = *row.Metadata
					}
					record.Id, record.ModelName, record.NameRule = 0, name, NameRuleExact
					record.CreatedTime = common.GetTimestamp()
				}
				if change.VendorID != nil {
					record.VendorID = *change.VendorID
				}
				if record.VendorID != 0 {
					vendor, valid := vendors[record.VendorID]
					if !valid {
						return errors.New("Select a saved vendor.")
					}
					// Explicit selection materializes a preset inside this same
					// transaction. Every model in the batch shares one saved ID.
					if record.VendorID < 0 {
						presetID := record.VendorID
						if vendor.Id < 0 {
							vendor.Id = 0
							if err := validateVendorMetadata(tx, &vendor); err != nil {
								return err
							}
							vendor.CreatedTime, vendor.UpdatedTime = common.GetTimestamp(), common.GetTimestamp()
							if err := tx.Create(&vendor).Error; err != nil {
								return err
							}
							vendors[presetID] = vendor
						}
						record.VendorID = vendor.Id
					}
				}
				// Empty model icon activates the existing vendor-icon fallback. It also
				// follows future vendor icon changes instead of copying today's icon.
				record.Icon, record.UpdatedTime = "", common.GetTimestamp()
				if !exists {
					status, syncOfficial := record.Status, record.SyncOfficial
					status, syncOfficial = 0, 0
					if err := tx.Create(&record).Error; err != nil {
						return err
					}
					record.Status, record.SyncOfficial = status, syncOfficial
					if err := tx.Model(&Model{}).Where("id = ?", record.Id).Updates(map[string]any{"status": status, "sync_official": syncOfficial}).Error; err != nil {
						return err
					}
				} else if err := tx.Model(&Model{}).Where("id = ?", record.Id).Updates(map[string]any{"vendor_id": record.VendorID, "icon": "", "updated_time": record.UpdatedTime}).Error; err != nil {
					return err
				}
			}
			if change.Pricing != nil {
				previous := modelPricingValues(values, name)
				if change.ExpectedPricingVersion == "" || change.ExpectedPricingVersion != ModelPricingVersion(previous) {
					return ErrModelPricingConflict
				}
				if err := validateModelPricing(name, *change.Pricing, previous); err != nil {
					return err
				}
				replaceModelPricing(values, name, *change.Pricing)
			}
		}
		names := make([]string, 0, len(snapshot.Rows))
		for _, row := range snapshot.Rows {
			name := row.SourceName
			if replacement, exists := changed[name]; exists {
				name = replacement
			}
			upstream, err := channelBatchUpstream(name, mapping)
			if err != nil {
				return err
			}
			if upstream != row.UpstreamName {
				return errors.New("Renaming would change another model mapping. Choose a different customer name.")
			}
			names = append(names, name)
		}
		encoded, err := common.Marshal(mapping)
		if err != nil {
			return err
		}
		channel.Models, channel.ModelMapping = strings.Join(names, ","), common.GetPointer(string(encoded))
		if err := tx.Model(&Channel{}).Where("id = ?", id).Updates(map[string]any{"models": channel.Models, "model_mapping": channel.ModelMapping}).Error; err != nil {
			return err
		}
		return channel.UpdateAbilities(tx)
	})
	if err != nil {
		return err
	}
	InitChannelCache()
	RefreshPricing()
	return nil
}
