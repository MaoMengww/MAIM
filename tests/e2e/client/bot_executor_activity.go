package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Activities are observations, not executor requests: neither a Run nor a
// run_id may be present, and their source identity remains the user's message.
type executorActivity struct {
	Type         string          `json:"type"`
	ActivityType string          `json:"activity_type"`
	BotID        entityID        `json:"bot_id"`
	ConvID       entityID        `json:"conv_id"`
	BindingID    entityID        `json:"binding_id"`
	RunID        json.RawMessage `json:"run_id"`
	Run          json.RawMessage `json:"run"`
	Message      struct {
		MessageID  entityID       `json:"message_id"`
		SenderID   entityID       `json:"sender_id"`
		SenderType string         `json:"sender_type"`
		Text       string         `json:"text"`
		Seq        sequenceNumber `json:"seq"`
	} `json:"message"`
}

type activityObserver struct {
	x    *controlledExecutor
	seen []executorActivity
}

func (o *activityObserver) snapshot() ([]executorActivity, error) {
	if o.x.conn != nil {
		// A same-conversation subscribed peer has already observed the last
		// source. Its delivery is the consumer barrier; pong then drains this
		// Bot's queued frames without a destructive negative read timeout.
		if err := o.x.conn.SetWriteDeadline(time.Now().Add(o.x.d.timeout)); err != nil {
			return nil, err
		}
		if err := o.x.conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
			return nil, err
		}
		for {
			frame, err := o.read()
			if err != nil {
				return nil, err
			}
			if frame.Type == "pong" {
				return o.seen, nil
			}
		}
	}
	var observed struct {
		Activities []struct {
			Request executorActivity `json:"request"`
			Valid   bool             `json:"signature_valid"`
		} `json:"executor_activities"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.x.d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.x.d.provider+"/observations", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.x.d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("activity observations: %s", transportFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("activity observations HTTP%d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&observed); err != nil {
		return nil, err
	}
	o.seen = nil
	for _, row := range observed.Activities {
		if row.Request.BotID != o.x.bot {
			continue
		}
		if !row.Valid {
			return nil, errors.New("activity: webhook signature invalid")
		}
		o.seen = append(o.seen, row.Request)
	}
	return o.seen, nil
}

func (o *activityObserver) read() (executorActivity, error) {
	var frame executorActivity
	if err := o.x.conn.SetReadDeadline(time.Now().Add(o.x.d.timeout)); err != nil {
		return frame, err
	}
	if err := o.x.conn.ReadJSON(&frame); err != nil {
		return frame, err
	}
	switch frame.Type {
	case "bot.activity":
		o.seen = append(o.seen, frame)
	case "bot.run.request":
		return frame, errors.New("activity: ordinary source delivered as Run work")
	case "error":
		return frame, errors.New("activity: observation socket error")
	}
	return frame, nil
}

func (o *activityObserver) wait(source sentMessage, binding entityID) error {
	deadline := time.Now().Add(o.x.d.timeout)
	for time.Now().Before(deadline) {
		for _, activity := range o.seen {
			if activity.Message.MessageID != source.MessageID {
				continue
			}
			if activity.Type != "bot.activity" || activity.ActivityType != "message.created" || activity.BotID != o.x.bot || activity.ConvID != source.ConvID || activity.BindingID != binding || len(activity.RunID) != 0 || len(activity.Run) != 0 || activity.Message.SenderID != source.SenderID || activity.Message.SenderType != "user" || activity.Message.Text != source.Content.Text || activity.Message.Seq != source.Seq {
				return errors.New("activity: source identity/text/binding or observation-only contract mismatch")
			}
			return nil
		}
		if o.x.conn != nil {
			if _, err := o.read(); err != nil {
				return err
			}
		} else {
			if _, err := o.snapshot(); err != nil {
				return err
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	return errors.New("activity: matching ordinary source not observed")
}

func (d *driver) activitySettings(admin account, conv, bot entityID, settings string) error {
	return d.request(http.MethodPut, "/convs/"+conv.String()+"/bots/"+bot.String(), admin.token, map[string]string{"bot_settings": settings}, nil)
}

func (d *driver) activityBinding(admin account, conv, bot entityID) (entityID, error) {
	var response struct {
		Bots []identityBinding `json:"bots"`
	}
	if err := d.request(http.MethodGet, "/convs/"+conv.String()+"/bots", admin.token, nil, &response); err != nil {
		return "", err
	}
	for _, binding := range response.Bots {
		if binding.BotID == bot && binding.BindingID != "" {
			return binding.BindingID, nil
		}
	}
	return "", errors.New("activity: active conversation binding missing")
}

func (d *driver) activityNoWork(caller account, conv entityID) error {
	runs, err := d.conversationRuns(caller, conv)
	if err != nil {
		return err
	}
	if len(runs) != 0 {
		return errors.New("activity: ordinary observations created Run work")
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err = d.request(http.MethodGet, "/convs/"+conv.String()+"/messages?limit=50", caller.token, nil, &history); err != nil {
		return err
	}
	for _, message := range history.Messages {
		if message.SenderType == "bot" {
			return errors.New("activity: observation produced unauthorized Bot output")
		}
	}
	return nil
}

func (d *driver) executorActivities(owner, admin, caller account, bots [2]botView, mode, address, suffix string, socket *p6Socket) error {
	var err error
	socket, err = d.p6Connect(address, caller)
	if err != nil {
		return err
	}
	defer socket.close()
	var observers [2]activityObserver
	for i, bot := range bots {
		if err := d.request(http.MethodPut, "/bots/"+bot.ID.String(), owner.token, map[string]string{"capabilities": executorCapabilities}, nil); err != nil {
			return err
		}
		var x *controlledExecutor
		var err error
		if mode == "ws" {
			x, err = d.websocketExecutor(owner, bot.ID, address, fmt.Sprintf("activity-%s-%d", suffix, i))
		} else {
			x, err = d.webhookExecutor(owner, bot.ID)
		}
		if err != nil {
			return err
		}
		if x.conn != nil {
			defer x.conn.Close()
		}
		observers[i].x = x
	}
	const subscription = `{"activity_types":["message.created"]}`
	var convs [2]entityID
	var bindings [2][2]entityID
	for c := range convs {
		var conv struct {
			ID entityID `json:"conversation_id"`
		}
		if err := d.request(http.MethodPost, "/convs", admin.token, map[string]any{"type": "group", "group_name": fmt.Sprintf("activity-%s-%s-%d", mode, suffix, c), "member_ids": []entityID{caller.id}}, &conv); err != nil {
			return err
		}
		convs[c] = conv.ID
		for i, bot := range bots {
			if err := d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", admin.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
				return err
			}
			binding, err := d.activityBinding(admin, conv.ID, bot.ID)
			if err != nil {
				return err
			}
			bindings[c][i] = binding
		}
		// Only the sentinel starts subscribed. It observes the same partition
		// and proves both ordinary sources were processed before any absence check.
		if err := d.activitySettings(admin, conv.ID, bots[1].ID, subscription); err != nil {
			return err
		}
	}
	sendPair := func(conv entityID, label string) ([]sentMessage, error) {
		var sources []sentMessage
		for i := range 2 {
			source, err := d.sendMessage(caller, conv, fmt.Sprintf("ordinary activity %s %s %d", label, suffix, i), 0)
			if err != nil {
				return nil, err
			}
			if _, err = d.p6Message(socket, source); err != nil {
				return nil, err
			}
			sources = append(sources, source)
		}
		return sources, nil
	}
	denied := func(c int, label string) error {
		sources, err := sendPair(convs[c], label)
		if err != nil {
			return err
		}
		barrier, err := d.sendMessage(caller, convs[c], "ordinary activity barrier "+label+" "+suffix, 0)
		if err != nil {
			return err
		}
		if _, err = d.p6Message(socket, barrier); err != nil {
			return err
		}
		if err = observers[1].wait(barrier, bindings[c][1]); err != nil {
			return err
		}
		activities, err := observers[0].snapshot()
		if err != nil {
			return err
		}
		for _, activity := range activities {
			for _, source := range sources {
				if activity.Message.MessageID == source.MessageID {
					return fmt.Errorf("activity: %s source crossed subscription/binding authorization", label)
				}
			}
		}
		return d.activityNoWork(caller, convs[c])
	}
	if err := denied(0, "default-unsubscribed"); err != nil {
		return err
	}
	if err := d.activitySettings(admin, convs[0], bots[0].ID, `{"activity_types":["message.unknown"]}`); err != nil {
		return err
	}
	if err := denied(0, "unknown-type"); err != nil {
		return err
	}
	if err := d.activitySettings(admin, convs[0], bots[0].ID, subscription); err != nil {
		return err
	}
	if err := denied(1, "wrong-binding"); err != nil {
		return err
	}
	sources, err := sendPair(convs[0], "authorized")
	if err != nil {
		return err
	}
	for _, source := range sources {
		if err = observers[0].wait(source, bindings[0][0]); err != nil {
			return err
		}
		stored, err := d.identityMessage(caller, source.MessageID)
		if err != nil {
			return err
		}
		if stored.SenderType != "user" || !hasEntityID(stored.SenderID, caller.id) || stored.Text.Text != source.Content.Text || stored.Seq != source.Seq {
			return errors.New("activity: persisted source identity/text rewritten")
		}
	}
	// Reusing an observed message identity is not an accepted Run and cannot
	// authorize a claim or completion, regardless of the executor credential.
	fake := executorRequest{Run: publicRun{ID: sources[0].MessageID}, Generation: 1, InputVersion: 1}
	for _, kind := range []string{"run.claim", "run.update"} {
		status := "accepted"
		if kind == "run.update" {
			status = "completed"
		}
		sequence := int64(0)
		if kind == "run.update" {
			sequence = 1
		}
		payload := executorPayload(fake, kind, status, sequence)
		if kind == "run.update" {
			payload["msg_type"], payload["content"], payload["result_text"] = "text", map[string]string{"text": "unauthorized activity output"}, "unauthorized activity output"
		}
		if _, err = observers[0].x.update(payload, true, false); err != nil {
			return err
		}
	}
	if err = d.activityNoWork(caller, convs[0]); err != nil {
		return err
	}
	if err = d.activitySettings(admin, convs[0], bots[0].ID, `{"activity_types":[]}`); err != nil {
		return err
	}
	if err = denied(0, "subscription-revoked"); err != nil {
		return err
	}
	if err = d.activitySettings(admin, convs[0], bots[0].ID, subscription); err != nil {
		return err
	}
	fresh, err := sendPair(convs[0], "reauthorized")
	if err != nil {
		return err
	}
	if err = observers[0].wait(fresh[1], bindings[0][0]); err != nil {
		return err
	}
	if err = d.request(http.MethodDelete, "/convs/"+convs[0].String()+"/bots/"+bots[0].ID.String(), admin.token, nil, nil); err != nil {
		return err
	}
	if err = denied(0, "binding-removed"); err != nil {
		return err
	}
	return d.activityNoWork(caller, convs[0])
}
