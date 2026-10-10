package resourcegrant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Grant is written only by the resource's owning service.
type Grant struct {
	ID                          string     `gorm:"type:uuid;primaryKey" json:"id"`
	ResourceType                string     `json:"resource_type"`
	ResourceID                  string     `gorm:"type:uuid" json:"resource_id"`
	BotID                       string     `gorm:"type:uuid" json:"bot_id"`
	ConvID                      string     `gorm:"type:uuid" json:"conv_id"`
	BindingID                   string     `gorm:"type:uuid" json:"binding_id"`
	ResourceBindingID           *string    `gorm:"type:uuid" json:"-"`
	OwnerID                     string     `gorm:"type:uuid" json:"owner_id"`
	SubjectID                   string     `gorm:"type:uuid" json:"subject_id"`
	SubjectMemberID             string     `gorm:"type:uuid" json:"-"`
	Scope                       string     `json:"scope"`
	CanRead                     bool       `json:"can_read"`
	CanShare                    bool       `json:"can_share"`
	ExpiresAt                   time.Time  `json:"expires_at"`
	RevokedAt                   *time.Time `json:"revoked_at,omitempty"`
	BotAuthorizationVersion     int64      `json:"-"`
	BindingAuthorizationVersion int64      `json:"-"`
	CreatedAt                   time.Time  `json:"created_at"`
}
type Access struct {
	BotID, ConvID, BindingID, RequesterID, Visibility string
	GrantIDs                                          []string
}
type Request struct {
	Op           string `json:"op"`
	BotID        string `json:"bot_id"`
	ConvID       string `json:"conv_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Scope        string `json:"scope"`
	CanRead      bool   `json:"can_read"`
	CanShare     bool   `json:"can_share"`
	Generation   int64  `json:"generation"`
	InputVersion int64  `json:"input_version"`
	ExpiresAt    int64  `json:"expires_at"`
	GrantID      string `json:"grant_id"`
	RunID        string `json:"run_id"`
	Query        string `json:"query"`
}

func Table(kind string) string {
	if kind == "file" {
		return "file.resource_grants"
	}
	return "knowledge.resource_grants"
}
func denial() error {
	return status.Error(codes.PermissionDenied, "resource authorization unavailable")
}
func Context(ctx context.Context, a Access) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("bot-id", a.BotID, "conv-id", a.ConvID, "binding-id", a.BindingID, "requester-id", a.RequesterID, "visibility", a.Visibility, "resource-grant-ids", strings.Join(a.GrantIDs, ",")))
}
func Incoming(ctx context.Context) (Access, bool) {
	md, _ := metadata.FromIncomingContext(ctx)
	get := func(k string) string {
		v := md.Get(k)
		if len(v) == 1 {
			return v[0]
		}
		return ""
	}
	a := Access{BotID: get("bot-id"), ConvID: get("conv-id"), BindingID: get("binding-id"), RequesterID: get("requester-id"), Visibility: get("visibility"), GrantIDs: strings.Split(get("resource-grant-ids"), ",")}
	return a, a.BotID != ""
}
func Owner(tx *gorm.DB, kind, id string) (string, error) {
	var row struct {
		OwnerID    *string
		UploaderID string
		Status     string
		OwnerType  string
	}
	if kind == "file" {
		if err := tx.Table("file.files").Where("id = ?", id).Take(&row).Error; err != nil {
			return "", denial()
		}
		return row.UploaderID, nil
	}
	if err := tx.Table("knowledge.knowledge_bases").Where("id = ? AND status = 'active'", id).Take(&row).Error; err != nil {
		return "", denial()
	}
	if row.OwnerID == nil {
		return "", denial()
	}
	return *row.OwnerID, nil
}

// Check revalidates every source, member incarnation and participation epoch.
// lock=true supplies the grant commit barrier to authoritative Run/output writes.
func Check(tx *gorm.DB, a Access, kind, id string, sharing, lock bool) (*Grant, error) {
	for _, value := range append([]string{a.BotID, a.ConvID, a.BindingID, a.RequesterID, id}, a.GrantIDs...) {
		if identity.Validate(value) != nil {
			return nil, denial()
		}
	}
	if !slices.Contains([]string{"file", "knowledge"}, kind) || len(a.GrantIDs) == 0 {
		return nil, denial()
	}
	var binding struct {
		ID                   string
		AuthorizationVersion int64
		BotSettings          map[string]any `gorm:"serializer:json"`
	}
	q := tx.Table("messaging.conv_bots").Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", a.BindingID, a.BotID, a.ConvID)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if q.Take(&binding).Error != nil {
		return nil, denial()
	}
	if enabled, ok := binding.BotSettings["enabled"]; ok && enabled != true {
		return nil, denial()
	}
	var bot struct {
		AuthorizationVersion int64
		EnableKnowledge      bool
	}
	if tx.Table("bot.bots").Where("id = ? AND status = 'active'", a.BotID).Take(&bot).Error != nil {
		return nil, denial()
	}
	if kind == "knowledge" && !bot.EnableKnowledge {
		return nil, denial()
	}
	var grants []Grant
	q = tx.Table(Table(kind)).Where("id IN ? AND resource_id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ? AND can_read = true AND revoked_at IS NULL AND expires_at > ?", a.GrantIDs, id, a.BotID, a.ConvID, a.BindingID, time.Now()).Order("id ASC")
	if lock {
		q = q.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := q.Find(&grants).Error; err != nil {
		return nil, err
	}
	source := tx
	if lock {
		source = source.Clauses(clause.Locking{Strength: "SHARE"})
	}
	owner, err := Owner(source, kind, id)
	if err != nil {
		return nil, err
	}
	for i := range grants {
		g := &grants[i]
		if g.OwnerID != owner || g.BindingAuthorizationVersion != binding.AuthorizationVersion || g.BotAuthorizationVersion != bot.AuthorizationVersion {
			continue
		}
		if kind == "knowledge" {
			if g.ResourceBindingID == nil {
				continue
			}
			var linked struct{ ID string }
			q := tx.Table("knowledge.knowledge_bindings").Where("id = ? AND kb_id = ? AND ((target_type = 'bot' AND target_id = ?) OR (target_type = 'conv' AND target_id = ?))", *g.ResourceBindingID, id, a.BotID, a.ConvID)
			if lock {
				q = q.Clauses(clause.Locking{Strength: "SHARE"})
			}
			if q.Take(&linked).Error != nil {
				continue
			}
		}
		var member struct{ ID string }
		if tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user' AND id = ?", a.ConvID, g.SubjectID, g.SubjectMemberID).Take(&member).Error != nil {
			continue
		}
		if g.Scope == "personal" && (a.Visibility != "personal" || g.SubjectID != a.RequesterID || sharing) {
			continue
		}
		if sharing && !g.CanShare {
			continue
		}
		return g, nil
	}
	return nil, denial()
}
func Manage(ctx context.Context, db *gorm.DB, user, kind string, body []byte) ([]byte, error) {
	var req Request
	if json.Unmarshal(body, &req) != nil || identity.Validate(user) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid resource authorization")
	}
	var result any
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		switch req.Op {
		case "create":
			if req.ResourceType != kind || identity.Validate(req.ResourceID) != nil || identity.Validate(req.BotID) != nil || identity.Validate(req.ConvID) != nil || !slices.Contains([]string{"conversation", "personal"}, req.Scope) || !req.CanRead || req.ExpiresAt <= time.Now().Unix() {
				return status.Error(codes.InvalidArgument, "invalid resource grant")
			}
			var conv struct{ ID string }
			if tx.Table("messaging.conversations").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", req.ConvID).Take(&conv).Error != nil {
				return denial()
			}
			owner, err := Owner(tx, kind, req.ResourceID)
			if err != nil || owner != user {
				return denial()
			}
			var binding struct {
				ID                   string
				AuthorizationVersion int64
			}
			if tx.Table("messaging.conv_bots").Clauses(clause.Locking{Strength: "SHARE"}).Where("bot_id = ? AND conv_id = ? AND revoked_at IS NULL", req.BotID, req.ConvID).Take(&binding).Error != nil {
				return denial()
			}
			var bot struct{ AuthorizationVersion int64 }
			if tx.Table("bot.bots").Where("id = ? AND status = 'active'", req.BotID).Take(&bot).Error != nil {
				return denial()
			}
			var member struct{ ID string }
			if tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", req.ConvID, user).Take(&member).Error != nil {
				return denial()
			}
			id, err := identity.New()
			if err != nil {
				return err
			}
			grant := Grant{ID: id, ResourceType: kind, ResourceID: req.ResourceID, BotID: req.BotID, ConvID: req.ConvID, BindingID: binding.ID, OwnerID: owner, SubjectID: user, SubjectMemberID: member.ID, Scope: req.Scope, CanRead: req.CanRead, CanShare: req.CanShare, ExpiresAt: time.Unix(req.ExpiresAt, 0), BotAuthorizationVersion: bot.AuthorizationVersion, BindingAuthorizationVersion: binding.AuthorizationVersion, CreatedAt: time.Now()}
			if kind == "knowledge" {
				var linked struct{ ID string }
				if tx.Table("knowledge.knowledge_bindings").Clauses(clause.Locking{Strength: "SHARE"}).Where("kb_id = ? AND ((target_type = 'bot' AND target_id = ?) OR (target_type = 'conv' AND target_id = ?))", req.ResourceID, req.BotID, req.ConvID).Order("id ASC").Take(&linked).Error != nil {
					return denial()
				}
				grant.ResourceBindingID = &linked.ID
			}
			if err := tx.Table(Table(kind)).Create(&grant).Error; err != nil {
				return err
			}
			result = grant
			return nil
		case "revoke":
			var prior Grant
			if tx.Table(Table(kind)).Where("id = ? AND owner_id = ?", req.GrantID, user).Take(&prior).Error != nil {
				return denial()
			}
			var conv struct{ ID string }
			if err := tx.Table("messaging.conversations").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", prior.ConvID).Take(&conv).Error; err != nil {
				return err
			}
			if err := tx.Table(Table(kind)).Where("id = ? AND owner_id = ?", prior.ID, user).Update("revoked_at", time.Now()).Error; err != nil {
				return err
			}
			result = map[string]any{"revoked": true, "grant_id": prior.ID}
			return nil
		case "list":
			grants := []Grant{}
			q := tx.Table(Table(kind)).Where("owner_id = ?", user)
			if req.BotID != "" {
				q = q.Where("bot_id = ?", req.BotID)
			}
			if req.ConvID != "" {
				q = q.Where("conv_id = ?", req.ConvID)
			}
			if err := q.Order("created_at DESC").Find(&grants).Error; err != nil {
				return err
			}
			result = grants
			return nil
		default:
			return status.Error(codes.InvalidArgument, "unsupported resource operation")
		}
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func CheckIDs(tx *gorm.DB, a Access, sharing, lock bool) error {
	ids := slices.Clone(a.GrantIDs)
	slices.Sort(ids)
	for _, id := range ids {
		if identity.Validate(id) != nil {
			return denial()
		}
		var g Grant
		found := false
		for _, kind := range []string{"file", "knowledge"} {
			err := tx.Table(Table(kind)).Where("id = ?", id).Take(&g).Error
			if err == nil {
				exact := a
				exact.GrantIDs = []string{id}
				if _, err := Check(tx, exact, kind, g.ResourceID, sharing, lock); err != nil {
					return err
				}
				found = true
				break
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if !found {
			return denial()
		}
	}
	return nil
}

func ForViewer(tx *gorm.DB, user, kind, id string) error {
	var grants []Grant
	if err := tx.Table(Table(kind)).Where("resource_id = ? AND scope = 'conversation' AND can_read = true AND can_share = true AND revoked_at IS NULL AND expires_at > ?", id, time.Now()).Find(&grants).Error; err != nil {
		return err
	}
	for _, g := range grants {
		var member struct{ ID string }
		if tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", g.ConvID, user).Take(&member).Error != nil {
			continue
		}
		if _, err := Check(tx, Access{BotID: g.BotID, ConvID: g.ConvID, BindingID: g.BindingID, RequesterID: user, Visibility: "conversation", GrantIDs: []string{g.ID}}, kind, id, true, false); err == nil {
			return nil
		}
	}
	return denial()
}
