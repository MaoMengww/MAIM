package main

import (
	"encoding/json"
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
	if replay.ID != started.ID || replay.RequesterID != requester.id || replay.Generation != supplemented.Generation || replay.InputVersion != supplemented.InputVersion {
		return errors.New("executor controls: input retry duplicated acceptance or changed work identity")
	}
	afterReplay, err := d.readRun(requester, started.ID)
	if err != nil {
		return err
	}
	if len(afterReplay.InputSources) != 2 || afterReplay.InputSources[0].RequesterID != requester.id || afterReplay.InputSources[1].RequesterID != member.id || afterReplay.InputSources[1].Version != supplemented.InputVersion || afterReplay.Status != "running" {
		return errors.New("executor controls: input retry lost contributor, repeated input or regressed execution")
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
	if stopRequest.Operation != "stop" || stopRequest.ID() != started.ID || stopRequest.Generation != request.Generation || len(stopRequest.Inputs) != 0 {
		return errors.New("executor controls: stop not delivered to current executor")
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
	// Completion can win a stop request before any stop confirmation. This is a
	// distinct execution, not a forbidden late completion of the stopped Run.
	race, err := d.startRun(requester, bot.ID, conv, "completion race "+suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	raceRequest, err := x.request(race.ID)
	if err != nil {
		return err
	}
	if _, err = x.update(executorPayload(raceRequest, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	if _, err = d.controlExecutorRun(requester, race.ID, "stop", "", uuid.NewString()); err != nil {
		return err
	}
	completion := executorPayload(raceRequest, "run.update", "completed", 1)
	completion["result_text"] = executorText(raceRequest.Text())
	if _, err = x.update(completion, false, x.conn == nil); err != nil {
		return err
	}
	finished, err := d.publishedRun(requester, race.ID)
	if err != nil {
		return err
	}
	if finished.Status != "succeeded" || finished.ExecutorStatus != "completed" || finished.StopStatus == "confirmed" {
		return errors.New("executor controls: actual completion lost to unconfirmed stop request")
	}
	if _, err = x.update(executorPayload(raceRequest, "run.update", "running", 2), true, false); err != nil {
		return err
	}
	return d.executorAcceptedSources(admin, requester, bot, conv, x, suffix)
}

func (d *driver) executorAcceptedSources(sender, requester account, bot botView, conv entityID, x *controlledExecutor, suffix string) error {
	for _, phase := range []string{"queued", "waiting_input", "completed"} {
		for _, operation := range []string{"edit", "personal_delete", "recall", "delete_all"} {
			label := phase + "_" + operation + "_" + suffix
			source, err := d.sendMessage(sender, conv, "EXECUTOR_ACCEPTED_BODY_"+label, 0)
			if err != nil {
				return err
			}
			var original sentMessage
			if err = d.request(http.MethodPost, "/messages/send", requester.token, map[string]any{
				"conversation_id": conv, "client_msg_id": uuid.NewString(), "reply_to_msg_id": source.MessageID,
				"content": map[string]any{"text": "compute accepted material " + label, "mentions": []entityID{bot.ID}}}, &original); err != nil {
				return err
			}
			var started publicRun
			err = d.broadcastPoll("message-backed executor request accepted", func(bounded *driver) (bool, error) {
				runs, err := bounded.conversationRuns(requester, conv)
				if err != nil {
					return false, err
				}
				for _, run := range runs {
					if hasEntityID(run.SourceMessageID, original.MessageID) {
						started = run
						return true, nil
					}
				}
				return false, nil
			})
			if err != nil {
				return err
			}
			request, err := x.request(started.ID)
			if err != nil {
				return err
			}
			sequence := int64(1)
			if phase != "queued" {
				if _, err = x.update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
					return err
				}
			}
			if phase == "waiting_input" {
				if _, err = x.update(executorPayload(request, "run.update", "waiting_input", sequence), false, false); err != nil {
					return err
				}
				sequence++
			}
			material, err := executorAcceptedMaterial(request, source.MessageID)
			if err != nil {
				return err
			}
			if material != source.Content.Text {
				return errors.New("executor-source: delivery lost actual accepted source body")
			}
			result := request.Text() + "\n" + material + "\n" + executorText(request.Text()+"\n"+material)
			var finished publicRun
			if phase == "completed" {
				payload := executorPayload(request, "run.update", "completed", sequence)
				payload["result_text"] = result
				if _, err = x.update(payload, false, false); err != nil {
					return err
				}
				finished, err = d.publishedRun(requester, started.ID)
				if err != nil {
					return err
				}
			}
			if err = d.mutateWorkSource(sender, requester, source, operation); err != nil {
				return err
			}
			if err = d.mutateWorkSource(requester, requester, original, operation); err != nil {
				return err
			}
			current, err := d.readRun(requester, started.ID)
			if err != nil {
				return err
			}
			expectedStatus := "accepted"
			if phase == "waiting_input" {
				expectedStatus = "waiting_input"
			} else if phase == "completed" {
				expectedStatus = "succeeded"
			}
			if current.Status != expectedStatus || current.Generation != request.Generation || current.InputVersion != request.InputVersion {
				return errors.New("executor-source: ordinary IM operation stopped, reran or changed accepted work")
			}
			if operation != "edit" {
				if err = d.authorizationReject("/bots/"+bot.ID.String()+"/runs", requester.token, map[string]any{
					"conv_id": conv, "submission_key": uuid.NewString(), "message": "new hidden source", "message_ids": []entityID{source.MessageID}}); err != nil {
					return err
				}
				if phase == "waiting_input" {
					if err = d.authorizationReject("/runs/"+started.ID.String()+"/control", requester.token, map[string]any{
						"operation": "input", "submission_key": uuid.NewString(), "message": "hidden supplement", "message_ids": []entityID{source.MessageID}}); err != nil {
						return err
					}
				}
			}
			if phase == "queued" {
				if _, err = x.update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
					return err
				}
			}
			if phase == "waiting_input" {
				supplement, err := d.sendMessage(requester, conv, "explicit new input "+label, 0)
				if err != nil {
					return err
				}
				supplemented, err := d.interactionControl(requester, started.ID, "input", supplement.Content.Text, uuid.NewString(), map[string]any{"message_ids": []entityID{supplement.MessageID}})
				if err != nil {
					return err
				}
				request, err = x.requestVersion(started.ID, supplemented.Generation, supplemented.InputVersion, "")
				if err != nil {
					return err
				}
				if err = d.mutateWorkSource(requester, requester, supplement, operation); err != nil {
					return err
				}
				retained, err := executorAcceptedMaterial(request, source.MessageID)
				if err != nil {
					return err
				}
				supplementMaterial, err := executorAcceptedMaterial(request, supplement.MessageID)
				if err != nil {
					return err
				}
				if retained != material || supplementMaterial != supplement.Content.Text || len(request.Inputs) != 2 || request.Inputs[0].Content != original.Content.Text || request.Inputs[1].Content != supplement.Content.Text {
					return errors.New("executor-source: supplemental boundary lost original material or explicit new input")
				}
				result = request.Text() + "\n" + retained + "\n" + executorText(request.Text()+"\n"+retained)
			}
			if phase != "completed" {
				payload := executorPayload(request, "run.update", "completed", sequence)
				payload["result_text"] = result
				if _, err = x.update(payload, false, false); err != nil {
					return err
				}
				finished, err = d.publishedRun(requester, started.ID)
				if err != nil {
					return err
				}
			}
			message, err := d.identityMessage(requester, *finished.MessageID)
			if err != nil {
				return err
			}
			if message.Text.Text != result || finished.ResultText != result {
				return errors.New("executor-source: independent result missing accepted computation after IM operation")
			}
			// Hiding the Bot result itself must not hide its Run or prevent an
			// explicitly authorized continuation from the independent work copy.
			if err = d.request(http.MethodDelete, "/messages/"+finished.MessageID.String(), requester.token, map[string]bool{"delete_for_all": false}, nil); err != nil {
				return err
			}
			retained, err := d.readRun(requester, started.ID)
			if err != nil {
				return err
			}
			if retained.Status != "succeeded" || retained.ResultText != result {
				return errors.New("executor-source: hiding Bot result hid independent execution record")
			}
			child, err := d.interactionStart(requester, bot.ID, conv, "explicit continuation "+label, map[string]any{"parent_run_id": finished.ID})
			if err != nil {
				return err
			}
			childRequest, err := x.request(child.ID)
			if err != nil {
				return err
			}
			var parentResult string
			for _, input := range childRequest.Inputs {
				for _, raw := range input.Materials {
					var parent struct {
						ParentRunID entityID `json:"parent_run_id"`
						Content     string   `json:"content"`
					}
					if err = json.Unmarshal(raw, &parent); err != nil {
						return err
					}
					if parent.ParentRunID == finished.ID {
						parentResult = parent.Content
					}
				}
			}
			if parentResult != result {
				return errors.New("executor-source: explicit continuation could not read independent parent result")
			}
			childResult := parentResult + "\n" + executorText(childRequest.Text()+"\n"+parentResult)
			if _, err = x.complete(childRequest, "text", map[string]any{"text": childResult}, childResult); err != nil {
				return err
			}
			childFinished, err := d.publishedRun(requester, child.ID)
			if err != nil {
				return err
			}
			childMessage, err := d.identityMessage(requester, *childFinished.MessageID)
			if err != nil {
				return err
			}
			if childMessage.Text.Text != childResult {
				return errors.New("executor-source: independent child output missing after source/result deletion")
			}
			fmt.Printf("Accepted executor source: phase=%s operation=%s transport_ws=%t run=%s result=%q child=%s\n", phase, operation, x.conn != nil, started.ID, result, child.ID)
		}
	}
	return nil
}
