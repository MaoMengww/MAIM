package resourcegrant

import (
	"encoding/json"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ToolPolicy is manager-owned configuration. Remote annotations and model output
// never establish risk or sharing authority.
type ToolPolicy struct {
	Risk            string   `json:"risk"`
	TargetParameter string   `json:"target_parameter,omitempty"`
	CanShare        bool     `json:"can_share"`
	ConversationIDs []string `json:"conversation_ids,omitempty"`
}

type ToolRef struct {
	ServerID        string   `json:"server_id"`
	Transport       string   `json:"transport"`
	URL             string   `json:"url"`
	Name            string   `json:"name"`
	DefinitionHash  string   `json:"definition_hash"`
	Risk            string   `json:"risk"`
	TargetParameter string   `json:"target_parameter,omitempty"`
	CanShare        bool     `json:"can_share"`
	ConversationIDs []string `json:"conversation_ids,omitempty"`
}

func TrustedToolPolicy(ownerType string, policies map[string]ToolPolicy, name string) (ToolPolicy, bool) {
	if policy, ok := policies[name]; ok {
		if slices.Contains([]string{"read", "draft", "send", "modify", "delete", "pay"}, policy.Risk) {
			return policy, true
		}
		return ToolPolicy{}, false
	}
	// Only trusted platform resources retain the pre-existing public network list.
	if ownerType == "platform" && slices.Contains([]string{"fetch", "fetch_url", "read_url", "web_search", "search", "wikipedia_search"}, name) {
		return ToolPolicy{Risk: "read", CanShare: true}, true
	}
	return ToolPolicy{}, false
}

// CheckToolPermissions authorizes the tools involved in a new invocation against
// current manager configuration and assignment. It performs no MCP network I/O.
func CheckToolPermissions(tx *gorm.DB, a Access, refs []ToolRef, lock bool) error {
	for _, ref := range refs {
		if ref.ServerID == "runtime" {
			if !slices.Contains([]string{"ask_for_input", "delegate_run", "delegation_result", "search_knowledge", "list_knowledge_bases", "list_knowledge_documents", "read_knowledge_document", "read_conversation_history", "load_skill", "read_skill_resource", "run_skill_script"}, ref.Name) {
				return toolDenial()
			}
			if slices.Contains([]string{"load_skill", "read_skill_resource", "run_skill_script"}, ref.Name) && ref.Risk != "read" {
				return toolDenial()
			}
			if slices.Contains([]string{"search_knowledge", "list_knowledge_bases", "list_knowledge_documents", "read_knowledge_document"}, ref.Name) {
				var bot struct{ EnableKnowledge bool }
				q := tx.Table("bot.bots").Select("enable_knowledge").Where("id = ? AND status = 'active'", a.BotID)
				if lock {
					q = q.Clauses(clause.Locking{Strength: "SHARE"})
				}
				if err := q.Take(&bot).Error; err != nil {
					return err
				}
				if !bot.EnableKnowledge || ref.Risk != "read" {
					return toolDenial()
				}
			}
			if ref.Name == "read_conversation_history" && (ref.Risk != "read" || !ref.CanShare) {
				return toolDenial()
			}
			continue
		}
		if ref.ServerID == "builtin" {
			if ref.Name != "web_search" || ref.Risk != "read" {
				return toolDenial()
			}
			continue
		}
		var server struct {
			ID, OwnerType, Transport, URL string
			AdvancedConfig                json.RawMessage
		}
		q := tx.Table("bot.mcp_servers").Where("id = ? AND enabled = true", ref.ServerID)
		if lock {
			q = q.Clauses(clause.Locking{Strength: "SHARE"})
		}
		if err := q.Take(&server).Error; err != nil {
			return toolDenial()
		}
		var assignment struct{ ID string }
		q = tx.Table("bot.bot_mcp_servers").Where("bot_id = ? AND mcp_server_id = ? AND enabled = true", a.BotID, ref.ServerID)
		if lock {
			q = q.Clauses(clause.Locking{Strength: "SHARE"})
		}
		if err := q.Take(&assignment).Error; err != nil {
			return toolDenial()
		}
		var advanced struct {
			ToolPolicies map[string]ToolPolicy `json:"tool_policies"`
		}
		if len(server.AdvancedConfig) > 0 && string(server.AdvancedConfig) != "null" {
			if json.Unmarshal(server.AdvancedConfig, &advanced) != nil {
				return toolDenial()
			}
		}
		policy, known := TrustedToolPolicy(server.OwnerType, advanced.ToolPolicies, ref.Name)
		if !known || policy.Risk != ref.Risk || policy.TargetParameter != ref.TargetParameter || policy.CanShare != ref.CanShare || !slices.Equal(policy.ConversationIDs, ref.ConversationIDs) || server.Transport != ref.Transport || server.URL != ref.URL {
			return toolDenial()
		}
		if a.Visibility != "personal" {
			if !policy.CanShare {
				return toolDenial()
			}
			if _, explicit := advanced.ToolPolicies[ref.Name]; explicit && !slices.Contains(policy.ConversationIDs, a.ConvID) {
				return toolDenial()
			}
		}
	}
	return nil
}
func toolDenial() error {
	return status.Error(codes.PermissionDenied, "current tool capability or sharing scope unavailable")
}
