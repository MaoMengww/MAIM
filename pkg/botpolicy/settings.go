package botpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
)

const (
	MessageCreated   = "message.created"
	MemberJoined     = "member.joined"
	MemberLeft       = "member.left"
	BusinessActivity = "business.activity"
)

// Rule is the stored response contract. Authorization and watermarks are stamped
// by the conversation administrator boundary, never supplied by an executor.
type Rule struct {
	ID                    string `json:"id"`
	Enabled               bool   `json:"enabled"`
	ActivityType          string `json:"activity_type"`
	MessageContains       string `json:"message_contains,omitempty"`
	BusinessType          string `json:"business_type,omitempty"`
	Version               int64  `json:"version"`
	AuthorizationUserID   string `json:"authorization_user_id"`
	AuthorizationMemberID string `json:"authorization_member_id"`
	StartSeq              int64  `json:"start_seq"`
}

type RuleInput struct {
	ID              string `json:"id"`
	Enabled         *bool  `json:"enabled"`
	ActivityType    string `json:"activity_type"`
	MessageContains string `json:"message_contains,omitempty"`
	BusinessType    string `json:"business_type,omitempty"`
}

func Supported(activity string) bool {
	return slices.Contains([]string{MessageCreated, MemberJoined, MemberLeft, BusinessActivity}, activity)
}

// PrepareSettings validates the untrusted settings object without accepting rule
// provenance. Unrelated existing binding settings remain on their existing seam.
func PrepareSettings(raw string) (map[string]any, error) {
	var settings map[string]any
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &settings) != nil || settings == nil {
		return nil, fmt.Errorf("bot settings must be an object")
	}
	for _, key := range []string{"origin", "provenance", "requester_id", "authorization_user_id", "authorization_member_id"} {
		if _, exists := settings[key]; exists {
			return nil, fmt.Errorf("settings cannot supply execution provenance")
		}
	}
	if enabled, exists := settings["enabled"]; exists {
		if _, ok := enabled.(bool); !ok {
			return nil, fmt.Errorf("enabled must be boolean")
		}
	}
	var subscriptions []string
	if value, exists := settings["activity_types"]; exists {
		rawTypes, ok := value.([]any)
		if !ok || len(rawTypes) > 4 {
			return nil, fmt.Errorf("invalid activity subscriptions")
		}
		for _, value := range rawTypes {
			activity, ok := value.(string)
			if !ok || !Supported(activity) || slices.Contains(subscriptions, activity) {
				return nil, fmt.Errorf("unsupported or duplicate activity subscription")
			}
			subscriptions = append(subscriptions, activity)
		}
	}
	if value, exists := settings["response_rules"]; exists {
		rawRules, ok := value.([]any)
		if !ok || len(rawRules) > 32 {
			return nil, fmt.Errorf("invalid response rules")
		}
		ids := make(map[string]bool, len(rawRules))
		for _, value := range rawRules {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			var rule RuleInput
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&rule) != nil || decoder.Decode(new(any)) != io.EOF || rule.Enabled == nil || identity.Validate(rule.ID) != nil || ids[rule.ID] || !Supported(rule.ActivityType) {
				return nil, fmt.Errorf("invalid rule identity, fields or activity type")
			}
			if (rule.MessageContains != "" && (rule.ActivityType != MessageCreated || len(rule.MessageContains) > 1024 || strings.TrimSpace(rule.MessageContains) == "")) || (rule.BusinessType != "" && (rule.ActivityType != BusinessActivity || len(rule.BusinessType) > 128 || strings.TrimSpace(rule.BusinessType) == "")) {
				return nil, fmt.Errorf("invalid rule condition")
			}
			if *rule.Enabled && !slices.Contains(subscriptions, rule.ActivityType) {
				return nil, fmt.Errorf("enabled rule requires its activity subscription")
			}
			ids[rule.ID] = true
		}
	}
	return settings, nil
}

// StampSettings is called while the conversation lock is held. Every save
// advances the version and starts strictly after the current sequence, including
// remove/re-add, so replay cannot acquire a newly granted trigger authorization.
func StampSettings(settings, previous map[string]any, userID, memberID string, startSeq int64) error {
	if identity.Validate(userID) != nil || identity.Validate(memberID) != nil || startSeq < 0 || startSeq > sequence.Max {
		return fmt.Errorf("invalid rule authorization")
	}
	previousVersion, _ := integer(previous["response_rules_version"])
	if previousVersion >= sequence.Max {
		return fmt.Errorf("response rule version exhausted")
	}
	version := previousVersion + 1
	settings["activity_start_seq"] = startSeq
	settings["response_rules_version"] = version
	var rules []Rule
	if value, exists := settings["response_rules"]; exists {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var inputs []RuleInput
		if err := json.Unmarshal(encoded, &inputs); err != nil {
			return err
		}
		rules = make([]Rule, 0, len(inputs))
		for _, input := range inputs {
			rules = append(rules, Rule{ID: input.ID, Enabled: *input.Enabled, ActivityType: input.ActivityType, MessageContains: input.MessageContains, BusinessType: input.BusinessType, Version: version, AuthorizationUserID: userID, AuthorizationMemberID: memberID, StartSeq: startSeq})
		}
		settings["response_rules"] = rules
	}
	return nil
}

func Rules(settings map[string]any) ([]Rule, error) {
	if _, exists := settings["response_rules"]; !exists {
		return nil, nil
	}
	raw, err := json.Marshal(settings["response_rules"])
	if err != nil {
		return nil, err
	}
	var rules []Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if identity.Validate(rule.ID) != nil || identity.Validate(rule.AuthorizationUserID) != nil || identity.Validate(rule.AuthorizationMemberID) != nil || !Supported(rule.ActivityType) || rule.Version <= 0 || rule.Version > sequence.Max || rule.StartSeq < 0 || rule.StartSeq > sequence.Max {
			return nil, fmt.Errorf("invalid stored rule authorization")
		}
	}
	return rules, nil
}

func Subscribed(settings map[string]any, activity string, seq int64) bool {
	if enabled, exists := settings["enabled"]; exists && enabled != true {
		return false
	}
	start, ok := integer(settings["activity_start_seq"])
	if !ok || seq <= start {
		return false
	}
	encoded, err := json.Marshal(settings["activity_types"])
	if err != nil {
		return false
	}
	var types []string
	return json.Unmarshal(encoded, &types) == nil && Supported(activity) && slices.Contains(types, activity)
}

func Matches(rule Rule, activity, text, businessType string, seq int64) bool {
	return rule.Enabled && rule.ActivityType == activity && seq > rule.StartSeq && (rule.MessageContains == "" || strings.Contains(text, rule.MessageContains)) && (rule.BusinessType == "" || rule.BusinessType == businessType)
}

func integer(value any) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, value >= 0 && value <= sequence.Max
	case int:
		return int64(value), value >= 0 && int64(value) <= sequence.Max
	case float64:
		if value < 0 || value > float64(sequence.Max) {
			return 0, false
		}
		return int64(value), value == float64(int64(value))
	default:
		return 0, false
	}
}
