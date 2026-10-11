package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
)

func (d *driver) collaborationActivities(admin, requester, invitee account, conv entityID, bot botView, x *controlledExecutor) error {
	rules := []map[string]any{}
	for _, kind := range []string{"member.joined", "member.left", "business.activity"} {
		r := map[string]any{"id": uuid.NewString(), "enabled": true, "activity_type": kind}
		if kind == "business.activity" {
			r["business_type"] = "conversation.announcement.updated"
		}
		rules = append(rules, r)
	}
	raw, _ := json.Marshal(map[string]any{"activity_types": []string{"member.joined", "member.left", "business.activity"}, "response_rules": rules})
	if err := d.activitySettings(admin, conv, bot.ID, string(raw)); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodPut, "/convs/"+conv.String()+"/announcement", requester, map[string]string{"content": "unauthorized business activity"}, http.StatusForbidden); err != nil {
		return err
	}
	operations := []struct {
		kind, method, path string
		body               any
	}{
		{"member.joined", http.MethodPost, "/convs/" + conv.String() + "/members/invite", map[string]any{"user_ids": []entityID{invitee.id}}},
		{"member.left", http.MethodPost, "/convs/" + conv.String() + "/members/kick", map[string]any{"user_ids": []entityID{invitee.id}}},
		{"business.activity", http.MethodPut, "/convs/" + conv.String() + "/announcement", map[string]string{"content": "authorized collaboration business source"}},
	}
	for _, op := range operations {
		if err := d.request(op.method, op.path, admin.token, op.body, nil); err != nil {
			return err
		}
		var triggered publicRun
		if err := d.broadcastPoll("authorized "+op.kind, func(b *driver) (bool, error) {
			runs, e := b.conversationRuns(admin, conv)
			if e != nil {
				return false, e
			}
			for _, r := range runs {
				if r.BotID == bot.ID && r.Activity != nil && r.Activity.Type == op.kind {
					triggered = r
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			return err
		}
		if triggered.RequesterID != "" || triggered.Origin == nil || triggered.Origin.AuthorizationUserID != admin.id {
			return errors.New("domain rule impersonated its actor or lost actual admin authority")
		}
		req, err := x.request(triggered.ID)
		if err != nil {
			return err
		}
		if _, err = x.update(executorPayload(req, "run.claim", "accepted", 0), false, false); err != nil {
			return err
		}
		done := executorPayload(req, "run.update", "completed", 1)
		done["result_text"] = executorText(req.Text())
		if _, err = x.update(done, false, false); err != nil {
			return err
		}
		if _, err = d.publishedRun(admin, triggered.ID); err != nil {
			return err
		}
	}
	if err := d.activitySettings(admin, conv, bot.ID, `{"activity_types":[],"response_rules":[]}`); err != nil {
		return err
	}
	fmt.Println("E2E PASS: collaboration real member.joined/member.left/authorized business.activity public sources and unauthorized administrator operation rejection")
	return nil
}
