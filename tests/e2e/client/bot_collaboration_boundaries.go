package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"time"
)

func (x *controlledExecutor) delegateWS(parent publicRun, target entityID, text string) (publicRun, error) {
	body := map[string]any{"type": "bot.run.delegate", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "target_bot_id": target, "submission_key": uuid.NewString(), "message": text}
	if err := x.conn.SetWriteDeadline(time.Now().Add(x.d.timeout)); err != nil {
		return publicRun{}, err
	}
	if err := x.conn.WriteJSON(body); err != nil {
		return publicRun{}, err
	}
	if err := x.conn.SetReadDeadline(time.Now().Add(x.d.timeout)); err != nil {
		return publicRun{}, err
	}
	for {
		var raw json.RawMessage
		if err := x.conn.ReadJSON(&raw); err != nil {
			return publicRun{}, err
		}
		var envelope struct {
			Type   string    `json:"type"`
			Result publicRun `json:"result"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return publicRun{}, err
		}
		if envelope.Type == "error" {
			return publicRun{}, errors.New("explicit WS delegation rejected")
		}
		if envelope.Type == "bot.run.delegation.receipt" {
			return envelope.Result, nil
		}
		if envelope.Type == "bot.run.request" {
			var request executorRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return publicRun{}, err
			}
			x.pending = append(x.pending, request)
		}
	}
}

func (d *driver) collaborationBounds(requester account, conv entityID, bots [5]botView, x *controlledExecutor) error {
	for _, budget := range []string{"count", "input"} {
		root, err := d.startRun(requester, bots[0].ID, conv, "bounded "+budget+" collaboration", uuid.NewString(), nil)
		if err != nil {
			return err
		}
		req, err := x.request(root.ID)
		if err != nil {
			return err
		}
		if _, err = x.update(executorPayload(req, "run.claim", "accepted", 0), false, false); err != nil {
			return err
		}
		quantity, text := 8, "explicit small task"
		if budget == "input" {
			quantity = 4
			text = strings.Repeat("x", 60000)
		}
		for range quantity {
			var child publicRun
			if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, map[string]any{"operation": "delegate", "run_id": root.ID, "generation": root.Generation, "input_version": root.InputVersion, "target_bot_id": bots[1].ID, "submission_key": uuid.NewString(), "message": text}, &child); err != nil {
				return err
			}
		}
		overflow := "ninth task"
		if budget == "input" {
			overflow = strings.Repeat("y", 30000)
		}
		if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: x.token}, map[string]any{"operation": "delegate", "run_id": root.ID, "generation": root.Generation, "input_version": root.InputVersion, "target_bot_id": bots[1].ID, "submission_key": uuid.NewString(), "message": overflow}, http.StatusTooManyRequests); err != nil {
			return err
		}
		limited, err := d.readRun(requester, root.ID)
		if err != nil {
			return err
		}
		if !strings.Contains(limited.ContextNotice, "budget") {
			return errors.New("resource limit rejected without visible collaboration constraint")
		}
		if len(limited.ChildRunIDs) != quantity {
			return errors.New("resource rejection still created overflow child")
		}
		if _, err = d.controlExecutorRun(requester, root.ID, "stop", "", uuid.NewString()); err != nil {
			return err
		}
	}
	fmt.Println("E2E PASS: collaboration descendant count and cumulative input-byte limits reject further work with visible constraint")
	return nil
}

func (d *driver) collaborationResourceBoundary(admin, requester account, conv entityID, bots [5]botView, x *controlledExecutor, suffix string) error {
	file, err := d.attachmentUpload(admin, "collaboration-private-"+suffix+".txt", "text/plain", []byte("CANARY_DELEGATION_PRIVATE_CONTEXT"), 3)
	if err != nil {
		return err
	}
	sourceGrant, err := d.resourceGrant(admin, bots[0].ID, conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	targetGrant, err := d.resourceGrant(admin, bots[1].ID, conv, "file", file.info.FileID, "conversation", false)
	if err != nil {
		return err
	}
	parent, err := d.interactionStart(requester, bots[0].ID, conv, "authorized source read", map[string]any{"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{sourceGrant.ID}})
	if err != nil {
		return err
	}
	req, err := x.request(parent.ID)
	if err != nil {
		return err
	}
	if _, err = x.update(executorPayload(req, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: x.token}, map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "target_bot_id": bots[1].ID, "submission_key": uuid.NewString(), "message": "use source file", "file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{targetGrant.ID}}, http.StatusForbidden); err != nil {
		return err
	}
	// A task with no explicit material never inherits parent file content/grants.
	child, err := x.delegateWS(parent.publicRun, bots[1].ID, "task without private source context")
	if err != nil {
		return err
	}
	if child.Origin == nil || child.Origin.SourceRunID != parent.ID {
		return errors.New("WS delegation lost source")
	}
	if _, err = d.controlExecutorRun(requester, parent.ID, "stop", "", uuid.NewString()); err != nil {
		return err
	}
	fmt.Println("E2E PASS: target read-without-share denied; explicit WS delegation does not inherit source file or permissions")
	return nil
}
