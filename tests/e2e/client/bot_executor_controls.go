package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

func (d *driver) controlExecutorRun(caller account, id entityID, operation, message, key string) (publicRun, error) {
	body := map[string]any{"operation": operation, "submission_key": key}
	if message != "" {
		body["message"] = message
	}
	var run publicRun
	err := d.request(http.MethodPost, "/runs/"+id.String()+"/control", caller.token, body, &run)
	return run, err
}

func (d *driver) executorControls(owner, admin, requester, member account, bot botView, conv entityID, x *controlledExecutor, address, suffix string) error {
	if err := d.request(http.MethodPut, "/bots/"+bot.ID.String(), owner.token, map[string]string{"capabilities": `{"input_types":["text"],"supports_input":true,"supports_stop":true,"supports_resume":true}`}, nil); err != nil {
		return err
	}
	// An unclaimed WS request must survive disconnection in the delivery outbox.
	if x.conn != nil {
		x.conn.Close()
	}
	started, err := d.startRun(requester, bot.ID, conv, "sum the supplied words "+suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if x.conn != nil {
		x, err = d.websocketExecutor(owner, bot.ID, address, "reconnected-executor-"+suffix)
		if err != nil {
			return err
		}
		defer x.conn.Close()
	}
	request, err := x.request(started.ID)
	if err != nil {
		return err
	}
	if request.Run.ID != started.ID || request.Generation != started.Generation || request.Text() != "sum the supplied words "+suffix {
		return errors.New("executor reconnect: unclaimed request lost or replaced")
	}
	caps := request.Run.ExecutorCapabilities
	if !caps.SupportsInput || !caps.SupportsStop || !caps.SupportsResume {
		return errors.New("executor controls: advertised capabilities missing")
	}
	claim := executorPayload(request, "run.claim", "accepted", 0)
	if _, err = x.update(claim, false, false); err != nil {
		return err
	}
	waiting := executorPayload(request, "run.update", "waiting_input", 1)
	waitingReceipt, err := x.update(waiting, false, false)
	if err != nil {
		return err
	}
	if waitingReceipt.Status != "waiting_input" {
		return errors.New("executor controls: waiting input not authoritative")
	}
	for _, operation := range []string{"stop", "resume"} {
		body := map[string]any{"operation": operation, "submission_key": uuid.NewString()}
		err = d.runtimeReject(http.MethodPost, "/runs/"+started.ID.String()+"/control", member, body, http.StatusForbidden)
		if err != nil {
			return fmt.Errorf("executor control-rights %s: %w", operation, err)
		}
	}
	inputKey := uuid.NewString()
	supplemented, err := d.controlExecutorRun(member, started.ID, "input", "additional words to count", inputKey)
	if err != nil {
		return err
	}
	if supplemented.InputVersion != request.InputVersion+1 || supplemented.Generation != request.Generation || supplemented.Status != "running" || supplemented.RequesterID != requester.id {
		return errors.New("executor controls: input reordered or replaced requester")
	}
	deliveredInput, err := x.requestVersion(started.ID, supplemented.Generation, supplemented.InputVersion, "")
	if err != nil {
		return err
	}
	if deliveredInput.InputVersion != supplemented.InputVersion || !strings.Contains(deliveredInput.Text(), "additional words to count") {
		return errors.New("executor controls: accepted input version/body not delivered")
	}
	if len(deliveredInput.Run.InputSources) != 2 || deliveredInput.Run.InputSources[0].RequesterID != requester.id || deliveredInput.Run.InputSources[1].RequesterID != member.id || deliveredInput.Run.InputSources[1].Version != supplemented.InputVersion || deliveredInput.Run.InputSources[1].SourceMessageID != nil {
		return errors.New("executor controls: supplement attribution lost or replaced original requester")
	}
	restoredInput, err := d.readRun(requester, started.ID)
	if err != nil {
		return err
	}
	if len(restoredInput.InputSources) != 2 || restoredInput.InputSources[1].RequesterID != member.id || restoredInput.InputSources[1].Version != supplemented.InputVersion {
		return errors.New("executor controls: REST lost accepted input attribution")
	}
	replay, err := d.controlExecutorRun(member, started.ID, "input", "additional words to count", inputKey)
	if err != nil {
		return err
	}
	if replay.Version != supplemented.Version || replay.InputVersion != supplemented.InputVersion {
		return errors.New("executor controls: duplicate input changed version")
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+started.ID.String()+"/control", member, map[string]any{"operation": "input", "message": "different words", "submission_key": inputKey}, http.StatusConflict); err != nil {
		return err
	}
	stale := executorPayload(request, "run.update", "running", 2)
	if _, err = x.update(stale, true, false); err != nil {
		return err
	}
	request = deliveredInput
	waiting = executorPayload(request, "run.update", "waiting_input", 2)
	if _, err = x.update(waiting, false, false); err != nil {
		return err
	}
	resumed, err := d.controlExecutorRun(admin, started.ID, "resume", "", uuid.NewString())
	if err != nil {
		return err
	}
	if resumed.ID != started.ID || resumed.Generation != request.Generation+1 || resumed.InputVersion != request.InputVersion || resumed.ExecutorStatus != "pending" {
		return errors.New("executor controls: resume failed to fence old generation")
	}
	oldGeneration := executorPayload(request, "run.update", "completed", 3)
	oldGeneration["result_text"] = "late old generation"
	if _, err = x.update(oldGeneration, true, false); err != nil {
		return err
	}
	resumedRequest, err := x.requestVersion(started.ID, resumed.Generation, resumed.InputVersion, "")
	if err != nil {
		return err
	}
	if resumedRequest.Generation != resumed.Generation {
		return errors.New("executor controls: resume request has stale generation")
	}
	request = resumedRequest
	claim = executorPayload(request, "run.claim", "accepted", 0)
	if _, err = x.update(claim, false, false); err != nil {
		return err
	}
	delayedAck := executorPayload(request, "run.delivery_ack", "", 0)
	stopping, err := d.controlExecutorRun(requester, started.ID, "stop", "", uuid.NewString())
	if err != nil {
		return err
	}
	if stopping.Status != "stopping" || stopping.MessageID != nil {
		return errors.New("executor controls: stop request falsely marked stopped")
	}
	if _, err = x.update(delayedAck, true, false); err != nil {
		return fmt.Errorf("executor controls: stale delivery ACK must not acknowledge stop: %w", err)
	}
	stopRequest, err := x.requestVersion(started.ID, stopping.Generation, stopping.InputVersion, "stopping")
	if err != nil {
		return err
	}
	if stopRequest.Run.Status != "stopping" || stopRequest.Generation != request.Generation {
		return errors.New("executor controls: stop not delivered to current executor")
	}
	prohibited := executorPayload(request, "run.update", "completed", 1)
	prohibited["result_text"] = "must not publish after stop"
	if _, err = x.update(prohibited, true, false); err != nil {
		return err
	}
	stoppedPayload := executorPayload(stopRequest, "run.update", "stopped", 1)
	stopped, err := x.update(stoppedPayload, false, false)
	if err != nil {
		return err
	}
	if stopped.Status != "stopped" || stopped.MessageID != nil {
		return errors.New("executor controls: executor stop confirmation missing")
	}
	late := executorPayload(stopRequest, "run.update", "running", 2)
	if _, err = x.update(late, true, false); err != nil {
		return err
	}
	restored, err := d.readRun(requester, started.ID)
	if err != nil {
		return err
	}
	if restored.Status != "stopped" || restored.MessageID != nil || restored.RequesterID != requester.id {
		return errors.New("executor controls: terminal state not recoverable")
	}
	return nil
}
