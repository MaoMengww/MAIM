package main

import (
	"errors"
	"fmt"
	"net/http"
)

type relationshipRequest struct {
	ID           entityID `json:"request_id"`
	From         entityID `json:"from_user_id"`
	To           entityID `json:"to_user_id"`
	Message      string   `json:"message"`
	Status       string   `json:"status"`
	FromUsername string   `json:"from_username"`
}

type relationshipFriend struct {
	ID        entityID  `json:"user_id"`
	Username  string    `json:"username"`
	Remark    string    `json:"remark"`
	GroupID   *entityID `json:"group_id"`
	GroupName string    `json:"group_name"`
}

type relationshipGroup struct {
	ID          entityID `json:"id"`
	Name        string   `json:"name"`
	FriendCount int      `json:"friend_count"`
}

// relationships observes only the public gateway HTTP seam. Fresh accounts make
// every run independent; the caller identity always comes from its login token.
func (d *driver) relationships() error {
	suffix, err := randomSuffix()
	if err != nil {
		return fmt.Errorf("relationships.accounts: %w", err)
	}
	a, err := d.register("rel_a", suffix)
	if err != nil {
		return fmt.Errorf("relationships.accounts.A: %w", err)
	}
	b, err := d.register("rel_b", suffix)
	if err != nil {
		return fmt.Errorf("relationships.accounts.B: %w", err)
	}
	if a.id == b.id {
		return errors.New("relationships.accounts: 独立账号返回相同 user_id")
	}
	if err := d.relationshipFriends("initial", a, b, nil); err != nil {
		return err
	}
	if err := d.relationshipFriends("initial", b, a, nil); err != nil {
		return err
	}

	accepted, err := d.relationshipSend("send-for-accept", a, b, suffix+"_accept")
	if err != nil {
		return err
	}
	acceptPath := "/friends/requests/" + accepted.ID.String() + "/accept"
	if err := d.relationshipReject("accept-only-recipient", http.MethodPost, acceptPath, a, nil); err != nil {
		return err
	}
	if err := d.relationshipRequestState("unauthorized-accept-unchanged", a, b, accepted, "pending"); err != nil {
		return err
	}
	if err := d.relationshipCall("accept", http.MethodPost, acceptPath, b, nil, nil); err != nil {
		return err
	}
	if err := d.relationshipRequestState("accepted-not-pending", a, b, accepted, "accepted"); err != nil {
		return err
	}
	friendA := relationshipFriend{ID: b.id, Username: b.username}
	friendB := relationshipFriend{ID: a.id, Username: a.username}
	if err := d.relationshipFriends("accepted.A", a, b, &friendA); err != nil {
		return err
	}
	if err := d.relationshipFriends("accepted.B", b, a, &friendB); err != nil {
		return err
	}

	friendA.Remark = "备注_" + suffix
	if err := d.relationshipCall("set-remark", http.MethodPut, "/friends/"+b.id.String()+"/remark", a, map[string]string{"remark": friendA.Remark}, nil); err != nil {
		return err
	}
	if err := d.relationshipFriends("remark-visible.A", a, b, &friendA); err != nil {
		return err
	}
	if err := d.relationshipFriends("remark-private.B", b, a, &friendB); err != nil {
		return err
	}

	var created struct {
		ID entityID `json:"group_id"`
	}
	group := relationshipGroup{Name: "分组_" + suffix}
	if err := d.relationshipCall("create-group", http.MethodPost, "/friends/groups", a, map[string]string{"name": group.Name}, &created); err != nil {
		return err
	}
	if created.ID == "" {
		return errors.New("relationships.create-group: 没有有效 group_id")
	}
	group.ID = created.ID
	if err := d.relationshipGroups("group-created.A", a, group.ID, &group); err != nil {
		return err
	}
	if err := d.relationshipGroups("group-private.B", b, group.ID, nil); err != nil {
		return err
	}
	groupPath := "/friends/groups/" + group.ID.String()
	if err := d.relationshipReject("rename-only-owner", http.MethodPut, groupPath, b, map[string]string{"name": "非所有者"}); err != nil {
		return err
	}
	if err := d.relationshipGroups("unauthorized-rename-unchanged", a, group.ID, &group); err != nil {
		return err
	}
	group.Name = "改名_" + suffix
	if err := d.relationshipCall("rename-group", http.MethodPut, groupPath, a, map[string]string{"name": group.Name}, nil); err != nil {
		return err
	}
	if err := d.relationshipGroups("group-renamed", a, group.ID, &group); err != nil {
		return err
	}
	if err := d.relationshipCall("set-group", http.MethodPut, "/friends/"+b.id.String()+"/group", a, map[string]entityID{"group_id": group.ID}, nil); err != nil {
		return err
	}
	friendA.GroupID, friendA.GroupName = &group.ID, group.Name
	group.FriendCount = 1
	if err := d.relationshipFriends("group-assigned.A", a, b, &friendA); err != nil {
		return err
	}
	if err := d.relationshipGroups("group-member-count", a, group.ID, &group); err != nil {
		return err
	}
	if err := d.relationshipFriends("group-private.B", b, a, &friendB); err != nil {
		return err
	}
	if err := d.relationshipCall("delete-group", http.MethodDelete, groupPath, a, nil, nil); err != nil {
		return err
	}
	if err := d.relationshipGroups("group-deleted", a, group.ID, nil); err != nil {
		return err
	}
	friendA.GroupID, friendA.GroupName = nil, ""
	if err := d.relationshipFriends("deleted-group-unassigned", a, b, &friendA); err != nil {
		return err
	}
	if err := d.relationshipCall("delete-friend", http.MethodDelete, "/friends/"+b.id.String(), a, nil, nil); err != nil {
		return err
	}
	if err := d.relationshipFriends("deleted.A", a, b, nil); err != nil {
		return err
	}
	if err := d.relationshipFriends("deleted.B", b, a, nil); err != nil {
		return err
	}

	rejected, err := d.relationshipSend("send-for-reject", a, b, suffix+"_reject")
	if err != nil {
		return err
	}
	if rejected.ID == accepted.ID {
		return errors.New("relationships.send-for-reject: 新请求复用了旧 request_id")
	}
	if err := d.relationshipCall("reject", http.MethodPost, "/friends/requests/"+rejected.ID.String()+"/reject", b, nil, nil); err != nil {
		return err
	}
	if err := d.relationshipRequestState("rejected-not-pending", a, b, rejected, "rejected"); err != nil {
		return err
	}
	cancelled, err := d.relationshipSend("send-for-cancel", a, b, suffix+"_cancel")
	if err != nil {
		return err
	}
	if cancelled.ID == accepted.ID || cancelled.ID == rejected.ID {
		return errors.New("relationships.send-for-cancel: 新请求复用了旧 request_id")
	}
	cancelPath := "/friends/requests/" + cancelled.ID.String()
	if err := d.relationshipReject("cancel-only-sender", http.MethodDelete, cancelPath, b, nil); err != nil {
		return err
	}
	if err := d.relationshipRequestState("unauthorized-cancel-unchanged", a, b, cancelled, "pending"); err != nil {
		return err
	}
	if err := d.relationshipCall("cancel", http.MethodDelete, cancelPath, a, nil, nil); err != nil {
		return err
	}
	if err := d.relationshipRequestState("cancelled-not-pending", a, b, cancelled, "cancelled"); err != nil {
		return err
	}
	if err := d.relationshipFriends("reject-cancel-no-friends.A", a, b, nil); err != nil {
		return err
	}
	if err := d.relationshipFriends("reject-cancel-no-friends.B", b, a, nil); err != nil {
		return err
	}

	blockPath := "/friends/" + b.id.String() + "/block"
	if err := d.relationshipCall("block", http.MethodPost, blockPath, a, nil, nil); err != nil {
		return err
	}
	if err := d.relationshipBlacklist("blocked-visible.A", a, b, true); err != nil {
		return err
	}
	if err := d.relationshipBlacklist("blacklist-private.B", b, a, false); err != nil {
		return err
	}
	for _, pair := range [][2]account{{a, b}, {b, a}} {
		if err := d.relationshipReject("blocked-send."+pair[0].username, http.MethodPost, "/friends/requests", pair[0], map[string]any{"to_user_id": pair[1].id, "message": suffix + "_blocked"}); err != nil {
			return err
		}
		if err := d.relationshipPendingEmpty("blocked-no-pending."+pair[0].username, pair[0]); err != nil {
			return err
		}
	}
	if err := d.relationshipCall("unblock", http.MethodDelete, blockPath, a, nil, nil); err != nil {
		return err
	}
	if err := d.relationshipBlacklist("unblocked-absent", a, b, false); err != nil {
		return err
	}
	restored, err := d.relationshipSend("send-restored", a, b, suffix+"_restored")
	if err != nil {
		return err
	}
	if restored.ID == accepted.ID || restored.ID == rejected.ID || restored.ID == cancelled.ID {
		return errors.New("relationships.send-restored: 新请求复用了旧 request_id")
	}
	if err := d.relationshipCall("cancel-restored", http.MethodDelete, "/friends/requests/"+restored.ID.String(), a, nil, nil); err != nil {
		return err
	}
	return d.relationshipRequestState("restored-cleanup-not-pending", a, b, restored, "cancelled")
}

