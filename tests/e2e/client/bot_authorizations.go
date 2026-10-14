package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// These views are decoded exclusively from the authenticated public contract.
type resourceAuthorization struct {
	ID           entityID   `json:"id"`
	BotID        entityID   `json:"bot_id"`
	ConvID       entityID   `json:"conv_id"`
	ResourceType string     `json:"resource_type"`
	ResourceID   entityID   `json:"resource_id"`
	Scope        string     `json:"scope"`
	CanRead      bool       `json:"can_read"`
	CanShare     bool       `json:"can_share"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
}

type authorizationAction struct {
	ID             entityID        `json:"id"`
	RunID          entityID        `json:"run_id"`
	ToolName       string          `json:"tool_name"`
	ServerID       string          `json:"server_id"`
	Risk           string          `json:"risk"`
	Target         string          `json:"target"`
	Parameters     json.RawMessage `json:"parameters"`
	ParametersJSON string          `json:"parameters_json"`
	Status         string          `json:"status"`
	ExpiresAt      int64           `json:"expires_at"`
	Generation     int64           `json:"generation"`
	InputVersion   int64           `json:"input_version"`
	CanApprove     bool            `json:"can_approve"`
}

type authorizationObservation struct {
	Held     bool `json:"held"`
	Requests []struct {
		Body json.RawMessage `json:"body"`
	} `json:"requests"`
	Calls []struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	} `json:"calls"`
	Settled bool `json:"settled"`
}

type authorizationFixture struct {
	d                                             *driver
	owner, requester, admin, member               account
	bot, external                                 botView
	conv, otherConv, server, model, embedding, kb entityID
	file                                          uploadedAttachment
	suffix                                        string
	browser                                       map[string]any
}

func (d *driver) resourceGrant(owner account, bot, conv entityID, kind string, resource entityID, scope string, share bool) (resourceAuthorization, error) {
	var grant resourceAuthorization
	err := d.request(http.MethodPost, "/bot-authorizations/resources", owner.token, map[string]any{
		"op": "create", "bot_id": bot, "conv_id": conv, "resource_type": kind, "resource_id": resource,
		"scope": scope, "can_read": true, "can_share": share, "expires_at": time.Now().Add(10 * time.Minute).Unix(),
	}, &grant)
	if err == nil && (grant.ID == "" || grant.BotID != bot || grant.ConvID != conv || grant.ResourceID != resource || !grant.CanRead || grant.CanShare != share || grant.Scope != scope) {
		return grant, errors.New("bot-authorizations: grant lost resource identity or independent read/share permissions")
	}
	return grant, err
}

func (d *driver) authorizationReject(path, token string, body any) error {
	err := d.request(http.MethodPost, path, token, body, nil)
	var api *apiError
	if !errors.As(err, &api) || api.status < 400 || api.status >= 500 {
		return fmt.Errorf("authorization: expected explicit public 4xx denial at %s, got %v", path, err)
	}
	return nil
}

func (f *authorizationFixture) save() error {
	if f.d.controlDir == "" {
		return nil
	}
	raw, err := json.MarshalIndent(f.browser, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(f.d.controlDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.d.controlDir, "browser-fixtures.json"), append(raw, '\n'), 0600)
}

func (f *authorizationFixture) record(label string, run publicRun, action *authorizationAction, token string) error {
	f.browser["runs"].(map[string]any)[label] = map[string]any{"id": run.ID, "action": action, "token": token}
	return f.save()
}

func (f *authorizationFixture) observe(token string) (authorizationObservation, error) {
	var row authorizationObservation
	err := f.d.executorProvider("/authorization/observe", map[string]string{"token": token}, &row)
	return row, err
}

func (f *authorizationFixture) arm(token string, calls []map[string]any, hold bool) error {
	var result struct {
		OK bool `json:"ok"`
	}
	err := f.d.executorProvider("/authorization/arm", map[string]any{"token": token, "calls": calls, "hold": hold}, &result)
	if err == nil && !result.OK {
		return errors.New("authorization provider did not arm")
	}
	return err
}

func (f *authorizationFixture) release(token string) error {
	var result struct {
		OK bool `json:"ok"`
	}
	return f.d.executorProvider("/authorization/release", map[string]string{"token": token}, &result)
}

func (f *authorizationFixture) sideEffects(token string, count int) error {
	row, err := f.observe(token)
	if err != nil {
		return err
	}
	if len(row.Calls) != count {
		return fmt.Errorf("authorization external fact %s: expected %d actual MCP calls, got %d", token, count, len(row.Calls))
	}
	return nil
}

func (f *authorizationFixture) modelContains(token, text string, expected bool) error {
	row, err := f.observe(token)
	if err != nil {
		return err
	}
	found := slices.ContainsFunc(row.Requests, func(r struct {
		Body json.RawMessage `json:"body"`
	}) bool {
		return strings.Contains(string(r.Body), text)
	})
	if found != expected {
		return fmt.Errorf("authorization actual model input: canary presence=%t, expected=%t", found, expected)
	}
	return nil
}

func (f *authorizationFixture) start(token string, extra map[string]any) (publicRun, error) {
	run, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, token, extra)
	return run.publicRun, err
}

func (f *authorizationFixture) newAction(token, risk, target string) (publicRun, authorizationAction, error) {
	call := map[string]any{"name": "fixture_auth_" + risk, "arguments": map[string]string{"target": target, "text": token}}
	if err := f.arm(token, []map[string]any{call}, false); err != nil {
		return publicRun{}, authorizationAction{}, err
	}
	run, err := f.start(token, nil)
	if err != nil {
		return run, authorizationAction{}, err
	}
	deadline := time.Now().Add(f.d.timeout)
	for time.Now().Before(deadline) {
		var actions []authorizationAction
		if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
			return run, authorizationAction{}, err
		}
		if len(actions) == 1 {
			action := actions[0]
			if action.Status != "pending" || action.Risk != risk || action.Target != target || action.ServerID != f.server.String() || action.RunID != run.ID {
				return run, action, errors.New("authorization pending action is not the exact trusted risk/target")
			}
			var requesterActions []authorizationAction
			if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.requester.token, map[string]any{"op": "list", "run_id": run.ID}, &requesterActions); err != nil {
				return run, action, err
			}
			if len(requesterActions) != 1 || requesterActions[0].CanApprove || requesterActions[0].Target != "[redacted]" || strings.Contains(string(requesterActions[0].Parameters), target) {
				return run, action, errors.New("requester could inspect or approve owner-only action parameters")
			}
			if err = f.sideEffects(token, 0); err != nil {
				return run, action, err
			}
			return run, action, nil
		}
		run, err = f.d.readRun(f.requester, run.ID)
		if err != nil {
			return run, authorizationAction{}, err
		}
		if run.Status == "failed" || run.Status == "succeeded" {
			return run, authorizationAction{}, fmt.Errorf("authorization action should wait, got %s", run.Status)
		}
		time.Sleep(30 * time.Millisecond)
	}
	return run, authorizationAction{}, errors.New("authorization pending action not observed")
}

func actionDecision(action authorizationAction, op, key string) map[string]any {
	decision := map[string]any{"op": op, "run_id": action.RunID, "action_id": action.ID, "generation": action.Generation,
		"input_version": action.InputVersion, "submission_key": key}
	if op == "approve" {
		decision["expires_at"] = min(time.Now().Add(time.Minute).Unix(), action.ExpiresAt)
	}
	return decision
}

func (d *driver) botAuthorizations(address, secondAddress string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := authorizationFixture{d: d, suffix: suffix}
	for _, item := range []struct {
		name    string
		account *account
	}{{"owner", &f.owner}, {"requester", &f.requester}, {"admin", &f.admin}, {"member", &f.member}} {
		*item.account, err = d.register("authorization_"+item.name, suffix)
		if err != nil {
			return err
		}
	}
	for _, id := range []*entityID{&f.conv, &f.otherConv} {
		var conv struct {
			ID entityID `json:"conversation_id"`
		}
		if err = d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{"type": "group", "group_name": "authorizations-" + suffix, "member_ids": []entityID{f.owner.id, f.requester.id, f.member.id}}, &conv); err != nil {
			return err
		}
		*id = conv.ID
	}
	// First vertical tracer: upload actual bytes and ask the resource owner, not Bot owner, to grant them.
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "authorization-external-" + suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "capabilities": controlCapabilities}, &f.external); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": f.external.ID}, nil); err != nil {
		return err
	}
	f.file, err = d.attachmentUpload(f.requester, "authorization-"+suffix+".txt", "text/plain", []byte("PRIVATE_AUTHORIZATION_FILE_"+suffix), 1)
	if err != nil {
		return err
	}
	tracer, err := d.resourceGrant(f.requester, f.external.ID, f.conv, "file", f.file.info.FileID, "personal", false)
	if err != nil {
		return fmt.Errorf("bot-authorizations vertical tracer create personal read/no-share: %w", err)
	}
	fmt.Printf("Authorization tracer: resource=%s grant=%s read=true share=false\n", f.file.info.FileID, tracer.ID)
	f.model, err = d.fixtureModel(f.owner, "chat", "fixture-authorization-"+suffix)
	if err != nil {
		return err
	}
	f.embedding, err = d.fixtureModel(f.requester, "embed", "fixture-embed-authorization-"+suffix)
	if err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "authorization-builtin-" + suffix, "type": "self_deployed", "model_id": f.model, "response_triggers": []string{"mention"}, "streaming_enabled": true, "enable_knowledge": true}, &f.bot); err != nil {
		return err
	}
	for _, conv := range []entityID{f.conv, f.otherConv} {
		if err = d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
			return err
		}
	}
	policies := map[string]any{}
	for _, risk := range []string{"read", "draft", "send", "modify", "delete", "pay"} {
		policies["fixture_auth_"+risk] = map[string]any{"risk": risk, "target_parameter": "target", "can_share": true, "conversation_ids": []entityID{f.conv, f.otherConv}}
	}
	advanced, _ := json.Marshal(map[string]any{"timeout": 10, "retry_count": 0, "tool_policies": policies})
	var server struct {
		ID entityID `json:"id"`
	}
	if err = d.request(http.MethodPost, "/mcp-servers", f.owner.token, map[string]any{"name": "authorization-tools-" + suffix, "transport": "http-streamable", "url": d.provider + "/authorization/mcp", "enabled": true, "advanced_config": string(advanced)}, &server); err != nil {
		return err
	}
	f.server = server.ID
	for _, bot := range []entityID{f.bot.ID, f.external.ID} {
		if err = d.request(http.MethodPost, "/bots/"+bot.String()+"/mcp-servers", f.owner.token, map[string]any{"mcp_server_id": f.server}, nil); err != nil {
			return err
		}
	}
	var kb struct {
		ID entityID `json:"id"`
	}
	if err = d.request(http.MethodPost, "/knowledge/bases", f.requester.token, map[string]any{"name": "authorization-knowledge-" + suffix, "mode": "rag", "embedding_model_id": f.embedding, "pipeline_config": map[string]any{"parsing": map[string]any{"engines": []string{"builtin"}}, "chunking": map[string]any{"chunk_size": 1024, "overlap": 0}, "retrieval": map[string]any{"mode": "vector", "top_k": 5, "score_threshold": 0.7}}}, &kb); err != nil {
		return err
	}
	f.kb = kb.ID
	doc, err := d.uploadKnowledge(f.requester, f.kb, "authorization-source.txt", "authorization-source-"+suffix, knownKnowledge+" PRIVATE_AUTHORIZATION_KNOWLEDGE_"+suffix)
	if err != nil {
		return err
	}
	if err = d.awaitKnowledgeDocument(f.requester, doc, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	if err = d.broadcastPoll("authorization knowledge visible", func(bounded *driver) (bool, error) {
		query := *bounded
		query.timeout = d.queryDeadline
		var response struct {
			Items []struct {
				KBID    entityID `json:"kb_id"`
				DocID   entityID `json:"doc_id"`
				Content string   `json:"content"`
			} `json:"items"`
		}
		if err := query.request(http.MethodPost, "/knowledge/bases/"+f.kb.String()+"/search", f.requester.token,
			map[string]any{"query": "P5_KNOWN_ANCHOR launch code", "kb_ids": []entityID{f.kb}}, &response); err != nil {
			return false, err
		}
		return slices.ContainsFunc(response.Items, func(item struct {
			KBID    entityID `json:"kb_id"`
			DocID   entityID `json:"doc_id"`
			Content string   `json:"content"`
		}) bool {
			return item.KBID == f.kb && item.DocID == doc.ID && item.Content == knownKnowledge+" PRIVATE_AUTHORIZATION_KNOWLEDGE_"+suffix
		}), nil
	}); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/knowledge", f.requester.token, map[string]any{"kb_id": f.kb}, nil); err != nil {
		return err
	}
	accounts := map[string]any{}
	for _, item := range []struct {
		name    string
		account account
	}{{"owner", f.owner}, {"requester", f.requester}, {"admin", f.admin}, {"member", f.member}} {
		accounts[item.name] = map[string]any{"id": item.account.id, "username": item.account.username, "password": item.account.password, "token": item.account.token}
	}
	f.browser = map[string]any{"accounts": accounts, "conversation_id": f.conv, "other_conversation_id": f.otherConv, "builtin_bot_id": f.bot.ID, "external_bot_id": f.external.ID, "server_id": f.server, "gateway": d.gateway, "realtime_a": address, "realtime_b": secondAddress, "provider": d.provider, "runs": map[string]any{}}
	if err = f.save(); err != nil {
		return err
	}
	if err = f.resources(secondAddress); err != nil {
		return fmt.Errorf("authorization resources: %w", err)
	}
	if err = f.actions(secondAddress); err != nil {
		return fmt.Errorf("authorization actions: %w", err)
	}
	fmt.Printf("Authorization evidence: bot=%s conv=%s resource=%s kb=%s; public model input and actual MCP side-effect counts PASS\n", f.bot.ID, f.conv, f.file.info.FileID, f.kb)
	return nil
}

func (f *authorizationFixture) resources(address string) error {
	var sockets []*p6Socket
	for _, caller := range []account{f.owner, f.admin, f.member} {
		socket, err := f.d.p6Connect(address, caller)
		if err != nil {
			return err
		}
		sockets = append(sockets, socket)
		defer socket.close()
	}
	for _, resource := range []struct {
		kind   string
		id     entityID
		canary string
	}{{"file", f.file.info.FileID, "PRIVATE_AUTHORIZATION_FILE_" + f.suffix}, {"knowledge", f.kb, "PRIVATE_AUTHORIZATION_KNOWLEDGE_" + f.suffix}} {
		for _, scope := range []string{"personal", "conversation"} {
			grant, err := f.d.resourceGrant(f.requester, f.bot.ID, f.conv, resource.kind, resource.id, scope, false)
			if err != nil {
				return err
			}
			extra := map[string]any{"resource_grant_ids": []entityID{grant.ID}}
			if resource.kind == "file" {
				extra["file_ids"] = []entityID{resource.id}
			}
			sharedToken := "AUTH_NO_SHARE_" + scope + "_" + resource.kind + "_" + f.suffix
			if err = f.arm(sharedToken, nil, false); err != nil {
				return err
			}
			body := maps.Clone(extra)
			body["conv_id"], body["submission_key"], body["message"] = f.conv, uuid.NewString(), sharedToken
			if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.requester.token, body); err != nil {
				return err
			}
			if err = f.modelContains(sharedToken, resource.canary, false); err != nil {
				return err
			}
			privateToken := "AUTH_PRIVATE_" + scope + "_" + resource.kind + "_" + f.suffix + " P5_KNOWN_ANCHOR"
			if err = f.arm(privateToken, nil, false); err != nil {
				return err
			}
			extra["visibility"] = "personal"
			run, err := f.start(privateToken, extra)
			if err != nil {
				return err
			}
			run, err = f.d.waitRun(f.requester, run.ID)
			if err != nil {
				return err
			}
			if run.Status != "succeeded" || run.Visibility != "personal" || !hasEntityID(run.PersonalUserID, f.requester.id) || run.MessageID != nil || !strings.Contains(run.ResultText, resource.canary) {
				return errors.New("private work did not read actual material or published a group message")
			}
			if err = f.modelContains(privateToken, resource.canary, true); err != nil {
				return err
			}
			if err = f.record("private_"+resource.kind, run, nil, privateToken); err != nil {
				return err
			}
			if err = f.groupNoCanary(resource.canary); err != nil {
				return err
			}
			for _, caller := range []account{f.owner, f.admin, f.member} {
				if err = f.d.runtimeReject(http.MethodGet, "/runs/"+run.ID.String(), caller, nil, http.StatusForbidden); err != nil {
					return err
				}
				if err = f.d.authorizationReject("/bot-authorizations/resources", caller.token, map[string]any{"op": "read", "run_id": run.ID, "grant_id": grant.ID}); err != nil {
					return err
				}
			}
			for _, caller := range []account{f.requester, f.owner, f.admin, f.member} {
				runs, err := f.d.conversationRuns(caller, f.conv)
				if err != nil {
					return err
				}
				if slices.ContainsFunc(runs, func(item publicRun) bool {
					return item.ID == run.ID || strings.Contains(item.ResultText, resource.canary)
				}) {
					return errors.New("private run leaked into shared listing")
				}
			}
			for _, socket := range sockets {
				socket.mu.Lock()
				raw, _ := json.Marshal(socket.events)
				socket.mu.Unlock()
				if strings.Contains(string(raw), resource.canary) || strings.Contains(string(raw), run.ID.String()) {
					return errors.New("private run leaked through shared WS update/stream")
				}
			}
			cross := maps.Clone(body)
			cross["conv_id"] = f.otherConv
			cross["visibility"] = "personal"
			cross["submission_key"] = uuid.NewString()
			if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.requester.token, cross); err != nil {
				return err
			}
			if scope == "personal" {
				other := maps.Clone(body)
				other["visibility"] = "personal"
				other["submission_key"] = uuid.NewString()
				if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.owner.token, other); err != nil {
					return err
				}
			}
			if err = f.d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
				return err
			}
			body["visibility"] = "personal"
			body["submission_key"] = uuid.NewString()
			if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.requester.token, body); err != nil {
				return err
			}
		}
	}
	if err := f.browserPrivate(); err != nil {
		return err
	}
	if err := f.inflightRevocation(); err != nil {
		return err
	}
	if err := f.deletedHistory(); err != nil {
		return err
	}
	return f.externalResource(address)
}

func (f *authorizationFixture) inflightRevocation() error {
	for _, resource := range []struct {
		kind   string
		id     entityID
		canary string
	}{{"file", f.file.info.FileID, "PRIVATE_AUTHORIZATION_FILE_" + f.suffix}, {"knowledge", f.kb, "PRIVATE_AUTHORIZATION_KNOWLEDGE_" + f.suffix}} {
		if err := f.inflightResourceRevocation(resource.kind, resource.id, resource.canary); err != nil {
			return err
		}
	}
	return nil
}

func (f *authorizationFixture) inflightResourceRevocation(kind string, id entityID, canary string) error {
	grant, err := f.d.resourceGrant(f.requester, f.bot.ID, f.conv, kind, id, "conversation", true)
	if err != nil {
		return err
	}
	token := "AUTH_REVOKE_INFLIGHT_" + kind + "_" + f.suffix + " P5_KNOWN_ANCHOR"
	if err = f.arm(token, []map[string]any{{"name": "fixture_auth_read", "arguments": map[string]string{"target": "after-revocation", "text": token}}}, true); err != nil {
		return err
	}
	defer f.release(token)
	extra := map[string]any{"resource_grant_ids": []entityID{grant.ID}}
	if kind == "file" {
		extra["file_ids"] = []entityID{id}
	}
	socket, err := f.d.p6Connect(f.browser["realtime_b"].(string), f.member)
	if err != nil {
		return err
	}
	defer socket.close()
	run, err := f.start(token, extra)
	if err != nil {
		return err
	}
	if err = f.d.broadcastPoll("authorization provider inflight", func(*driver) (bool, error) { row, err := f.observe(token); return row.Held, err }); err != nil {
		return err
	}
	beforeRevoke, err := f.observe(token)
	if err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
		return err
	}
	if err = f.release(token); err != nil {
		return err
	}
	if err = f.d.broadcastPoll("revoked provider response settled", func(*driver) (bool, error) { row, err := f.observe(token); return row.Settled, err }); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond)
	final, err := f.d.waitRun(f.requester, run.ID)
	if err != nil {
		var api *apiError
		if !errors.As(err, &api) || api.status != http.StatusForbidden {
			return err
		}
		final = publicRun{ID: run.ID}
	}
	if final.MessageID != nil || strings.Contains(final.ResultText, canary) {
		return errors.New("in-flight revoke leaked restricted result to shared output")
	}
	if err = f.groupNoCanary(canary); err != nil {
		return err
	}
	socket.mu.Lock()
	raw, _ := json.Marshal(socket.events)
	socket.mu.Unlock()
	if strings.Contains(string(raw), canary) {
		return errors.New("revoked resource streamed to shared WS")
	}
	if err = f.sideEffects(token, 0); err != nil {
		return err
	}
	row, err := f.observe(token)
	if err != nil {
		return err
	}
	if len(row.Requests) == 0 {
		return errors.New("inflight model boundary was not actually exercised")
	}
	for _, request := range row.Requests[len(beforeRevoke.Requests):] {
		if strings.Contains(string(request.Body), canary) {
			return errors.New("revoked source reused in later actual model input")
		}
	}
	return f.record("inflight_revoked_"+kind, final, nil, token)
}

func (f *authorizationFixture) deletedHistory() error {
	binding, err := f.d.identityBinding(f.admin, f.conv, f.bot.ID)
	if err != nil {
		return err
	}
	msg, err := f.d.sendMessage(f.admin, f.conv, "PERSONAL_DELETED_AUTHORIZATION_CANARY_"+f.suffix, 0)
	if err != nil {
		return err
	}
	grant, err := f.d.identityGrant(f.requester, f.conv, f.bot.ID, binding.BindingID, msg.Seq, msg.Seq)
	if err != nil {
		return err
	}
	if err = f.d.request(http.MethodDelete, "/messages/"+msg.MessageID.String(), f.requester.token, map[string]bool{"delete_for_all": false}, nil); err != nil {
		return err
	}
	token := "AUTH_DELETED_HISTORY_" + f.suffix
	if err = f.arm(token, nil, false); err != nil {
		return err
	}
	body := map[string]any{"message_ids": []entityID{msg.MessageID}, "conv_id": f.conv, "message": token, "submission_key": uuid.NewString()}
	if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.requester.token, body); err != nil {
		return err
	}
	filtered, err := f.start(token, map[string]any{"history_grant_id": grant})
	if err != nil {
		return err
	}
	filtered, err = f.d.waitRun(f.requester, filtered.ID)
	if err != nil {
		return err
	}
	if filtered.Status != "succeeded" || strings.Contains(filtered.ResultText, msg.Content.Text) {
		return errors.New("filtered personal-deleted history resurrected the source in result")
	}
	var history json.RawMessage
	if err = f.d.request(http.MethodGet, "/convs/"+f.conv.String()+"/messages?cursor=0&limit=50", f.requester.token, nil, &history); err != nil {
		return err
	}
	if strings.Contains(string(history), msg.Content.Text) {
		return errors.New("personal history overlay or Bot result exposed personally deleted source")
	}
	return f.modelContains(token, msg.Content.Text, false)
}

func (f *authorizationFixture) externalResource(address string) error {
	grant, err := f.d.resourceGrant(f.requester, f.external.ID, f.conv, "file", f.file.info.FileID, "personal", false)
	if err != nil {
		return err
	}
	x, err := f.d.websocketExecutor(f.owner, f.external.ID, address, "authorization-resource-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	run, err := f.d.interactionStart(f.requester, f.external.ID, f.conv, "EXTERNAL_RESOURCE_"+f.suffix, map[string]any{"visibility": "personal", "resource_grant_ids": []entityID{grant.ID}})
	if err != nil {
		return err
	}
	request, err := x.request(run.ID)
	if err != nil {
		return err
	}
	path := "/bots/" + f.external.ID.String() + "/executor/resources"
	if err = f.d.authorizationReject(path, x.token, map[string]any{"op": "read", "run_id": run.ID, "grant_id": grant.ID, "generation": request.Generation, "input_version": request.InputVersion}); err != nil {
		return err
	}
	for _, payload := range []map[string]any{executorPayload(request, "run.delivery_ack", "", 0), executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
		if _, err = x.update(payload, false, false); err != nil {
			return err
		}
	}
	var payload json.RawMessage
	resourceBody := map[string]any{"op": "read", "run_id": run.ID, "grant_id": grant.ID, "generation": request.Generation, "input_version": request.InputVersion}
	if err = f.d.request(http.MethodPost, path, x.token, resourceBody, &payload); err != nil {
		return err
	}
	stale := maps.Clone(resourceBody)
	stale["generation"] = int64(0)
	if err = f.d.authorizationReject(path, x.token, stale); err != nil {
		return err
	}
	if !strings.Contains(string(payload), "PRIVATE_AUTHORIZATION_FILE_"+f.suffix) {
		return errors.New("formal executor resource read did not return actual file bytes")
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
		return err
	}
	if err = f.d.authorizationReject(path, x.token, resourceBody); err != nil {
		return err
	}
	return f.record("external_resource_revoked", run.publicRun, nil, "")
}

func (f *authorizationFixture) actions(address string) error {
	// Read and draft cross the real MCP endpoint with no approval record.
	for _, risk := range []string{"read", "draft"} {
		token := "AUTH_SAFE_" + risk + "_" + f.suffix
		if err := f.arm(token, []map[string]any{{"name": "fixture_auth_" + risk, "arguments": map[string]string{"target": "safe", "text": token}}}, false); err != nil {
			return err
		}
		run, err := f.start(token, nil)
		if err != nil {
			return err
		}
		run, err = f.d.waitRun(f.requester, run.ID)
		if err != nil {
			return err
		}
		if run.Status != "succeeded" {
			return fmt.Errorf("read/draft required approval: %s", run.Status)
		}
		if err = f.sideEffects(token, 1); err != nil {
			return err
		}
		row, err := f.observe(token)
		if err != nil {
			return err
		}
		if row.Calls[0].Name != "fixture_auth_"+risk || row.Calls[0].Arguments["target"] != "safe" || row.Calls[0].Arguments["text"] != token {
			return errors.New("read/draft invoked different scoped external parameters")
		}
	}
	for _, risk := range []string{"send", "modify", "delete", "pay"} {
		token := "AUTH_APPROVE_" + risk + "_" + f.suffix
		run, action, err := f.newAction(token, risk, "controlled-"+risk)
		if err != nil {
			return err
		}
		for _, unauthorized := range []account{f.requester, f.admin, f.member} {
			if err = f.d.authorizationReject("/bot-authorizations/actions", unauthorized.token, actionDecision(action, "approve", uuid.NewString())); err != nil {
				return err
			}
		}
		for _, field := range []string{"generation", "input_version"} {
			stale := actionDecision(action, "approve", uuid.NewString())
			stale[field] = int64(0)
			if err = f.d.authorizationReject("/bot-authorizations/actions", f.owner.token, stale); err != nil {
				return err
			}
		}
		if err = f.sideEffects(token, 0); err != nil {
			return err
		}
		decision := actionDecision(action, "approve", uuid.NewString())
		if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, decision, nil); err != nil {
			return err
		}
		run, err = f.d.waitRun(f.requester, run.ID)
		if err != nil {
			return err
		}
		if run.Status != "succeeded" {
			return errors.New("approved exact action did not resume Eino run")
		}
		if err = f.sideEffects(token, 1); err != nil {
			return err
		}
		for range 3 {
			err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, decision, nil)
			var api *apiError
			if err != nil && (!errors.As(err, &api) || api.status >= 500) {
				return err
			}
		}
		if err = f.sideEffects(token, 1); err != nil {
			return err
		}
		if err = f.record("approved_"+risk, run, &action, token); err != nil {
			return err
		}
	}
	for _, op := range []string{"deny", "revoke", "expired"} {
		token := "AUTH_" + strings.ToUpper(op) + "_" + f.suffix
		run, action, err := f.newAction(token, "send", "blocked-"+op)
		if err != nil {
			return err
		}
		decision := actionDecision(action, op, uuid.NewString())
		if op == "expired" {
			decision["op"] = "approve"
			decision["expires_at"] = time.Now().Add(-time.Second).Unix()
			err = f.d.authorizationReject("/bot-authorizations/actions", f.owner.token, decision)
		} else {
			err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, decision, nil)
		}
		if err != nil {
			return err
		}
		if err = f.sideEffects(token, 0); err != nil {
			return err
		}
		if err = f.record(op, run, &action, token); err != nil {
			return err
		}
	}
	// Unknown tool is advertised by the external provider with falsely-safe annotations.
	token := "AUTH_UNKNOWN_" + f.suffix
	if err := f.arm(token, []map[string]any{{"name": "fixture_auth_unknown", "arguments": map[string]string{"target": "unknown", "text": token}}}, false); err != nil {
		return err
	}
	run, err := f.start(token, nil)
	if err != nil {
		return err
	}
	if _, err = f.d.waitRun(f.requester, run.ID); err != nil {
		return err
	}
	if err = f.sideEffects(token, 0); err != nil {
		return err
	}
	if err = f.privateActionApproval(); err != nil {
		return err
	}
	if err = f.acceptedActionSources(); err != nil {
		return err
	}
	if err = f.escapedCredentialAdmission(address); err != nil {
		return err
	}
	if err = f.exactNumericAction(address); err != nil {
		return err
	}
	if err = f.automaticActions(); err != nil {
		return err
	}
	if err = f.externalActions(address); err != nil {
		return err
	}
	// Leave a concrete pending action for the actual browser to approve/deny.
	browserToken := "AUTH_BROWSER_SEND_" + f.suffix
	pending, action, err := f.newAction(browserToken, "send", "browser-controlled-inbox")
	if err != nil {
		return err
	}
	return f.record("browser_pending", pending, &action, browserToken)
}

func (f *authorizationFixture) automaticActions() error {
	// Only the authorized owner/requester can delegate exact parameters.
	token := "AUTH_AUTO_" + f.suffix
	args := map[string]string{"target": "exact-inbox", "text": token}
	var grant struct {
		ID entityID `json:"id"`
	}
	if err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "grant", "bot_id": f.bot.ID, "conv_id": f.conv, "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": args, "expires_at": time.Now().Add(time.Minute).Unix()}, &grant); err != nil {
		return err
	}
	// A grant is scoped to its subject; use its owner as requester rather than transfer it.
	if err := f.arm(token, []map[string]any{{"name": "fixture_auth_send", "arguments": args}}, false); err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token, nil)
	if err != nil {
		return err
	}
	if _, err = f.d.waitRun(f.owner, run.ID); err != nil {
		return err
	}
	if err = f.sideEffects(token, 1); err != nil {
		return err
	}
	changedToken := "AUTH_AUTO_OUTSIDE_" + f.suffix
	changed := map[string]string{"target": "different-inbox", "text": changedToken}
	if err = f.arm(changedToken, []map[string]any{{"name": "fixture_auth_send", "arguments": changed}}, false); err != nil {
		return err
	}
	outside, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, changedToken, nil)
	if err != nil {
		return err
	}
	if err = f.d.broadcastPoll("automatic grant exact parameter boundary", func(*driver) (bool, error) {
		var actions []authorizationAction
		err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": outside.ID}, &actions)
		return len(actions) == 1 && actions[0].Status == "pending", err
	}); err != nil {
		return err
	}
	if err = f.sideEffects(changedToken, 0); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
		return err
	}
	if err = f.arm(token, []map[string]any{{"name": "fixture_auth_send", "arguments": args}}, false); err != nil {
		return err
	}
	revoked, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token, nil)
	if err != nil {
		return err
	}
	// Wait until the newly revoked use is visibly pending, then observe no new external call.
	if err = f.d.broadcastPoll("revoked automatic grant", func(*driver) (bool, error) {
		current, err := f.d.readRun(f.owner, revoked.ID)
		return current.Status == "waiting_authorization", err
	}); err != nil {
		return err
	}
	return f.sideEffects(token, 1)
}

func (f *authorizationFixture) externalActions(address string) error {
	x, err := f.d.websocketExecutor(f.owner, f.external.ID, address, "authorization-actions-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	run, err := f.d.startRun(f.requester, f.external.ID, f.conv, "EXTERNAL_ACTIONS_"+f.suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	request, err := x.request(run.ID)
	if err != nil {
		return err
	}
	for _, payload := range []map[string]any{executorPayload(request, "run.delivery_ack", "", 0), executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
		if _, err = x.update(payload, false, false); err != nil {
			return err
		}
	}
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	for _, risk := range []string{"read", "draft"} {
		token := "AUTH_EXTERNAL_SAFE_" + risk + "_" + f.suffix
		body := map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_" + risk, "arguments": map[string]string{"target": "external-safe", "text": token}}
		var safe authorizationAction
		if err = f.d.request(http.MethodPost, path, x.token, body, &safe); err != nil {
			return err
		}
		if safe.Status != "approved" {
			return errors.New("formal read/draft unexpectedly requires approval")
		}
		if err = f.d.request(http.MethodPost, path, x.token, actionDecision(safe, "execute", uuid.NewString()), nil); err != nil {
			return err
		}
		if err = f.sideEffects(token, 1); err != nil {
			return err
		}
	}
	token := "AUTH_EXTERNAL_" + f.suffix
	body := map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": map[string]string{"target": "external-inbox", "text": token}}
	var action authorizationAction
	if err = f.d.request(http.MethodPost, path, x.token, body, &action); err != nil {
		return err
	}
	var outcome struct {
		Action authorizationAction `json:"action"`
		Result string              `json:"result"`
	}
	execute := actionDecision(action, "execute", uuid.NewString())
	if err = f.d.authorizationReject(path, x.token, execute); err != nil {
		return err
	}
	if err = f.sideEffects(token, 0); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	mutated := maps.Clone(execute)
	mutated["arguments"] = map[string]string{"target": "different-target", "text": token}
	if err = f.d.authorizationReject(path, x.token, mutated); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, path, x.token, execute, &outcome); err != nil {
		return err
	}
	if !strings.Contains(outcome.Result, "external-inbox") {
		return errors.New("formal action contract did not invoke actual MCP")
	}
	if err = f.sideEffects(token, 1); err != nil {
		return err
	}
	for range 3 {
		if err = f.d.request(http.MethodPost, path, x.token, execute, &outcome); err != nil {
			return err
		}
	}
	if err = f.sideEffects(token, 1); err != nil {
		return err
	}
	// A new target is a different action, not covered by approval of the first one.
	changed := maps.Clone(body)
	changed["submission_key"] = uuid.NewString()
	changed["arguments"] = map[string]string{"target": "changed-inbox", "text": token + "_CHANGED"}
	var different authorizationAction
	if err = f.d.request(http.MethodPost, path, x.token, changed, &different); err != nil {
		return err
	}
	if different.ID == action.ID {
		return errors.New("changed parameters reused an approved action")
	}
	if err = f.d.authorizationReject(path, x.token, actionDecision(different, "execute", uuid.NewString())); err != nil {
		return err
	}
	// Ordinary input and resume cannot convert the changed action into a permit.
	_, err = f.d.interactionControl(f.requester, run.ID, "input", "I approve everything", uuid.NewString(), nil)
	var api *apiError
	if err != nil && (!errors.As(err, &api) || api.status >= 500) {
		return err
	}
	_, err = f.d.interactionControl(f.requester, run.ID, "resume", "approve", uuid.NewString(), nil)
	if err != nil && (!errors.As(err, &api) || api.status >= 500) {
		return err
	}
	if err = f.d.authorizationReject(path, x.token, actionDecision(different, "execute", uuid.NewString())); err != nil {
		return err
	}
	if err = f.sideEffects(token, 1); err != nil {
		return err
	}
	if err = f.approvedActionSources(x); err != nil {
		return err
	}
	if err = f.externalActionFences(x); err != nil {
		return err
	}
	return f.record("external_exact_action", run, &action, token)
}

func (f *authorizationFixture) approvedActionSources(x *controlledExecutor) error {
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	for _, operation := range []string{"edit", "personal_delete", "recall", "delete_all"} {
		token := "AUTH_APPROVED_SOURCE_" + strings.ToUpper(operation) + "_" + f.suffix
		source, err := f.d.sendMessage(f.admin, f.conv, "APPROVED_SOURCE_BODY_"+token, 0)
		if err != nil {
			return err
		}
		started, err := f.d.interactionStart(f.requester, f.external.ID, f.conv, token, map[string]any{"message_ids": []entityID{source.MessageID}})
		if err != nil {
			return err
		}
		request, err := x.request(started.ID)
		if err != nil {
			return err
		}
		material, err := executorAcceptedMaterial(request, source.MessageID)
		if err != nil {
			return err
		}
		if material != source.Content.Text {
			return errors.New("approved-source: executor did not receive the actual accepted source")
		}
		if _, err = x.update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
			return err
		}
		var action authorizationAction
		if err = f.d.request(http.MethodPost, path, x.token, map[string]any{
			"op": "request", "run_id": started.ID, "generation": request.Generation, "input_version": request.InputVersion,
			"submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_send",
			"arguments": map[string]string{"target": "approved-source-inbox", "text": token + "|" + material}}, &action); err != nil {
			return err
		}
		if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
			return err
		}
		if err = f.sideEffects(token, 0); err != nil {
			return err
		}
		if err = f.d.mutateWorkSource(f.admin, f.requester, source, operation); err != nil {
			return err
		}
		var outcome struct {
			Action authorizationAction `json:"action"`
			Result string              `json:"result"`
		}
		execute := actionDecision(action, "execute", uuid.NewString())
		for range 3 {
			if err = f.d.request(http.MethodPost, path, x.token, execute, &outcome); err != nil {
				return err
			}
			if outcome.Action.Status != "succeeded" || !strings.Contains(outcome.Result, material) {
				return errors.New("approved-source: original approval/parameters/external result lost after IM operation")
			}
		}
		if err = f.sideEffects(token, 1); err != nil {
			return err
		}
		completion := executorPayload(request, "run.update", "completed", 1)
		completion["result_text"] = outcome.Result
		if _, err = x.update(completion, false, false); err != nil {
			return err
		}
		finished, err := f.d.publishedRun(f.requester, started.ID)
		if err != nil {
			return err
		}
		message, err := f.d.identityMessage(f.requester, *finished.MessageID)
		if err != nil {
			return err
		}
		if message.Text.Text != outcome.Result || finished.ResultText != outcome.Result {
			return errors.New("approved-source: independent published result was hidden or changed")
		}
		if err = f.record("approved_source_"+operation, finished, &action, token); err != nil {
			return err
		}
	}
	return nil
}

// Existing scenarios authorize their actual external echo endpoint explicitly.
// Sharing is an owner decision scoped to known conversations, never a fixture
// name added to production's platform whitelist.
func (d *driver) fixtureEchoPolicy(owner account, server entityID, convs ...entityID) error {
	var current struct {
		Advanced string `json:"advanced_config"`
	}
	path := "/mcp-servers/" + server.String()
	if err := d.request(http.MethodGet, path, owner.token, nil, &current); err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(current.Advanced), &config); err != nil {
		return err
	}
	var scopes []entityID
	if policies, ok := config["tool_policies"].(map[string]any); ok {
		if policy, ok := policies["fixture_echo"].(map[string]any); ok {
			if ids, ok := policy["conversation_ids"].([]any); ok {
				for _, id := range ids {
					if text, ok := id.(string); ok {
						scopes = append(scopes, entityID(text))
					}
				}
			}
		}
	}
	scopes = append(scopes, convs...)
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	config["tool_policies"] = map[string]any{"fixture_echo": map[string]any{"risk": "read", "can_share": true, "conversation_ids": scopes}}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return d.request(http.MethodPut, path, owner.token, map[string]any{"advanced_config": string(raw)}, nil)
}

func (f *authorizationFixture) groupNoCanary(canary string) error {
	for _, caller := range []account{f.owner, f.requester, f.admin, f.member} {
		var history json.RawMessage
		if err := f.d.request(http.MethodGet, "/convs/"+f.conv.String()+"/messages?cursor=0&limit=50", caller.token, nil, &history); err != nil {
			return err
		}
		if strings.Contains(string(history), canary) {
			return errors.New("restricted material leaked into persisted group history")
		}
	}
	return nil
}

func (f *authorizationFixture) externalActionFences(x *controlledExecutor) error {
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	for _, mode := range []string{"expiry", "revoke", "stop", "assignment", "same_key_changed_parameters"} {
		token := "AUTH_EXTERNAL_FENCE_" + mode + "_" + f.suffix
		run, err := f.d.startRun(f.requester, f.external.ID, f.conv, token, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		request, err := x.request(run.ID)
		if err != nil {
			return err
		}
		for _, payload := range []map[string]any{executorPayload(request, "run.delivery_ack", "", 0), executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
			if _, err = x.update(payload, false, false); err != nil {
				return err
			}
		}
		body := map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_pay", "arguments": map[string]string{"target": "controlled-payment-ledger", "text": token}}
		var action authorizationAction
		if err = f.d.request(http.MethodPost, path, x.token, body, &action); err != nil {
			return err
		}
		for _, field := range []string{"generation", "input_version"} {
			stale := actionDecision(action, "execute", uuid.NewString())
			stale[field] = int64(0)
			if err = f.d.authorizationReject(path, x.token, stale); err != nil {
				return err
			}
		}
		approve := actionDecision(action, "approve", uuid.NewString())
		if mode == "expiry" {
			approve["expires_at"] = time.Now().Add(2 * time.Second).Unix()
		}
		if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, approve, nil); err != nil {
			return err
		}
		switch mode {
		case "expiry":
			// A real previously accepted approval ages out; not merely invalid input.
			time.Sleep(time.Until(time.Unix(approve["expires_at"].(int64), 0)) + 50*time.Millisecond)
		case "revoke":
			if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "revoke", uuid.NewString()), nil); err != nil {
				return err
			}
		case "stop":
			if _, err = f.d.interactionControl(f.requester, run.ID, "stop", "", uuid.NewString(), nil); err != nil {
				return err
			}
		case "assignment":
			if err = f.d.request(http.MethodDelete, "/bots/"+f.external.ID.String()+"/mcp-servers/"+f.server.String(), f.owner.token, nil, nil); err != nil {
				return err
			}
		case "same_key_changed_parameters":
			changed := maps.Clone(body)
			changed["arguments"] = map[string]string{"target": "unauthorized-payment-ledger", "text": token}
			if err = f.d.authorizationReject(path, x.token, changed); err != nil {
				return err
			}
			if err = f.sideEffects(token, 0); err != nil {
				return err
			}
			continue
		}
		err = f.d.request(http.MethodPost, path, x.token, actionDecision(action, "execute", uuid.NewString()), nil)
		if mode == "revoke" {
			var api *apiError
			if err != nil && (!errors.As(err, &api) || api.status >= 500) {
				return err
			}
		} else {
			var api *apiError
			if !errors.As(err, &api) || api.status < 400 || api.status >= 500 {
				return fmt.Errorf("expired/stopped/unassigned execute was accepted: %v", err)
			}
		}
		if err = f.sideEffects(token, 0); err != nil {
			return err
		}
		if mode == "assignment" {
			if err = f.d.request(http.MethodPost, "/bots/"+f.external.ID.String()+"/mcp-servers", f.owner.token, map[string]any{"mcp_server_id": f.server}, nil); err != nil {
				return err
			}
		}
		if err = f.record("external_"+mode, run, &action, token); err != nil {
			return err
		}
	}
	return f.actionPrincipalIncarnations(x)
}

func (f *authorizationFixture) browserPrivate() error {
	grant, err := f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", f.file.info.FileID, "personal", false)
	if err != nil {
		return err
	}
	token := "AUTH_BROWSER_PRIVATE_" + f.suffix
	if err = f.arm(token, nil, false); err != nil {
		return err
	}
	run, err := f.start(token, map[string]any{"visibility": "personal", "file_ids": []entityID{f.file.info.FileID}, "resource_grant_ids": []entityID{grant.ID}})
	if err != nil {
		return err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if run.Status != "succeeded" || run.MessageID != nil || !strings.Contains(run.ResultText, "PRIVATE_AUTHORIZATION_FILE_"+f.suffix) {
		return errors.New("browser private fixture lacks actual private successful output")
	}
	if err := f.record("browser_private", run, nil, token); err != nil {
		return err
	}
	for _, mode := range []string{"api", "browser"} {
		waitingToken := "AUTH_PRIVATE_INPUT_" + mode + "_" + f.suffix
		if err := f.arm(waitingToken, []map[string]any{{"name": "ask_for_input", "arguments": map[string]string{"reason": "请补充个人工作约束"}}}, false); err != nil {
			return err
		}
		waiting, err := f.start(waitingToken, map[string]any{"visibility": "personal", "file_ids": []entityID{f.file.info.FileID}, "resource_grant_ids": []entityID{grant.ID}})
		if err != nil {
			return err
		}
		if err := f.d.broadcastPoll("personal missing information waits privately", func(*driver) (bool, error) {
			current, err := f.d.readRun(f.requester, waiting.ID)
			return current.Status == "waiting_input", err
		}); err != nil {
			return err
		}
		if mode == "browser" {
			return f.record("browser_private_wait", waiting, nil, waitingToken)
		}
		if _, err := f.d.interactionControl(f.requester, waiting.ID, "input", "确认仅供个人处理", uuid.NewString(), nil); err != nil {
			return err
		}
		completed, err := f.d.waitRun(f.requester, waiting.ID)
		if err != nil {
			return err
		}
		if completed.Status != "succeeded" || completed.MessageID != nil || !strings.Contains(completed.ResultText, "PRIVATE_AUTHORIZATION_FILE_"+f.suffix) {
			return errors.New("private supplementary input did not finish the same authorized Run privately")
		}
	}
	return nil
}

func (f *authorizationFixture) actionPrincipalIncarnations(x *controlledExecutor) error {
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	token := "AUTH_AUTO_PRINCIPAL_" + f.suffix
	arguments := map[string]string{"target": "owner-only-controlled-inbox", "text": token}
	var grant struct {
		ID entityID `json:"id"`
	}
	if err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{
		"op": "grant", "bot_id": f.external.ID, "conv_id": f.conv, "server_id": f.server,
		"tool_name": "fixture_auth_send", "arguments": arguments, "expires_at": time.Now().Add(time.Minute).Unix(),
	}, &grant); err != nil {
		return err
	}
	claim := func(key string) (publicRun, executorRequest, error) {
		run, err := f.d.startRun(f.owner, f.external.ID, f.conv, token, key, nil)
		if err != nil {
			return run, executorRequest{}, err
		}
		request, err := x.request(run.ID)
		if err != nil {
			return run, request, err
		}
		for _, payload := range []map[string]any{executorPayload(request, "run.delivery_ack", "", 0), executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
			if _, err = x.update(payload, false, false); err != nil {
				return run, request, err
			}
		}
		return run, request, nil
	}
	run, request, err := claim(uuid.NewString())
	if err != nil {
		return err
	}
	requestAction := func(request executorRequest) (authorizationAction, error) {
		var action authorizationAction
		err := f.d.request(http.MethodPost, path, x.token, map[string]any{
			"op": "request", "run_id": request.ID(), "generation": request.Generation, "input_version": request.InputVersion,
			"submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": arguments,
		}, &action)
		return action, err
	}
	approved, err := requestAction(request)
	if err != nil {
		return err
	}
	if approved.Status != "approved" {
		return errors.New("personal automatic grant did not authorize its original owner principal")
	}
	taken, err := f.d.interactionControl(f.admin, run.ID, "takeover", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if taken.ControllerID != f.admin.id || taken.RequesterID != f.owner.id {
		return errors.New("takeover did not retain provenance and replace controlling principal")
	}
	if err = f.d.authorizationReject(path, x.token, actionDecision(approved, "execute", uuid.NewString())); err != nil {
		return err
	}
	request.Generation, request.InputVersion = taken.Generation, taken.InputVersion
	if taken.Generation != run.Generation {
		request, err = x.requestVersion(run.ID, taken.Generation, taken.InputVersion, "")
		if err != nil {
			return err
		}
		for _, payload := range []map[string]any{executorPayload(request, "run.delivery_ack", "", 0), executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
			if _, err = x.update(payload, false, false); err != nil {
				return err
			}
		}
	}
	pending, err := requestAction(request)
	if err != nil {
		return err
	}
	if pending.Status != "pending" {
		return errors.New("takeover inherited former controlling principal's automatic grant")
	}
	if err = f.sideEffects(token, 0); err != nil {
		return err
	}
	if err = f.record("external_auto_takeover", taken.publicRun, &pending, token); err != nil {
		return err
	}
	// A fresh user membership is not the original delegation subject, even though
	// user UUID and exact action arguments are unchanged.
	if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/kick", f.admin.token, map[string]any{"user_ids": []entityID{f.owner.id}}, nil); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/invite", f.admin.token, map[string]any{"user_ids": []entityID{f.owner.id}}, nil); err != nil {
		return err
	}
	rejoined, current, err := claim(uuid.NewString())
	if err != nil {
		return err
	}
	rejoinedAction, err := requestAction(current)
	if err != nil {
		return err
	}
	if rejoinedAction.Status != "pending" {
		return errors.New("departure and rejoin revived an old membership incarnation's automatic permission")
	}
	if err = f.d.authorizationReject(path, x.token, actionDecision(rejoinedAction, "execute", uuid.NewString())); err != nil {
		return err
	}
	if err = f.sideEffects(token, 0); err != nil {
		return err
	}
	return f.record("external_auto_rejoined", rejoined, &rejoinedAction, token)
}

func (f *authorizationFixture) privateActionApproval() error {
	grant, err := f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", f.file.info.FileID, "personal", false)
	if err != nil {
		return err
	}
	token := "AUTH_PRIVATE_CONFIRM_" + f.suffix
	canary := "PRIVATE_AUTHORIZATION_FILE_" + f.suffix
	if err := f.arm(token, []map[string]any{{"name": "fixture_auth_send", "arguments": map[string]string{"target": "private-confirmation-inbox", "text": token + " " + canary}}}, false); err != nil {
		return err
	}
	run, err := f.start(token, map[string]any{"visibility": "personal", "resource_grant_ids": []entityID{grant.ID}, "file_ids": []entityID{f.file.info.FileID}})
	if err != nil {
		return err
	}
	var action authorizationAction
	if err := f.d.broadcastPoll("personal action requires exact owner approval", func(*driver) (bool, error) {
		var actions []authorizationAction
		err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.requester.token, map[string]any{"op": "list", "run_id": run.ID}, &actions)
		if len(actions) == 1 {
			action = actions[0]
		}
		return len(actions) == 1 && action.Status == "pending", err
	}); err != nil {
		return err
	}
	listOwner := func() ([]authorizationAction, error) {
		var view struct {
			Actions []authorizationAction `json:"actions"`
		}
		err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "bot_id": f.bot.ID, "conv_id": f.conv}, &view)
		return view.Actions, err
	}
	actions, err := listOwner()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(actions, func(a authorizationAction) bool {
		return a.ID == action.ID || strings.Contains(string(a.Parameters), canary)
	}) {
		return errors.New("private confirmation disclosed before its separate sharing permission")
	}
	if err := f.d.authorizationReject("/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString())); err != nil {
		return err
	}
	if err := f.sideEffects(token, 0); err != nil {
		return err
	}
	if err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.requester.token, actionDecision(action, "share_confirmation", uuid.NewString()), nil); err != nil {
		return err
	}
	actions, err = listOwner()
	if err != nil {
		return err
	}
	shared := slices.IndexFunc(actions, func(a authorizationAction) bool { return a.ID == action.ID })
	if shared < 0 || !actions[shared].CanApprove || !strings.Contains(string(actions[shared].Parameters), canary) {
		return errors.New("explicit exact-action disclosure did not reach the authorized resource owner")
	}
	if err := f.d.runtimeReject(http.MethodGet, "/runs/"+run.ID.String(), f.owner, nil, http.StatusForbidden); err != nil {
		return err
	}
	if err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if run.Status != "succeeded" || run.MessageID != nil || !strings.Contains(run.ResultText, canary) {
		return errors.New("private approved action did not preserve requester-only final result")
	}
	if err := f.sideEffects(token, 1); err != nil {
		return err
	}
	if err := f.d.runtimeReject(http.MethodGet, "/runs/"+run.ID.String(), f.owner, nil, http.StatusForbidden); err != nil {
		return err
	}
	return f.record("private_action_approved", run, &action, token)
}

// The external model derives the action's text from a real cited chat source;
// neither the armed tool plan nor this client injects the canary into arguments.
func (f *authorizationFixture) acceptedActionSources() error {
	type actionView struct {
		authorizationAction
		CanShareConfirmation bool `json:"can_share_confirmation"`
	}
	for _, phase := range []string{"inference", "pending", "completed"} {
		for _, operation := range []string{"edit", "personal_delete", "recall", "delete_all"} {
			label := phase + "_" + operation
			token := "AUTH_SOURCE_" + strings.ToUpper(label) + "_" + f.suffix
			canary := "CANARY_ACTION_SOURCE_" + strings.ToUpper(label) + "_" + f.suffix
			source, err := f.d.sendMessage(f.admin, f.conv, canary, 0)
			if err != nil {
				return err
			}
			if err = f.arm(token, []map[string]any{{"name": "fixture_auth_send", "from_message_id": source.MessageID,
				"arguments": map[string]string{"target": "accepted-source-inbox", "text": token}}}, phase == "inference"); err != nil {
				return err
			}
			run, err := f.start(token, map[string]any{"visibility": "personal", "message_ids": []entityID{source.MessageID}})
			if err != nil {
				return err
			}
			if phase == "inference" {
				if err = f.d.broadcastPoll("source-derived inference held", func(*driver) (bool, error) {
					observed, err := f.observe(token)
					return observed.Held, err
				}); err != nil {
					return err
				}
				if err = f.d.mutateWorkSource(f.admin, f.requester, source, operation); err != nil {
					return err
				}
				if err = f.release(token); err != nil {
					return err
				}
			}
			var action actionView
			if err = f.d.broadcastPoll("accepted source action awaits exact approval", func(bounded *driver) (bool, error) {
				var actions []actionView
				err := bounded.request(http.MethodPost, "/bot-authorizations/actions", f.requester.token, map[string]any{"op": "list", "run_id": run.ID}, &actions)
				if len(actions) == 1 {
					action = actions[0]
				}
				return len(actions) == 1 && action.Status == "pending", err
			}); err != nil {
				return err
			}
			if !strings.Contains(string(action.Parameters), canary) || action.CanApprove || !action.CanShareConfirmation {
				return errors.New("accepted-source: pending private arguments or separate confirmation permission lost")
			}
			if err = f.sideEffects(token, 0); err != nil {
				return err
			}
			if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.requester.token,
				actionDecision(action.authorizationAction, "share_confirmation", uuid.NewString()), nil); err != nil {
				return err
			}
			if phase == "completed" {
				if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token,
					actionDecision(action.authorizationAction, "approve", uuid.NewString()), nil); err != nil {
					return err
				}
				run, err = f.d.waitRun(f.requester, run.ID)
				if err != nil {
					return err
				}
			}
			if phase != "inference" {
				if err = f.d.mutateWorkSource(f.admin, f.requester, source, operation); err != nil {
					return err
				}
			}
			// The resource owner's narrow surface must preserve separately shared
			// accepted parameters; reading the Run first must not be necessary.
			var ownerView struct {
				Actions []actionView `json:"actions"`
			}
			if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token,
				map[string]any{"op": "list", "bot_id": f.bot.ID, "conv_id": f.conv}, &ownerView); err != nil {
				return err
			}
			shared := slices.IndexFunc(ownerView.Actions, func(a actionView) bool { return a.ID == action.ID })
			if shared < 0 || !strings.Contains(string(ownerView.Actions[shared].Parameters), canary) || phase != "completed" && !ownerView.Actions[shared].CanApprove {
				return errors.New("accepted-source: IM operation revoked exact confirmation disclosure/approval")
			}
			if phase != "completed" {
				current, err := f.d.readRun(f.requester, run.ID)
				if err != nil {
					return err
				}
				if current.Status != "waiting_authorization" || current.Generation != action.Generation || current.InputVersion != action.InputVersion {
					return errors.New("accepted-source: IM operation stopped or changed work awaiting approval")
				}
				if err = f.sideEffects(token, 0); err != nil {
					return err
				}
				if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token,
					actionDecision(action.authorizationAction, "approve", uuid.NewString()), nil); err != nil {
					return err
				}
			}
			run, err = f.d.waitRun(f.requester, run.ID)
			if err != nil {
				return err
			}
			if run.Status != "succeeded" || run.MessageID != nil || !strings.Contains(run.ResultText, canary) || strings.Contains(run.ResultText, "EDITED_NOT_ACCEPTED_") {
				return errors.New("accepted-source: independent personal result no longer contains original accepted body")
			}
			var actionRaw json.RawMessage
			if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.requester.token,
				map[string]any{"op": "list", "run_id": run.ID}, &actionRaw); err != nil {
				return err
			}
			var completed []actionView
			if err = json.Unmarshal(actionRaw, &completed); err != nil {
				return err
			}
			if len(completed) != 1 || completed[0].ID != action.ID || completed[0].Status != "succeeded" || !strings.Contains(string(actionRaw), "external-result:") || !strings.Contains(string(actionRaw), canary) {
				return errors.New("accepted-source: completed external-action fact/arguments/result were cleared")
			}
			observed, err := f.observe(token)
			if err != nil {
				return err
			}
			if len(observed.Calls) != 1 || observed.Calls[0].Name != "fixture_auth_send" || observed.Calls[0].Arguments["text"] != token+"|"+source.Content.Text {
				return errors.New("accepted-source: actual MCP did not act once on the original accepted body")
			}
			if operation != "edit" {
				if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.requester.token, map[string]any{
					"conv_id": f.conv, "submission_key": uuid.NewString(), "message": token + "_DENIED", "message_ids": []entityID{source.MessageID}}); err != nil {
					return err
				}
			}
			if operation == "edit" && phase == "completed" {
				// New explicit references read the current message, not an older work copy.
				current, err := f.d.identityMessage(f.requester, source.MessageID)
				if err != nil {
					return err
				}
				freshToken := token + "_CURRENT"
				if err = f.arm(freshToken, nil, false); err != nil {
					return err
				}
				fresh, err := f.start(freshToken, map[string]any{"visibility": "personal", "message_ids": []entityID{source.MessageID}})
				if err != nil {
					return err
				}
				fresh, err = f.d.waitRun(f.requester, fresh.ID)
				if err != nil {
					return err
				}
				if fresh.Status != "succeeded" || !strings.Contains(fresh.ResultText, current.Text.Text) || strings.Contains(fresh.ResultText, canary) {
					return errors.New("accepted-source: new acceptance reused stale accepted message body")
				}
			}
			if err = f.d.runtimeReject(http.MethodGet, "/runs/"+run.ID.String(), f.owner, nil, http.StatusForbidden); err != nil {
				return err
			}
			if err = f.record("accepted_source_action_"+label, run, &completed[0].authorizationAction, token); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *authorizationFixture) escapedCredentialAdmission(address string) error {
	secret := "fixture&key\"value"
	auth, err := json.Marshal(map[string]string{"token": secret})
	if err != nil {
		return err
	}
	serverPath := "/mcp-servers/" + f.server.String()
	if err := f.d.request(http.MethodPut, serverPath, f.owner.token, map[string]string{"auth_config": string(auth)}, nil); err != nil {
		return err
	}
	defer f.d.request(http.MethodPut, serverPath, f.owner.token, map[string]string{"auth_config": "{}"}, nil)
	x, err := f.d.websocketExecutor(f.owner, f.external.ID, address, "escaped-credential-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	run, err := f.d.startRun(f.owner, f.external.ID, f.conv, "AUTH_CREDENTIAL_ADMISSION_"+f.suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	request, err := x.request(run.ID)
	if err != nil {
		return err
	}
	if _, err := x.update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	nested, _ := json.Marshal(map[string]string{"value": secret})
	for _, value := range []string{secret, string(nested)} {
		arguments := map[string]string{"target": "controlled-inbox", "text": value}
		body := map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": arguments}
		if err := f.d.authorizationReject("/bots/"+f.external.ID.String()+"/executor/actions", x.token, body); err != nil {
			return err
		}
		if err := f.d.authorizationReject("/bot-authorizations/actions", f.owner.token, map[string]any{"op": "grant", "bot_id": f.bot.ID, "conv_id": f.conv, "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": arguments, "expires_at": time.Now().Add(time.Minute).Unix()}); err != nil {
			return err
		}
	}
	var actions []authorizationAction
	if err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
		return err
	}
	if len(actions) != 0 {
		return errors.New("escaped transient credentials entered durable action records")
	}
	resultToken := "AUTH_CREDENTIAL_RESULT_" + f.suffix
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	var action authorizationAction
	if err := f.d.request(http.MethodPost, path, x.token, map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_read", "arguments": map[string]string{"target": "safe", "text": resultToken}}, &action); err != nil {
		return err
	}
	var result struct {
		Result string `json:"result"`
	}
	if err := f.d.request(http.MethodPost, path, x.token, actionDecision(action, "execute", uuid.NewString()), &result); err != nil {
		return err
	}
	var envelope struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(result.Result), &envelope); err != nil || len(envelope.Content) != 1 {
		return errors.New("real credential-result tool did not return its consumer contract")
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(envelope.Content[0].Text), &values); err != nil || values["value"] != "[redacted]" {
		return errors.New("escaped credential value leaked from actual tool result")
	}
	if _, leaked := values[secret]; leaked {
		return errors.New("credential-bearing JSON property leaked from actual tool result")
	}
	if err := f.sideEffects(resultToken, 1); err != nil {
		return err
	}
	return nil
}

func (f *authorizationFixture) exactNumericAction(address string) error {
	x, err := f.d.websocketExecutor(f.owner, f.external.ID, address, "numeric-action-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	run, err := f.d.startRun(f.owner, f.external.ID, f.conv, "AUTH_NUMERIC_ACTION_"+f.suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	request, err := x.request(run.ID)
	if err != nil {
		return err
	}
	if _, err := x.update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	token := "AUTH_NUMERIC_TOOL_" + f.suffix
	var action authorizationAction
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	if err := f.d.request(http.MethodPost, path, x.token, map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": map[string]any{"target": "controlled-number-ledger", "text": token, "amount": json.Number("9007199254740993")}}, &action); err != nil {
		return err
	}
	var actions []authorizationAction
	if err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
		return err
	}
	index := slices.IndexFunc(actions, func(a authorizationAction) bool { return a.ID == action.ID })
	if index < 0 || !strings.Contains(actions[index].ParametersJSON, `"amount":9007199254740993`) {
		return errors.New("authoritative confirmation display rounded the exact approved amount")
	}
	if err := f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	if err := f.d.request(http.MethodPost, path, x.token, actionDecision(action, "execute", uuid.NewString()), nil); err != nil {
		return err
	}
	var observation struct {
		Calls []struct {
			Arguments map[string]json.RawMessage `json:"arguments"`
		} `json:"calls"`
	}
	if err := f.d.executorProvider("/authorization/observe", map[string]string{"token": token}, &observation); err != nil {
		return err
	}
	if len(observation.Calls) != 1 || string(observation.Calls[0].Arguments["amount"]) != "9007199254740993" {
		return errors.New("actual external action rounded approved numeric parameters")
	}
	return nil
}
