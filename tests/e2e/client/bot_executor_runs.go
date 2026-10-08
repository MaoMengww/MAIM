package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const executorCapabilities = `{"input_types":["text"],"supports_input":false,"supports_stop":false,"supports_resume":false}`

type executorRequest struct {
	Type         string    `json:"type"`
	Run          publicRun `json:"run"`
	Generation   int64     `json:"generation"`
	InputVersion int64     `json:"input_version"`
	Operation    string    `json:"operation"`
	Inputs       []struct {
		Version     int64             `json:"version"`
		RequesterID entityID          `json:"requester_id"`
		Content     string            `json:"content"`
		Materials   []json.RawMessage `json:"materials"`
	} `json:"inputs"`
}

func (r executorRequest) Text() string {
	parts := make([]string, len(r.Inputs))
	for i, input := range r.Inputs {
		parts[i] = input.Content
	}
	return strings.Join(parts, "\n\n")
}

type controlledExecutor struct {
	d       *driver
	bot     entityID
	conn    *websocket.Conn
	secret  string
	token   string
	rest    bool
	pending []executorRequest
}

func (d *driver) executorProvider(path string, body any, output any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.provider+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("executor provider: %s", transportFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("executor provider HTTP%d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output)
}

func (d *driver) webhookExecutor(owner account, bot entityID) (*controlledExecutor, error) {
	var secrets struct {
		Webhook string `json:"webhook_secret"`
	}
	if err := d.request(http.MethodPost, "/bots/"+bot.String()+"/secret/rotate", owner.token, map[string]any{}, &secrets); err != nil {
		return nil, err
	}
	if secrets.Webhook == "" {
		return nil, errors.New("executor: missing webhook credential")
	}
	var registered struct {
		Registered bool `json:"registered"`
	}
	err := d.executorProvider("/executor/register", map[string]any{"bot_id": bot, "secret": secrets.Webhook, "gateway": d.gateway}, &registered)
	if err != nil {
		return nil, err
	}
	if !registered.Registered {
		return nil, errors.New("executor: provider registration failed")
	}
	return &controlledExecutor{d: d, bot: bot, secret: secrets.Webhook}, nil
}

func (d *driver) websocketExecutor(owner account, bot entityID, address, device string) (*controlledExecutor, error) {
	var issued struct {
		Token string `json:"token"`
	}
	if err := d.request(http.MethodPost, "/bots/"+bot.String()+"/token", owner.token, map[string]int{"ttl_seconds": 300}, &issued); err != nil {
		return nil, err
	}
	conn, err := d.identityBotSocket(address, issued.Token, device)
	if err != nil {
		return nil, err
	}
	if err := d.ready(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return &controlledExecutor{d: d, bot: bot, conn: conn, token: issued.Token}, nil
}

func (x *controlledExecutor) request(id entityID) (executorRequest, error) {
	return x.requestVersion(id, 0, 0, "")
}

func (x *controlledExecutor) requestVersion(id entityID, generation, inputVersion int64, status string) (executorRequest, error) {
	matches := func(request executorRequest) bool {
		return request.Type == "bot.run.request" && (id == "" || request.Run.ID == id) && (generation == 0 || request.Generation == generation) && (inputVersion == 0 || request.InputVersion == inputVersion) && (status == "" || request.Run.Status == status)
	}
	for i, request := range x.pending {
		if matches(request) {
			x.pending = append(x.pending[:i], x.pending[i+1:]...)
			return request, nil
		}
	}
	deadline := time.Now().Add(x.d.timeout)
	if x.conn != nil {
		if err := x.conn.SetReadDeadline(deadline); err != nil {
			return executorRequest{}, err
		}
		for {
			var request executorRequest
			if err := x.conn.ReadJSON(&request); err != nil {
				return request, err
			}
			if matches(request) {
				return request, nil
			}
		}
	}
	for time.Now().Before(deadline) {
		var observed struct {
			Requests []struct {
				Request executorRequest `json:"request"`
				Valid   bool            `json:"signature_valid"`
			} `json:"executor_requests"`
		}
		ctx, cancel := context.WithTimeout(context.Background(), x.d.timeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, x.d.provider+"/observations", nil)
		if err != nil {
			cancel()
			return executorRequest{}, err
		}
		resp, err := x.d.client.Do(req)
		if err != nil {
			cancel()
			return executorRequest{}, err
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&observed)
		resp.Body.Close()
		cancel()
		if err != nil {
			return executorRequest{}, err
		}
		for i := len(observed.Requests) - 1; i >= 0; i-- {
			row := observed.Requests[i]
			if matches(row.Request) {
				if !row.Valid {
					return executorRequest{}, errors.New("executor: webhook signature invalid")
				}
				return row.Request, nil
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	return executorRequest{}, errors.New("executor: request not delivered")
}

func (x *controlledExecutor) update(payload map[string]any, reject bool, compute bool) (publicRun, error) {
	if x.rest {
		var run publicRun
		err := x.d.request(http.MethodPost, "/bot-app/runs/update", x.token, payload, &run)
		if reject {
			var api *apiError
			if !errors.As(err, &api) || api.status < 400 || api.status >= 500 || api.code == 0 {
				return publicRun{}, fmt.Errorf("executor: REST rejection missing: %v", err)
			}
			return publicRun{}, nil
		}
		return run, err
	}
	if x.conn == nil {
		var response struct {
			Status int `json:"status"`
			Body   struct {
				Code *int      `json:"code"`
				Data publicRun `json:"data"`
			} `json:"body"`
		}
		err := x.d.executorProvider("/executor/update", map[string]any{"bot_id": x.bot, "update": payload, "compute": compute, "secret": x.secret}, &response)
		if err != nil {
			return publicRun{}, err
		}
		if reject {
			if response.Status < 400 || response.Status >= 500 || response.Body.Code == nil || *response.Body.Code == 0 {
				return publicRun{}, fmt.Errorf("executor: rejected update succeeded HTTP%d", response.Status)
			}
			return publicRun{}, nil
		}
		if response.Status < 200 || response.Status >= 300 || response.Body.Code == nil || *response.Body.Code != 0 || response.Body.Data.ID == "" {
			return publicRun{}, fmt.Errorf("executor: update failed HTTP%d", response.Status)
		}
		return response.Body.Data, nil
	}
	if err := x.conn.SetWriteDeadline(time.Now().Add(x.d.timeout)); err != nil {
		return publicRun{}, err
	}
	if err := x.conn.WriteJSON(payload); err != nil {
		return publicRun{}, err
	}
	if err := x.conn.SetReadDeadline(time.Now().Add(x.d.timeout)); err != nil {
		return publicRun{}, err
	}
	for {
		var response struct{ executorRequest }
		if err := x.conn.ReadJSON(&response); err != nil {
			return publicRun{}, err
		}
		if response.Type == "bot.run.request" {
			// Delivery can interleave with the receipt, especially input/stop controls.
			x.pending = append(x.pending, response.executorRequest)
		}
		if response.Type == "error" {
			if reject {
				return publicRun{}, nil
			}
			return publicRun{}, errors.New("executor: legitimate WS update rejected")
		}
		if response.Type == "bot.run.receipt" {
			if reject || response.Run.ID == "" {
				return publicRun{}, errors.New("executor: invalid WS acceptance")
			}
			return response.Run, nil
		}
	}
}

func executorPayload(request executorRequest, kind, status string, sequence int64) map[string]any {
	payload := map[string]any{"type": kind, "run_id": request.Run.ID, "generation": request.Generation, "input_version": request.InputVersion, "sequence": sequence, "update_key": uuid.NewString(), "status": status}
	if kind == "run.delivery_ack" {
		payload["operation"] = request.Operation
	}
	return payload
}

func executorText(text string) string {
	digest := sha256.Sum256([]byte(text))
	return fmt.Sprintf("words=%d;sha256=%s", len(strings.Fields(text)), hex.EncodeToString(digest[:]))
}

func (d *driver) readRun(caller account, id entityID) (publicRun, error) {
	var run publicRun
	err := d.request(http.MethodGet, "/runs/"+id.String(), caller.token, nil, &run)
	return run, err
}

func (d *driver) publishedRun(caller account, id entityID) (publicRun, error) {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		run, err := d.readRun(caller, id)
		if err != nil {
			return run, err
		}
		if run.OutputStatus == "committed" && run.MessageID != nil {
			return run, nil
		}
		if run.OutputStatus == "failed" || run.Status == "failed" {
			return run, fmt.Errorf("executor: output failed: %s", run.Error)
		}
		time.Sleep(30 * time.Millisecond)
	}
	return publicRun{}, errors.New("executor: result not committed")
}

func (x *controlledExecutor) complete(request executorRequest, label string, content map[string]any, resultText string) (map[string]any, error) {
	for _, payload := range []map[string]any{executorPayload(request, "run.delivery_ack", "", 0), executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
		if _, err := x.update(payload, false, false); err != nil {
			return nil, err
		}
	}
	invalid := executorPayload(request, "run.update", "completed", 2)
	invalid["msg_type"], invalid["result_text"] = label, resultText
	switch label {
	case "file":
		invalid["content"] = map[string]any{"file_id": 7}
	case "result":
		invalid["content"] = map[string]any{"type": "invalid", "data": "not-json"}
	default:
		invalid["content"] = map[string]any{"text": resultText, "unexpected_identity": "forged"}
	}
	if _, err := x.update(invalid, true, false); err != nil {
		return nil, err
	}
	payload := executorPayload(request, "run.update", "completed", 2)
	payload["msg_type"], payload["content"], payload["result_text"] = label, content, resultText
	_, err := x.update(payload, false, false)
	return payload, err
}

func (d *driver) executorMatrix(x, wrong *controlledExecutor, caller, admin account, bot botView, binding identityBinding, conv entityID, socket *p6Socket, reconnectAddress, suffix string) error {
	secretHistory := "UNRELATED_SECRET_" + suffix
	if _, err := d.sendMessage(admin, conv, secretHistory, 0); err != nil {
		return err
	}
	text := "count these five words " + suffix
	source, err := d.sendBotMention(caller, bot.ID, conv, text)
	if err != nil {
		return err
	}
	var request executorRequest
	if x.conn != nil {
		request, err = x.request("")
	}
	if x.conn == nil {
		deadline := time.Now().Add(d.timeout)
		for time.Now().Before(deadline) {
			runs, e := d.conversationRuns(caller, conv)
			if e != nil {
				return e
			}
			for _, r := range runs {
				if hasEntityID(r.SourceMessageID, source.MessageID) {
					request, err = x.request(r.ID)
					break
				}
			}
			if request.Run.ID != "" {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(request.Inputs)
	if err != nil {
		return err
	}
	if request.Run.ID == "" || request.Run.BindingID != binding.BindingID || request.Run.RequesterID != caller.id || request.Run.BotID != bot.ID || request.Run.ConvID != conv || !hasEntityID(request.Run.SourceMessageID, source.MessageID) || request.Run.TriggerKind != "mention" || request.Generation < 1 || request.InputVersion < 1 || request.Text() != text || strings.Contains(string(raw), secretHistory) {
		return errors.New("executor: source/requester/input isolation broken")
	}
	delivered, err := d.readRun(caller, request.Run.ID)
	if err != nil {
		return err
	}
	caps := delivered.ExecutorCapabilities
	if delivered.ExecutorType != "third_party" || len(caps.InputTypes) != 1 || caps.InputTypes[0] != "text" || caps.SupportsInput || caps.SupportsStop || caps.SupportsResume || delivered.Status != "accepted" || delivered.ExecutorStatus != "pending" || delivered.MessageID != nil {
		return errors.New("executor: transport delivery misreported success or capabilities")
	}
	ack := executorPayload(request, "run.delivery_ack", "", 0)
	acknowledged, err := x.update(ack, false, false)
	if err != nil {
		return err
	}
	if acknowledged.DeliveryStatus != "acknowledged" || acknowledged.ExecutorStatus != "pending" || acknowledged.Status != "accepted" || acknowledged.MessageID != nil {
		return errors.New("executor: ACK accepted/completed business work")
	}
	claim := executorPayload(request, "run.claim", "accepted", 0)
	if _, err = wrong.update(claim, true, false); err != nil {
		return err
	}
	if x.conn == nil {
		forged := *x
		forged.secret = "invalid-executor-secret"
		if _, err = forged.update(claim, true, false); err != nil {
			return err
		}
	}
	invalid := maps.Clone(claim)
	invalid["generation"] = request.Generation + 1
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = maps.Clone(claim)
	invalid["generation"] = 0
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = maps.Clone(claim)
	invalid["requester_id"] = admin.id
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = executorPayload(request, "run.update", "completed", 1)
	invalid["result_text"] = "unclaimed"
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	accepted, err := x.update(claim, false, false)
	if err != nil {
		return err
	}
	if accepted.Generation != request.Generation || accepted.ExecutorStatus != "accepted" || accepted.Status != "running" || accepted.MessageID != nil {
		return errors.New("executor: claim receipt broken")
	}
	replay, err := x.update(claim, false, false)
	if err != nil {
		return err
	}
	if replay.Version != accepted.Version || replay.ID != accepted.ID {
		return errors.New("executor: duplicate claim mutated run")
	}
	invalid = maps.Clone(claim)
	invalid["stage"] = "different"
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	for _, operation := range []string{"input", "stop", "resume"} {
		control := map[string]any{"operation": operation, "submission_key": uuid.NewString()}
		if operation == "input" {
			control["message"] = "more"
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+request.Run.ID.String()+"/control", caller, control, http.StatusConflict); err != nil {
			return fmt.Errorf("unsupported %s: %w", operation, err)
		}
	}
	unchanged, err := d.readRun(caller, request.Run.ID)
	if err != nil {
		return err
	}
	if unchanged.Version != accepted.Version || unchanged.Status != accepted.Status {
		return errors.New("executor: unsupported controls mutated authoritative run")
	}
	running := executorPayload(request, "run.update", "running", 1)
	running["stage"] = "computing"
	progressed, err := x.update(running, false, false)
	if err != nil {
		return err
	}
	if progressed.Version <= accepted.Version || progressed.MessageID != nil {
		return errors.New("executor: running update lacks progress")
	}
	invalid = executorPayload(request, "run.update", "running", 1)
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = executorPayload(request, "run.update", "running", 0)
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = executorPayload(request, "run.update", "accepted", 1)
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = executorPayload(request, "run.update", "accepted", 2)
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = executorPayload(request, "run.update", "running", 3)
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	invalid = executorPayload(request, "run.update", "running", 2)
	invalid["input_version"] = request.InputVersion + 1
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	completed := executorPayload(request, "run.update", "completed", 2)
	result := executorText(request.Text())
	if x.conn != nil {
		completed["result_text"] = result
	}
	terminal, err := x.update(completed, false, x.conn == nil)
	if err != nil {
		return err
	}
	if terminal.ExecutorStatus != "completed" || terminal.ResultText != result {
		return errors.New("executor: external computation result mismatch")
	}
	replay, err = x.update(completed, false, x.conn == nil)
	if err != nil {
		return err
	}
	if replay.Version != terminal.Version {
		return errors.New("executor: duplicate completion mutated run")
	}
	invalid = maps.Clone(completed)
	invalid["result_text"] = "contradiction"
	if _, err = x.update(invalid, true, false); err != nil {
		return err
	}
	late := executorPayload(request, "run.update", "running", 3)
	if _, err = x.update(late, true, false); err != nil {
		return err
	}
	done, err := d.publishedRun(caller, request.Run.ID)
	if err != nil {
		return err
	}
	if done.Status != "succeeded" || done.ExecutorStatus != "completed" || done.ResultText != result {
		return errors.New("executor: authoritative completion mismatch")
	}
	stored, err := d.identityMessage(caller, *done.MessageID)
	if err != nil {
		return err
	}
	expected := identityOutput{text: result, message: storedMessage{MessageID: *done.MessageID, ConvID: conv, Seq: stored.Seq, Type: 1, SenderID: &bot.ID, SenderType: "bot", SenderName: bot.Name, SenderAvatar: bot.Avatar, BindingID: &binding.BindingID, RequesterID: &caller.id, RawPayload: stored.RawPayload, ReplyToID: &source.MessageID}}
	if err = d.identityObserve(socket, caller, expected); err != nil {
		return err
	}
	if _, err = d.p6Wait(socket, 0, "executor authoritative completion", func(e p6Event) bool {
		return e.Type == "bot.run.updated" && e.Run.ID == done.ID && e.Run.Status == "succeeded" && hasEntityID(e.Run.MessageID, *done.MessageID)
	}); err != nil {
		return err
	}
	sourceStored, err := d.identityMessage(caller, source.MessageID)
	if err != nil {
		return err
	}
	if sourceStored.SenderType != "user" || !hasEntityID(sourceStored.SenderID, caller.id) || sourceStored.Text.Text != text {
		return errors.New("executor: source message identity/body rewritten")
	}
	reconnected, err := d.p6Connect(reconnectAddress, caller)
	if err != nil {
		return err
	}
	defer reconnected.close()
	restored, err := d.readRun(caller, done.ID)
	if err != nil {
		return err
	}
	if restored.ID != done.ID || restored.ResultText != result || restored.OutputStatus != "committed" || !sameEntityID(restored.MessageID, done.MessageID) {
		return errors.New("executor: REST recovery lost committed result")
	}
	runs, err := d.conversationRuns(caller, conv)
	if err != nil {
		return err
	}
	if len(runs) != 1 || runs[0].ID != done.ID {
		return errors.New("executor: ordinary activities or bot result started extra run")
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err = d.request(http.MethodGet, "/convs/"+conv.String()+"/messages?limit=50", caller.token, nil, &history); err != nil {
		return err
	}
	count := 0
	for _, m := range history.Messages {
		if m.SenderType == "bot" {
			count++
			if m.MessageID != *done.MessageID {
				return errors.New("executor: progress leaked into history")
			}
		}
	}
	if count != 1 {
		return errors.New("executor: duplicate result messages")
	}
	if err = d.request(http.MethodDelete, "/convs/"+conv.String()+"/bots/"+bot.ID.String(), admin.token, nil, nil); err != nil {
		return err
	}
	if _, err = x.update(completed, true, x.conn == nil); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", admin.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
		return err
	}
	rejoined, err := d.identityBinding(admin, conv, bot.ID)
	if err != nil {
		return err
	}
	if rejoined.BindingID == binding.BindingID {
		return errors.New("executor: stale lifecycle revived")
	}
	if _, err = x.update(completed, true, x.conn == nil); err != nil {
		return err
	}
	return nil
}

func (d *driver) botExecutorRuns(address, secondAddress string) (result error) {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("executor_owner", suffix)
	if err != nil {
		return err
	}
	admin, err := d.register("executor_inviter", suffix)
	if err != nil {
		return err
	}
	requester, err := d.register("executor_requester", suffix)
	if err != nil {
		return err
	}
	member, err := d.register("executor_member", suffix)
	if err != nil {
		return err
	}
	for _, mode := range []string{"ws", "webhook"} {
		socket, err := d.p6Connect(secondAddress, requester)
		if err != nil {
			return err
		}
		defer socket.close()
		var bots [2]botView
		for i := range bots {
			body := map[string]any{"name": fmt.Sprintf("executor-%s-%s-%d", mode, suffix, i), "type": "third_party", "sub_type": mode, "conn_mode": mode, "response_triggers": []string{"mention"}, "capabilities": executorCapabilities}
			if mode == "webhook" {
				body["callback_url"] = d.provider + "/external-webhook"
			}
			if err = d.request(http.MethodPost, "/bots", owner.token, body, &bots[i]); err != nil {
				return err
			}
			defer func() {
				result = errors.Join(result, d.request(http.MethodDelete, "/bots/"+bots[i].ID.String(), owner.token, nil, nil))
			}()
		}
		var x, wrong *controlledExecutor
		if mode == "ws" {
			x, err = d.websocketExecutor(owner, bots[0].ID, address, "executor-"+suffix)
			if err != nil {
				return err
			}
			defer x.conn.Close()
			wrong, err = d.websocketExecutor(owner, bots[1].ID, address, "wrong-executor-"+suffix)
			if err != nil {
				return err
			}
			defer wrong.conn.Close()
		} else {
			x, err = d.webhookExecutor(owner, bots[0].ID)
			if err != nil {
				return err
			}
			wrong, err = d.webhookExecutor(owner, bots[1].ID)
			if err != nil {
				return err
			}
		}
		var conv struct {
			ID entityID `json:"conversation_id"`
		}
		if err = d.request(http.MethodPost, "/convs", admin.token, map[string]any{"type": "group", "group_name": mode + suffix, "member_ids": []entityID{requester.id, member.id}}, &conv); err != nil {
			return err
		}
		if err = d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", admin.token, map[string]any{"bot_id": bots[0].ID}, nil); err != nil {
			return err
		}
		binding, err := d.identityBinding(admin, conv.ID, bots[0].ID)
		if err != nil {
			return err
		}
		if err = d.executorMatrix(x, wrong, requester, admin, bots[0], binding, conv.ID, socket, secondAddress, suffix); err != nil {
			return fmt.Errorf("%s executor: %w", mode, err)
		}
		if err = d.executorControls(owner, admin, requester, member, bots[0], conv.ID, x, address, suffix); err != nil {
			return fmt.Errorf("%s executor controls: %w", mode, err)
		}
		if err = d.executorActivities(owner, admin, requester, bots, mode, address, suffix, socket); err != nil {
			return fmt.Errorf("%s executor activities: %w", mode, err)
		}
	}
	return nil
}