func (d *driver) relationshipCall(step, method, path string, caller account, input, output any) error {
	if err := d.request(method, path, caller.token, input, output); err != nil {
		return fmt.Errorf("relationships.%s: %w", step, err)
	}
	return nil
}

// relationshipReject asserts the operation did not succeed and left state unchanged.
// It deliberately does not separate a business rejection from an RPC failure: gateway
// friend handlers collapse both into the same envelope, so each negative step is paired
// with a positive call on the same RPC elsewhere in the scenario, which is what proves
// the endpoint is reachable.
func (d *driver) relationshipReject(step, method, path string, caller account, input any) error {
	err := d.request(method, path, caller.token, input, nil)
	var rejected *apiError
	if errors.As(err, &rejected) && rejected.code != 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("relationships.%s: 预期业务拒绝而非传输/协议失败: %w", step, err)
	}
	return fmt.Errorf("relationships.%s: 应拒绝的操作却成功", step)
}

func (d *driver) relationshipSend(step string, from, to account, message string) (relationshipRequest, error) {
	var sent struct {
		ID entityID `json:"request_id"`
	}
	if err := d.relationshipCall(step, http.MethodPost, "/friends/requests", from, map[string]any{"to_user_id": to.id, "message": message}, &sent); err != nil {
		return relationshipRequest{}, err
	}
	if sent.ID == "" {
		return relationshipRequest{}, fmt.Errorf("relationships.%s: 没有有效 request_id", step)
	}
	expected := relationshipRequest{ID: sent.ID, From: from.id, To: to.id, Message: message}
	return expected, d.relationshipRequestState(step+".pending", from, to, expected, "pending")
}

