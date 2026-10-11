package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/botpolicy"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// ValidateRuleInput checks live trigger authority before accepting or replaying a
// submission. It is not run authorization: disabling a rule never revokes work
// already accepted under its frozen configuration.
func (s *Service) ValidateRuleInput(ctx context.Context, input Input, binding *model.ConvBot) error {
	if input.Origin == nil || input.Origin.Kind != "rule" || input.RequesterID != "" || input.Activity == nil || input.SourceMessageID != nil || input.ParentRunID != nil || input.Visibility == "personal" {
		return status.Error(codes.InvalidArgument, "invalid rule input")
	}
	activity := input.Activity
	if identity.Validate(activity.ID) != nil || identity.Validate(activity.ActorID) != nil || activity.ConvID != input.ConvID || activity.BindingID != binding.ID || input.BindingID != binding.ID || input.Origin.ActivityID != activity.ID || activity.Seq <= binding.StartSeq || activity.Seq > sequence.Max || !botpolicy.Subscribed(binding.BotSettings, activity.Type, activity.Seq) {
		return status.Error(codes.PermissionDenied, "activity is outside current subscription")
	}
	rules, err := botpolicy.Rules(binding.BotSettings)
	if err != nil {
		return status.Error(codes.PermissionDenied, "rule authorization is unavailable")
	}
	businessType, err := activityBusinessType(activity)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.ID != input.Origin.RuleID || rule.Version != input.Origin.RuleVersion || rule.AuthorizationUserID != input.Origin.AuthorizationUserID || rule.AuthorizationMemberID != input.Origin.AuthorizationMemberID || !botpolicy.Matches(rule, activity.Type, activity.Text, businessType, activity.Seq) {
			continue
		}
		member, role, err := repo.RunMember(s.svc.DB.WithContext(ctx), input.ConvID, rule.AuthorizationUserID)
		if err != nil {
			return err
		}
		if member != rule.AuthorizationMemberID || role > 2 {
			return status.Error(codes.PermissionDenied, "current rule administrator required")
		}
		if input.SubmissionKey != ruleSubmissionKey(rule, activity) || input.Message != activityInput(activity) {
			return status.Error(codes.InvalidArgument, "rule submission does not match activity")
		}
		return s.validateActivity(ctx, input.BotID, binding, activity)
	}
	return status.Error(codes.PermissionDenied, "rule is disabled, replaced or no longer matches")
}

func ruleSubmissionKey(rule botpolicy.Rule, activity *model.ActivityEnvelope) string {
	return "rule:" + rule.ID + ":" + strconv.FormatInt(rule.Version, 10) + ":" + activity.ID
}

func activityInput(activity *model.ActivityEnvelope) string {
	if activity.Text != "" {
		return activity.Text
	}
	return activity.Type
}

func activityBusinessType(activity *model.ActivityEnvelope) (string, error) {
	if activity.Type != botpolicy.BusinessActivity {
		return "", nil
	}
	var data struct {
		BusinessType string `json:"business_type"`
	}
	if json.Unmarshal(activity.Data, &data) != nil || data.BusinessType == "" {
		return "", status.Error(codes.InvalidArgument, "business activity type required")
	}
	return data.BusinessType, nil
}

