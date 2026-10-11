package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
)

// Replay accepted delegation after parent completion is retry, not permission
// to schedule fresh children. The same public input key still conflicts.
func (d *driver) collaborationTerminalReplay(requester account, conv entityID, bots [5]botView, x, y *controlledExecutor) error {
	parent, err := d.startRun(requester, bots[0].ID, conv, "terminal parent replay", uuid.NewString(), nil)
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
	key := uuid.NewString()
	body := map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "target_bot_id": bots[1].ID, "submission_key": key, "message": "live child after parent completion"}
	var child publicRun
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, body, &child); err != nil {
		return err
	}
	childRequest, err := y.request(child.ID)
	if err != nil {
		return err
	}
	if _, err = y.update(executorPayload(childRequest, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	completed := executorPayload(req, "run.update", "completed", 1)
	completed["result_text"] = "parent completed without claiming child completed"
	if _, err = x.update(completed, false, false); err != nil {
		return err
	}
	if _, err = d.publishedRun(requester, parent.ID); err != nil {
		return err
	}
	var replay publicRun
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, body, &replay); err != nil {
		return fmt.Errorf("terminal accepted delegation replay: %w", err)
	}
	if replay.ID != child.ID {
		return errors.New("terminal delegation replay created different child")
	}
	body["message"] = "different accepted input"
	if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: x.token}, body, http.StatusConflict); err != nil {
		return err
	}
	body["submission_key"] = uuid.NewString()
	if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: x.token}, body, http.StatusConflict); err != nil {
		return err
	}
	stopped, err := d.controlExecutorRun(requester, parent.ID, "stop", "", uuid.NewString())
	if err != nil {
		return err
	}
	if stopped.Status != "succeeded" {
		return errors.New("terminal parent stop reopened completed parent")
	}
	stop, err := y.requestVersion(child.ID, child.Generation, child.InputVersion, "stopping")
	if err != nil {
		return err
	}
	if _, err = y.update(executorPayload(stop, "run.update", "stopped", 1), false, false); err != nil {
		return err
	}
	actual, err := d.readRun(requester, child.ID)
	if err != nil {
		return err
	}
	if actual.StopStatus != "confirmed" {
		return errors.New("terminal parent failed linked child stop")
	}
	fmt.Println("E2E PASS: terminal parent same-key accepted child replay/conflict/new-admission rejection/linked actual child stop")
	return nil
}

func (d *driver) collaborationRevokedResult(admin, requester account, conv entityID, bots [5]botView, x, y *controlledExecutor, suffix string) error {
	file, err := d.attachmentUpload(admin, "collaboration-shared-"+suffix+".txt", "text/plain", []byte("CHILD_RESULT_DEPENDENCY_CANARY"), 3)
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
	parent, err := d.interactionStart(requester, bots[0].ID, conv, "source authorized context", map[string]any{"resource_grant_ids": []entityID{a.ID}})
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
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "target_bot_id": bots[1].ID, "submission_key": uuid.NewString(), "message": "produce resource-derived result", "file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{b.ID}}, &child); err != nil {
		return err
	}
	cr, err := y.request(child.ID)
	if err != nil {
		return err
	}
	if _, err = y.update(executorPayload(cr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	completion := executorPayload(cr, "run.update", "completed", 1)
	completion["result_text"] = "CHILD_RESULT_DEPENDENCY_CANARY"
	if _, err = y.update(completion, false, false); err != nil {
		return err
	}
	if _, err = d.publishedRun(admin, child.ID); err != nil {
		return err
	}
	var result json.RawMessage
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, map[string]any{"operation": "result", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "child_run_id": child.ID}, &result); err != nil {
		return err
	}
	output := executorPayload(pr, "run.update", "completed", 1)
	output["result_text"] = "parent-derived:CHILD_RESULT_DEPENDENCY_CANARY"
	if _, err = x.update(output, false, false); err != nil {
		return err
	}
	if _, err = d.publishedRun(admin, parent.ID); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bot-authorizations/resources", admin.token, map[string]any{"op": "revoke", "resource_type": "file", "grant_id": b.ID}, nil); err != nil {
		return err
	}
	current, err := d.readRun(admin, parent.ID)
	if err != nil {
		return err
	}
	if current.ResultText != "" {
		return errors.New("terminal parent exposed child-derived result after only child share grant revoked")
	}
	fmt.Println("E2E PASS: current child-only resource sharing revocation invalidates consumed terminal parent-derived REST result")
	return nil
}