func (d *driver) relationshipRequestState(step string, from, to account, expected relationshipRequest, status string) error {
	var sent, pending struct {
		Requests []relationshipRequest `json:"requests"`
	}
	if err := d.relationshipCall(step+".sent", http.MethodGet, "/friends/requests/sent", from, nil, &sent); err != nil {
		return err
	}
	count := 0
	for _, item := range sent.Requests {
		if item.ID == expected.ID {
			count++
			if item.From != from.id || item.To != to.id || item.Message != expected.Message || item.Status != status {
				return fmt.Errorf("relationships.%s: 已发送请求身份、验证消息或状态不匹配（期望 %s）", step, status)
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("relationships.%s: 已发送列表应且仅应有一次 request_id=%s，实际 %d 次", step, expected.ID, count)
	}
	if err := d.relationshipCall(step+".received", http.MethodGet, "/friends/requests/pending", to, nil, &pending); err != nil {
		return err
	}
	if status != "pending" {
		if len(pending.Requests) != 0 {
			return fmt.Errorf("relationships.%s: 已处理请求仍出现在接收 pending 列表", step)
		}
		return nil
	}
	if len(pending.Requests) != 1 {
		return fmt.Errorf("relationships.%s: 接收 pending 列表应且仅应有一个请求，实际 %d 个", step, len(pending.Requests))
	}
	item := pending.Requests[0]
	if item.ID != expected.ID || item.From != from.id || item.To != to.id || item.Message != expected.Message || item.Status != "pending" || item.FromUsername != from.username {
		return fmt.Errorf("relationships.%s: 接收请求的 ID、双方身份、发送者资料、验证消息或 pending 状态不匹配", step)
	}
	return d.relationshipPendingEmpty(step+".sender-cannot-receive-own-request", from)
}

func (d *driver) relationshipPendingEmpty(step string, caller account) error {
	var result struct {
		Requests []relationshipRequest `json:"requests"`
	}
	if err := d.relationshipCall(step, http.MethodGet, "/friends/requests/pending", caller, nil, &result); err != nil {
		return err
	}
	if len(result.Requests) != 0 {
		return fmt.Errorf("relationships.%s: 不应有 pending 请求，实际 %d 个", step, len(result.Requests))
	}
	return nil
}

func (d *driver) relationshipFriends(step string, caller, peer account, expected *relationshipFriend) error {
	var result struct {
		Friends []relationshipFriend `json:"friends"`
	}
	if err := d.relationshipCall(step, http.MethodGet, "/friends", caller, nil, &result); err != nil {
		return err
	}
	if expected == nil {
		if len(result.Friends) != 0 {
			return fmt.Errorf("relationships.%s: 独立账号不应有好友，实际 %d 个", step, len(result.Friends))
		}
		return nil
	}
	if len(result.Friends) != 1 || !sameRelationshipFriend(result.Friends[0], *expected) {
		return fmt.Errorf("relationships.%s: 好友列表应只有 user_id=%s 且资料、备注、分组一致", step, peer.id)
	}
	return nil
}

func (d *driver) relationshipGroups(step string, caller account, id entityID, expected *relationshipGroup) error {
	var result struct {
		Groups []relationshipGroup `json:"groups"`
	}
	if err := d.relationshipCall(step, http.MethodGet, "/friends/groups", caller, nil, &result); err != nil {
		return err
	}
	count := 0
	for _, item := range result.Groups {
		if item.ID == id {
			count++
			if expected == nil || item != *expected {
				return fmt.Errorf("relationships.%s: 分组不应可见或名称、好友计数不匹配", step)
			}
		}
	}
	if expected != nil && count != 1 {
		return fmt.Errorf("relationships.%s: 分组 id=%s 应且仅应出现一次，实际 %d 次", step, id, count)
	}
	return nil
}

func (d *driver) relationshipBlacklist(step string, caller, peer account, present bool) error {
	var result struct {
		Users []struct {
			ID       entityID `json:"user_id"`
			Username string   `json:"username"`
		} `json:"users"`
	}
	if err := d.relationshipCall(step, http.MethodGet, "/friends/blacklist", caller, nil, &result); err != nil {
		return err
	}
	if !present {
		if len(result.Users) != 0 {
			return fmt.Errorf("relationships.%s: 黑名单应为空，实际 %d 个", step, len(result.Users))
		}
		return nil
	}
	if len(result.Users) != 1 || result.Users[0].ID != peer.id || result.Users[0].Username != peer.username {
		return fmt.Errorf("relationships.%s: 黑名单应且仅应包含目标 user_id=%s 及其正确资料", step, peer.id)
	}
	return nil
}

func sameRelationshipFriend(actual, expected relationshipFriend) bool {
	groupMatches := actual.GroupID == nil && expected.GroupID == nil
	if expected.GroupID != nil {
		groupMatches = hasEntityID(actual.GroupID, *expected.GroupID)
	}
	return actual.ID == expected.ID && actual.Username == expected.Username && actual.Remark == expected.Remark && groupMatches && actual.GroupName == expected.GroupName
}