// validateActivity resolves messages through the existing Bot source boundary;
// domain activity must exactly equal the committed message-domain outbox fact.
// Neither a Kafka payload nor executor-supplied provenance is itself authority.
func (s *Service) validateActivity(ctx context.Context, botID string, binding *model.ConvBot, activity *model.ActivityEnvelope) error {
	if !botpolicy.Supported(activity.Type) || identity.Validate(activity.ID) != nil || identity.Validate(activity.ActorID) != nil || activity.ConvID != binding.ConvID || activity.BindingID != binding.ID || activity.Seq <= binding.StartSeq || !botpolicy.Subscribed(binding.BotSettings, activity.Type, activity.Seq) {
		return status.Error(codes.PermissionDenied, "activity is not subscribed or visible")
	}
	if activity.Type == botpolicy.MessageCreated {
		if identity.Validate(activity.MessageID) != nil || activity.ID != activity.MessageID {
			return status.Error(codes.InvalidArgument, "invalid message activity identity")
		}
		input := Input{BotID: botID, ConvID: binding.ConvID, BindingID: binding.ID, RequesterID: activity.ActorID}
		source, err := s.source(ctx, input, activity.MessageID, true)
		if err != nil {
			return err
		}
		if source.SenderType != "user" || source.GetSenderId() != activity.ActorID || source.Seq != activity.Seq || source.GetText() == nil || source.EditCount != 0 {
			return status.Error(codes.PermissionDenied, "activity is not an unmodified current human source")
		}
		if source.GetText().Text != activity.Text {
			return status.Error(codes.AlreadyExists, "activity identity conflicts with current source input")
		}
		return nil
	}
	if activity.MessageID != "" {
		return status.Error(codes.InvalidArgument, "domain activity cannot claim a message source")
	}
	var fact struct{ Payload json.RawMessage }
	err := s.svc.DB.WithContext(ctx).Table("messaging.outbox_events").Select("payload").Where("id = ? AND conv_id = ?", activity.ID, binding.ConvID).Take(&fact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.PermissionDenied, "activity has no authoritative domain source")
	}
	if err != nil {
		return err
	}
	var stored struct {
		Kind     string                 `json:"kind"`
		Activity model.ActivityEnvelope `json:"activity"`
	}
	if json.Unmarshal(fact.Payload, &stored) != nil || stored.Kind != "bot.activity" {
		return status.Error(codes.PermissionDenied, "invalid activity provenance")
	}
	stored.Activity.BindingID = binding.ID
	left, err := activityFingerprint(&stored.Activity)
	if err != nil {
		return err
	}
	right, err := activityFingerprint(activity)
	if err != nil {
		return err
	}
	if left != right {
		return status.Error(codes.AlreadyExists, "activity identity conflicts with committed input")
	}
	return nil
}

func activityFingerprint(activity *model.ActivityEnvelope) (string, error) {
	raw, err := json.Marshal(activity)
	if err != nil {
		return "", err
	}
	var semantic any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&semantic); err != nil {
		return "", err
	}
	return hash(semantic)
}

// TriggerRules uses the same immutable activity envelope for builtin and external
// executors and never fabricates a requester from its actor or administrator.
func (s *Service) TriggerRules(ctx context.Context, botID, bindingID string, activity *model.ActivityEnvelope) error {
	if activity == nil {
		return status.Error(codes.InvalidArgument, "activity required")
	}
	bot, binding, err := s.svc.BotRepo.FindByIDWithConvBot(ctx, botID, activity.ConvID)
	if err != nil {
		return err
	}
	if binding.ID != bindingID || bot.Status != "active" {
		return status.Error(codes.PermissionDenied, "rule binding is unavailable")
	}
	rules, err := botpolicy.Rules(binding.BotSettings)
	if err != nil {
		return status.Error(codes.PermissionDenied, "rule settings are invalid")
	}
	businessType, err := activityBusinessType(activity)
	if err != nil {
		return err
	}
	var rejected error
	for _, rule := range rules {
		if !botpolicy.Matches(rule, activity.Type, activity.Text, businessType, activity.Seq) {
			continue
		}
		input := Input{BotID: botID, ConvID: binding.ConvID, BindingID: binding.ID, SubmissionKey: ruleSubmissionKey(rule, activity), Message: activityInput(activity), Activity: activity, Origin: &model.RunOrigin{Kind: "rule", RuleID: rule.ID, RuleVersion: rule.Version, AuthorizationUserID: rule.AuthorizationUserID, AuthorizationMemberID: rule.AuthorizationMemberID, ActivityID: activity.ID}}
		if _, err := s.Accept(ctx, input); err != nil {
			switch status.Code(err) {
			case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.InvalidArgument, codes.FailedPrecondition, codes.AlreadyExists:
				rejected = err
			default:
				return err
			}
		}
	}
	return rejected
}
