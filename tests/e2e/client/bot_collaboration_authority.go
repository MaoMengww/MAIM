package main

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
)

func (d *driver) collaborationRejoin(admin, requester account, conv entityID, bots [5]botView, x, y *controlledExecutor) error {
	parent, err := d.startRun(requester, bots[0].ID, conv, "target participation retry scope", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	pr, err := x.request(parent.ID)
	if err != nil {
		return err
	}
	if _, err = x.update(executorPayload(pr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	key := uuid.NewString()
	body := map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "target_bot_id": bots[4].ID, "submission_key": key, "message": "captured target binding"}
	var child publicRun
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, body, &child); err != nil {
		return err
	}
	if err = d.request(http.MethodDelete, "/convs/"+conv.String()+"/bots/"+bots[4].ID.String(), admin.token, nil, nil); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", admin.token, map[string]any{"bot_id": bots[4].ID}, nil); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: x.token}, body, http.StatusForbidden); err != nil {
		return err
	}
	runs, err := d.conversationRuns(admin, conv)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Origin != nil && r.Origin.SourceRunID == parent.ID && r.BotID == bots[4].ID && r.ID != child.ID {
			return errors.New("same delegation key resurrected old request in rejoined binding")
		}
	}
	if _, err = d.controlExecutorRun(requester, parent.ID, "stop", "", uuid.NewString()); err != nil {
		return err
	}
	fmt.Println("E2E PASS: target remove/rejoin rejects same accepted delegation key instead of creating new child")
	return nil
}

func (d *driver) collaborationSourceGrant(admin, requester account, conv entityID, bots [5]botView, x, y *controlledExecutor, suffix string) error {
	file, err := d.attachmentUpload(admin, "source-share-"+suffix+".txt", "text/plain", []byte("SOURCE_SHARE_REVOKE_CANARY"), 3)
	if err != nil {
		return err
	}
	a, err := d.resourceGrant(admin, bots[0].ID, conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	b, err := d.resourceGrant(admin, bots[1].ID, conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	parent, err := d.interactionStart(requester, bots[0].ID, conv, "source share-dependent delegate", map[string]any{"resource_grant_ids": []entityID{a.ID}})
	if err != nil {
		return err
	}
	pr, err := x.request(parent.ID)
	if err != nil {
		return err
	}
	if _, err = x.update(executorPayload(pr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	var child publicRun
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "target_bot_id": bots[1].ID, "submission_key": uuid.NewString(), "message": "read only explicit shared file", "file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{b.ID}}, &child); err != nil {
		return err
	}
	cr, err := y.request(child.ID)
	if err != nil {
		return err
	}
	if _, err = y.update(executorPayload(cr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	output := executorPayload(cr, "run.update", "completed", 1)
	output["result_text"] = "SOURCE_SHARE_REVOKE_CANARY"
	if _, err = y.update(output, false, false); err != nil {
		return err
	}
	if _, err = d.publishedRun(admin, child.ID); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bot-authorizations/resources", admin.token, map[string]any{"op": "revoke", "grant_id": a.ID}, nil); err != nil {
		return err
	}
	for _, caller := range []account{admin, requester} {
		current, err := d.readRun(caller, child.ID)
		if err != nil {
			return fmt.Errorf("source-only revocation hid delegated child history: %w", err)
		}
		if current.ResultText != "SOURCE_SHARE_REVOKE_CANARY" || current.Status != "succeeded" || current.MessageID == nil {
			return errors.New("source-only revocation changed the committed delegated child result")
		}
	}
	var consumed struct {
		Run        publicRun `json:"run"`
		ResultText string    `json:"result_text"`
	}
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, map[string]any{"operation": "result", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "child_run_id": child.ID}, &consumed); err != nil {
		return fmt.Errorf("source-only revocation blocked authorized delegated result history: %w", err)
	}
	if consumed.Run.ID != child.ID || consumed.ResultText != "SOURCE_SHARE_REVOKE_CANARY" {
		return errors.New("source-only revocation stripped the delegated result snapshot")
	}
	_, err = d.interactionStart(requester, bots[0].ID, conv, "fresh revoked source acquisition", map[string]any{"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{a.ID}})
	if err = d.expectStatus(err, http.StatusForbidden, "fresh source acquisition cannot reuse revoked parent grant"); err != nil {
		return err
	}
	fmt.Println("E2E PASS: source-only sharing revoke preserves delegated child and explicit result history while denying fresh source acquisition")
	return nil
}
