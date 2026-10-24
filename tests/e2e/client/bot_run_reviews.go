package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// These fixed-provider checks prove public contracts, not model quality.
// No private table, runtime function, Docker socket or candidate script is used.
type runReviewPolicyView struct {
	Enabled   bool   `json:"enabled"`
	CanManage bool   `json:"can_manage"`
	ScopeKey  string `json:"scope_key"`
	Revision  int64  `json:"revision"`
}

type runReviewCandidateView struct {
	ID                   entityID          `json:"id"`
	SkillID              entityID          `json:"skill_id"`
	SourceRunID          entityID          `json:"source_run_id"`
	PublisherID          entityID          `json:"publisher_id"`
	ScopeKey             string            `json:"scope_key"`
	Intent               string            `json:"intent"`
	TargetGoal           string            `json:"target_goal"`
	Applicability        string            `json:"applicability"`
	Rationale            string            `json:"rationale"`
	Instructions         string            `json:"instructions"`
	BaselineInstructions string            `json:"baseline_instructions"`
	Resources            map[string]string `json:"resources"`
	Scripts              map[string]string `json:"scripts"`
	ImageDigest          string            `json:"image_digest"`
	EntryPoint           string            `json:"entry_point"`
	Digest               string            `json:"digest"`
	BaselineVersionID    *entityID         `json:"baseline_version_id"`
	Status               string            `json:"status"`
	CodeReviewRequired   bool              `json:"code_review_required"`
	EvidenceDigest       string            `json:"evidence_digest"`
	ExpiresAt            int64             `json:"expires_at"`
}

type runReviewView struct {
	ID           entityID                `json:"id"`
	RunID        entityID                `json:"run_id"`
	BotID        entityID                `json:"bot_id"`
	ConvID       entityID                `json:"conv_id"`
	ScopeKey     string                  `json:"scope_key"`
	Status       string                  `json:"status"`
	Reason       string                  `json:"reason"`
	ModelCalls   int                     `json:"model_calls"`
	ModelVersion string                  `json:"model_version"`
	CreatedAt    int64                   `json:"created_at"`
	UpdatedAt    int64                   `json:"updated_at"`
	ExpiresAt    int64                   `json:"expires_at"`
	CandidateID  *entityID               `json:"candidate_id"`
	Candidate    *runReviewCandidateView `json:"candidate"`
}

type runReviewObservation struct {
	Held       string `json:"held"`
	Executions int    `json:"executions"`
	Requests   []struct {
		Phase string         `json:"phase"`
		Body  map[string]any `json:"body"`
	} `json:"requests"`
}

type runReviewFixture struct {
	recoveryFixture
	sources        map[entityID]string
	approvalServer entityID
}

func (f *runReviewFixture) path(resource string) string {
	return "/bots/" + f.bot.ID.String() + "/" + resource
}

func (f *runReviewFixture) scope(resource, visibility string) string {
	return f.path(resource) + "?conv_id=" + f.conv.String() + "&visibility=" + visibility
}

func (f *runReviewFixture) policy(caller account, visibility string) (runReviewPolicyView, error) {
	var result runReviewPolicyView
	err := f.d.request(http.MethodGet, f.scope("review-policy", visibility), caller.token, nil, &result)
	return result, err
}

func (f *runReviewFixture) setPolicy(caller account, visibility string, enabled bool, key string) (runReviewPolicyView, error) {
	var result runReviewPolicyView
	err := f.d.request(http.MethodPut, f.path("review-policy"), caller.token, map[string]any{
		"conv_id": f.conv, "visibility": visibility, "enabled": enabled, "submission_key": key,
	}, &result)
	return result, err
}

func (f *runReviewFixture) list(caller account, visibility string) ([]runReviewView, error) {
	var result []runReviewView
	err := f.d.request(http.MethodGet, f.scope("run-reviews", visibility), caller.token, nil, &result)
	return result, err
}

func (f *runReviewFixture) detail(caller account, id entityID) (runReviewView, error) {
	var result runReviewView
	err := f.d.request(http.MethodGet, f.path("run-reviews/"+id.String()), caller.token, nil, &result)
	return result, err
}

func (f *runReviewFixture) arm(label, mode string, target entityID) (string, error) {
	token := "RUN_REVIEW_" + label + "_" + f.suffix
	var armed struct {
		OK   bool   `json:"ok"`
		Mode string `json:"mode"`
	}
	err := f.d.executorProvider("/run-reviews/arm", map[string]string{
		"token": token, "mode": mode, "target_version_id": target.String(),
		"callback_url": f.d.provider + "/run-reviews/executed",
	}, &armed)
	if err == nil && (!armed.OK || armed.Mode != mode) {
		err = fmt.Errorf("run-reviews: provider did not arm mode %q (got %q, ok=%t); rebuild the provider fixture image", mode, armed.Mode, armed.OK)
	}
	return token, err
}

func (f *runReviewFixture) observe(token, operation string) (runReviewObservation, error) {
	var result runReviewObservation
	err := f.d.executorProvider("/run-reviews/"+operation, map[string]string{"token": token}, &result)
	return result, err
}

func (f *runReviewFixture) start(token, visibility, key string, versions []entityID) (publicRun, error) {
	var run publicRun
	body := map[string]any{"conv_id": f.conv, "visibility": visibility, "submission_key": key, "message": token}
	if len(versions) != 0 {
		body["skill_version_ids"] = versions
	}
	err := f.d.request(http.MethodPost, f.path("runs"), f.requester.token, body, &run)
	if err == nil {
		f.sources[run.ID] = visibility
	}
	return run, err
}

func reviewTerminal(status string) bool {
	return slices.Contains([]string{"no_candidate", "candidate_generated", "failed", "over_budget", "cancelled"}, status)
}

func (f *runReviewFixture) waitReview(run publicRun, visibility, status string) (runReviewView, error) {
	deadline := time.Now().Add(f.d.timeout)
	var current runReviewView
	for time.Now().Before(deadline) {
		rows, err := f.list(f.requester, visibility)
		if err != nil {
			return current, err
		}
		matches := 0
		for _, row := range rows {
			if row.RunID == run.ID {
				current = row
				matches++
			}
		}
		if matches > 1 {
			return current, fmt.Errorf("run-reviews: Run %s has %d reviews", run.ID, matches)
		}
		if matches == 1 && current.Status == status {
			return f.detail(f.requester, current.ID)
		}
		if matches == 1 && reviewTerminal(current.Status) {
			return current, fmt.Errorf("run-reviews: expected %s, got %s (%s)", status, current.Status, current.Reason)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return current, fmt.Errorf("run-reviews: Run %s did not reach %s; last=%s", run.ID, status, current.Status)
}

func (f *runReviewFixture) unchanged(run publicRun) error {
	current, err := f.d.getRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if current.Status != run.Status || current.OutputStatus != run.OutputStatus || current.ResultText != run.ResultText ||
		current.Error != run.Error || !reflect.DeepEqual(current.MessageID, run.MessageID) {
		return fmt.Errorf("run-reviews: background review changed original Run %s", run.ID)
	}
	return nil
}

func (f *runReviewFixture) calls(token string, phases ...string) error {
	observed, err := f.observe(token, "observe")
	if err != nil {
		return err
	}
	var actual []string
	for _, request := range observed.Requests {
		if request.Phase == "run" {
			continue
		}
		actual = append(actual, request.Phase)
		if request.Body["model"] != "fixture-run-review-"+f.suffix {
			return errors.New("run-reviews: background review did not use the Bot model")
		}
		if tools, ok := request.Body["tools"].([]any); ok && len(tools) != 0 {
			return errors.New("run-reviews: background review advertised executable tools")
		}
	}
	if !slices.Equal(actual, phases) || len(actual) > 2 || observed.Executions != 0 {
		return fmt.Errorf("run-reviews: unexpected phases/execution: phases=%v expected=%v executions=%d", actual, phases, observed.Executions)
	}
	return nil
}

func (f *runReviewFixture) candidate(review runReviewView, run publicRun, intent, token string) error {
	candidate := review.Candidate
	if review.ID == "" || review.RunID != run.ID || review.BotID != f.bot.ID || review.ConvID != f.conv || review.ScopeKey == "" ||
		review.ModelCalls != 2 || review.ModelVersion == "" || review.CreatedAt <= 0 || review.UpdatedAt < review.CreatedAt || review.ExpiresAt <= review.CreatedAt {
		return fmt.Errorf("run-reviews: incomplete review identity/budget/retention: %+v", review)
	}
	if candidate == nil || review.CandidateID == nil || *review.CandidateID != candidate.ID || candidate.ID == "" || candidate.SkillID == "" ||
		candidate.SourceRunID != run.ID || candidate.PublisherID == "" || candidate.ScopeKey != review.ScopeKey || candidate.Intent != intent ||
		candidate.Status != "pending_validation" || candidate.Digest == "" || candidate.EvidenceDigest == "" || candidate.ExpiresAt <= review.CreatedAt ||
		candidate.TargetGoal != "Summarize fixed rows" || candidate.Applicability != "Fixed CSV input only" || candidate.Rationale == "" ||
		!strings.Contains(candidate.Instructions, token) || candidate.Resources["references/format.md"] != "Report a literal total." {
		return fmt.Errorf("run-reviews: candidate lost provenance, applicability, instructions or evidence: %+v", candidate)
	}
	if intent == "create" && (candidate.BaselineVersionID != nil || candidate.BaselineInstructions != "") {
		return errors.New("run-reviews: create candidate unexpectedly changes a baseline")
	}
	return nil
}

func (f *runReviewFixture) reject(err error, label string) error {
	var api *apiError
	if !errors.As(err, &api) || api.status < 400 || api.status >= 500 {
		return fmt.Errorf("run-reviews: %s must fail with explicit 4xx, got %v", label, err)
	}
	return nil
}

func (f *runReviewFixture) pendingOnly(review runReviewView) error {
	id := review.Candidate.ID
	for _, operation := range []string{"evaluate", "approve", "publish"} {
		err := f.d.request(http.MethodPost, f.path("skills/"+id.String()+"/"+operation), f.owner.token,
			map[string]any{"submission_key": uuid.NewString()}, nil)
		if err = f.reject(err, "scope candidate via legacy "+operation); err != nil {
			return err
		}
	}
	err := f.d.request(http.MethodPost, f.path("skill-versions/"+id.String()+"/enable"), f.owner.token,
		map[string]any{"enabled": true, "submission_key": uuid.NewString()}, nil)
	if err = f.reject(err, "unverified candidate enable"); err != nil {
		return err
	}
	for _, caller := range []account{f.owner, f.requester} {
		err = f.d.request(http.MethodGet, f.path("skills/"+id.String()), caller.token, nil, nil)
		if err = f.reject(err, "scope material via legacy candidate detail"); err != nil {
			return err
		}
	}
	var packages []skillPackageView
	if err = f.d.request(http.MethodGet, f.path("skills"), f.owner.token, nil, &packages); err != nil {
		return err
	}
	for _, pkg := range packages {
		for _, candidate := range pkg.Candidates {
			if candidate.ID == id {
				return errors.New("run-reviews: scoped candidate leaked into legacy package list")
			}
		}
	}
	current, err := f.detail(f.requester, review.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current.Candidate, review.Candidate) {
		return errors.New("run-reviews: denied legacy governance changed candidate")
	}
	return nil
}

func (f *runReviewFixture) noReview(run publicRun, visibility string, cancelledAllowed bool) error {
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		rows, err := f.list(f.requester, visibility)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.RunID == run.ID && (!cancelledAllowed || row.Status != "cancelled" || row.ModelCalls != 0 || row.CandidateID != nil) {
				return fmt.Errorf("run-reviews: non-reviewable Run %s created review %s (%s)", run.ID, row.ID, row.Status)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func (d *driver) botRunReviews() error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := runReviewFixture{recoveryFixture: recoveryFixture{authorizationFixture: authorizationFixture{d: d, suffix: suffix}}}
	f.sources = make(map[entityID]string)
	defer func() {
		if f.approvalServer != "" {
			_ = f.d.request(http.MethodDelete, f.path("mcp-servers/"+f.approvalServer.String()), f.owner.token, nil, nil)
		}
	}()
	for _, item := range []struct {
		name   string
		target *account
	}{{"owner", &f.owner}, {"requester", &f.requester}, {"admin", &f.admin}} {
		*item.target, err = d.register("run_review_"+item.name, suffix)
		if err != nil {
			return err
		}
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{
		"type": "group", "group_name": "run-reviews-" + suffix, "member_ids": []entityID{f.owner.id, f.requester.id},
	}, &conv); err != nil {
		return err
	}
	f.conv = conv.ID
	if f.model, err = d.fixtureModel(f.owner, "chat", "fixture-run-review-"+suffix); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{
		"name": "run-reviews-" + suffix, "type": "self_deployed", "model_id": f.model,
		"streaming_enabled": false, "response_triggers": []string{"mention"}, "max_step": 4,
	}, &f.bot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
		return err
	}
	if err = f.defaultPolicies(); err != nil {
		return err
	}
	for _, item := range []struct {
		label, mode, status string
		calls               int
	}{
		{"valuable", "valuable", "candidate_generated", 2},
		{"simple", "no-value", "no_candidate", 1},
		{"review-error", "provider-failed", "failed", 1},
		{"review-budget", "over-budget", "over_budget", 1},
		{"original-failed", "run-failed", "candidate_generated", 2},
		{"script", "script", "candidate_generated", 2},
	} {
		token, err := f.arm(item.label, item.mode, "")
		if err != nil {
			return err
		}
		key := uuid.NewString()
		accepted, err := f.start(token, "conversation", key, nil)
		if err != nil {
			return err
		}
		run, err := d.waitRun(f.requester, accepted.ID)
		if err != nil {
			return err
		}
		if item.mode == "run-failed" {
			if run.Status != "failed" || run.MessageID != nil {
				return fmt.Errorf("run-reviews: original failure was not preserved: %+v", run)
			}
		} else if run.Status != "succeeded" || run.ResultText != "RUN_REVIEW_RESULT:"+token || run.OutputStatus != "committed" {
			return fmt.Errorf("run-reviews: unexpected original result: %+v", run)
		}
		if len(run.FrozenSkills) != 0 {
			return errors.New("run-reviews: pending candidates appeared in original Run's executable skill catalog")
		}
		review, err := f.waitReview(run, "conversation", item.status)
		if err != nil {
			return err
		}
		if review.ModelCalls != item.calls || item.status != "candidate_generated" && review.Reason == "" {
			return fmt.Errorf("run-reviews: missing reason or incorrect model budget: %+v", review)
		}
		if item.status == "candidate_generated" {
			if err = f.candidate(review, run, "create", token); err != nil {
				return err
			}
			if err = f.pendingOnly(review); err != nil {
				return err
			}
			if item.mode == "script" {
				if !review.Candidate.CodeReviewRequired || len(review.Candidate.Scripts) != 1 ||
					!strings.Contains(review.Candidate.Scripts["scripts/review.sh"], "UNREVIEWED_SCRIPT_EXECUTED") {
					return errors.New("run-reviews: script material was lost or not marked for trusted code review")
				}
			} else if review.Candidate.CodeReviewRequired || len(review.Candidate.Scripts) != 0 {
				return errors.New("run-reviews: pure instruction candidate acquired script execution material")
			}
		} else if review.Candidate != nil || review.CandidateID != nil {
			return errors.New("run-reviews: non-candidate terminal state contains candidate")
		}
		// Replaying the original submission and repeated reads must not queue a
		// second review, consume another model call or mint another candidate.
		replayed, err := f.start(token, "conversation", key, nil)
		if err != nil || replayed.ID != run.ID {
			return fmt.Errorf("run-reviews: original submission did not replay: %v", err)
		}
		for range 3 {
			repeated, err := f.waitReview(run, "conversation", item.status)
			if err != nil {
				return err
			}
			if repeated.ID != review.ID || !reflect.DeepEqual(repeated.CandidateID, review.CandidateID) {
				return errors.New("run-reviews: repeat terminal/read generated another review or candidate")
			}
		}
		phases := []string{"assess"}
		if item.calls == 2 {
			phases = append(phases, "extract")
		}
		if err = f.calls(token, phases...); err != nil {
			return err
		}
		if err = f.unchanged(run); err != nil {
			return err
		}
		if run.Status == "succeeded" {
			if err = f.uniqueMessage(run); err != nil {
				return err
			}
		}
	}
	if err = f.waitingAndStopped(); err != nil {
		return err
	}
	if err = f.personalScope(); err != nil {
		return err
	}
	if err = f.cancellation(); err != nil {
		return err
	}
	if err = f.modifyCandidate(); err != nil {
		return err
	}
	if err = f.complete(); err != nil {
		return err
	}
	return f.candidateGovernance()
}

func (f *runReviewFixture) defaultPolicies() error {
	conversation, err := f.policy(f.admin, "conversation")
	if err != nil {
		return err
	}
	personal, err := f.policy(f.requester, "personal")
	if err != nil {
		return err
	}
	member, err := f.policy(f.requester, "conversation")
	if err != nil {
		return err
	}
	if !conversation.Enabled || !conversation.CanManage || !personal.Enabled || !personal.CanManage ||
		conversation.ScopeKey == "" || personal.ScopeKey == "" || conversation.ScopeKey == personal.ScopeKey || member.CanManage || member.ScopeKey != conversation.ScopeKey {
		return errors.New("run-reviews: default enabled policies or scope-specific management are incorrect")
	}
	for _, caller := range []account{f.owner, f.requester} {
		_, err = f.setPolicy(caller, "conversation", false, uuid.NewString())
		if err = f.d.expectStatus(err, http.StatusForbidden, "non-admin conversation policy"); err != nil {
			return err
		}
	}
	unchanged, err := f.policy(f.admin, "conversation")
	if err != nil {
		return err
	}
	if unchanged != conversation {
		return errors.New("run-reviews: denied policy update changed policy")
	}
	return f.policySubmissionReplay()
}

func (f *runReviewFixture) policySubmissionReplay() error {
	for _, scope := range []struct {
		caller     account
		visibility string
	}{{f.admin, "conversation"}, {f.requester, "personal"}} {
		disableKey, enableKey := uuid.NewString(), uuid.NewString()
		disabled, err := f.setPolicy(scope.caller, scope.visibility, false, disableKey)
		if err != nil {
			return err
		}
		enabled, err := f.setPolicy(scope.caller, scope.visibility, true, enableKey)
		if err != nil {
			return err
		}
		if disabled.Enabled || !enabled.Enabled || disabled.ScopeKey != enabled.ScopeKey || enabled.Revision <= disabled.Revision {
			return errors.New("run-reviews: distinct policy submissions did not produce ordered updates")
		}
		replayed, err := f.setPolicy(scope.caller, scope.visibility, false, disableKey)
		if err != nil {
			return err
		}
		if replayed != disabled {
			return errors.New("run-reviews: same policy submission did not replay its original receipt")
		}
		current, err := f.policy(scope.caller, scope.visibility)
		if err != nil {
			return err
		}
		if current != enabled {
			return errors.New("run-reviews: replaying an old disable submission reverted a newer enabled policy")
		}
		_, err = f.setPolicy(scope.caller, scope.visibility, true, disableKey)
		if err = f.d.expectStatus(err, http.StatusConflict, "policy submission key reused with changed enabled"); err != nil {
			return err
		}
		current, err = f.policy(scope.caller, scope.visibility)
		if err != nil {
			return err
		}
		if current != enabled {
			return errors.New("run-reviews: rejected policy identity conflict changed policy or revision")
		}
	}
	return nil
}

func (f *runReviewFixture) waitingAndStopped() error {
	advanced, err := json.Marshal(map[string]any{"timeout": 10, "retry_count": 0, "tool_policies": map[string]any{
		"fixture_auth_send": map[string]any{"risk": "send", "target_parameter": "target", "can_share": true, "conversation_ids": []entityID{f.conv}},
	}})
	if err != nil {
		return err
	}
	var server struct {
		ID entityID `json:"id"`
	}
	if err = f.d.request(http.MethodPost, "/mcp-servers", f.owner.token, map[string]any{
		"name": "run-review-approval-" + f.suffix, "transport": "http-streamable", "url": f.d.provider + "/authorization/mcp",
		"enabled": true, "advanced_config": string(advanced),
	}, &server); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, f.path("mcp-servers"), f.owner.token, map[string]any{"mcp_server_id": server.ID}, nil); err != nil {
		return err
	}
	// Keep source-tool authorization through final provenance checks; clean up after the whole journey.
	f.approvalServer = server.ID
	if err = f.waitingStoppedCase("waiting-stopped", "waiting", "waiting_input"); err != nil {
		return err
	}
	return f.waitingStoppedCase("waiting-auth-stopped", "waiting-approval", "waiting_authorization")
}

func (f *runReviewFixture) waitingStoppedCase(label, mode, status string) error {
	token, err := f.arm(label, mode, "")
	if err != nil {
		return err
	}
	run, err := f.start(token, "conversation", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.requester, run.ID, status); err != nil {
		return err
	}
	if err = f.noReview(run, "conversation", false); err != nil {
		return err
	}
	if err = f.calls(token); err != nil {
		return err
	}
	key := uuid.NewString()
	if _, err = f.d.interactionControl(f.requester, run.ID, "stop", "", key, nil); err != nil {
		return err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if run.Status != "stopped" || run.MessageID != nil {
		return errors.New("run-reviews: waiting Run did not reach confirmed stopped terminal")
	}
	review, err := f.waitReview(run, "conversation", "candidate_generated")
	if err != nil {
		return err
	}
	if err = f.candidate(review, run, "create", token); err != nil {
		return err
	}
	if _, err = f.d.interactionControl(f.requester, run.ID, "stop", "", key, nil); err != nil {
		return err
	}
	repeated, err := f.waitReview(run, "conversation", "candidate_generated")
	if err != nil || repeated.ID != review.ID || !reflect.DeepEqual(repeated.CandidateID, review.CandidateID) {
		return fmt.Errorf("run-reviews: repeated stop duplicated terminal review: %v", err)
	}
	if err = f.calls(token, "assess", "extract"); err != nil {
		return err
	}
	var observed struct {
		Calls []json.RawMessage `json:"calls"`
	}
	if err = f.d.executorProvider("/authorization/observe", map[string]string{"token": token}, &observed); err != nil {
		return err
	}
	if len(observed.Calls) != 0 {
		return errors.New("run-reviews: waiting authorization executed an unapproved external action")
	}
	return f.unchanged(run)
}

func (f *runReviewFixture) personalScope() error {
	token, err := f.arm("personal", "valuable", "")
	if err != nil {
		return err
	}
	run, err := f.start(token, "personal", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	review, err := f.waitReview(run, "personal", "candidate_generated")
	if err != nil {
		return err
	}
	if err = f.candidate(review, run, "create", token); err != nil {
		return err
	}
	for _, caller := range []account{f.owner, f.admin} {
		_, err = f.detail(caller, review.ID)
		if err = f.d.expectStatus(err, http.StatusForbidden, "another user's personal review"); err != nil {
			return err
		}
		policy, err := f.policy(caller, "personal")
		if err != nil {
			return err
		}
		if policy.ScopeKey == review.ScopeKey {
			return errors.New("run-reviews: personal policy selected another user's scope")
		}
		rows, err := f.list(caller, "personal")
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ID == review.ID || row.RunID == run.ID {
				return errors.New("run-reviews: another user's personal list leaked review material")
			}
		}
	}
	if err = f.pendingOnly(review); err != nil {
		return err
	}
	return f.calls(token, "assess", "extract")
}

func (f *runReviewFixture) held(token string) error {
	deadline := time.Now().Add(f.d.timeout)
	for time.Now().Before(deadline) {
		observed, err := f.observe(token, "observe")
		if err != nil {
			return err
		}
		if observed.Held == "assess" {
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return errors.New("run-reviews: provider never observed held background assessment")
}

func (f *runReviewFixture) cancellation() error {
	for _, disable := range []bool{false, true} {
		label := "explicit-cancel"
		if disable {
			label = "disable-cancel"
		}
		token, err := f.arm(label, "hold", "")
		if err != nil {
			return err
		}
		// Always release the external gate, including assertion failures.
		defer f.observe(token, "release")
		run, err := f.start(token, "conversation", uuid.NewString(), nil)
		if err != nil {
			return err
		}
		run, err = f.d.waitRun(f.requester, run.ID)
		if err != nil {
			return err
		}
		if run.Status != "succeeded" || run.ResultText != "RUN_REVIEW_RESULT:"+token {
			return errors.New("run-reviews: review gate blocked original result commitment")
		}
		if err = f.held(token); err != nil {
			return err
		}
		review, err := f.waitReview(run, "conversation", "assessing")
		if err != nil {
			return err
		}
		if disable {
			before, err := f.policy(f.admin, "conversation")
			if err != nil {
				return err
			}
			key := uuid.NewString()
			policy, err := f.setPolicy(f.admin, "conversation", false, key)
			if err != nil {
				return err
			}
			if policy.Enabled || !policy.CanManage || policy.ScopeKey != before.ScopeKey || policy.Revision <= before.Revision {
				return errors.New("run-reviews: disabling policy lost scope/revision")
			}
			replayed, err := f.setPolicy(f.admin, "conversation", false, key)
			if err != nil || replayed != policy {
				return fmt.Errorf("run-reviews: policy retry changed revision: %v", err)
			}
		} else {
			key := uuid.NewString()
			for range 2 {
				var result runReviewView
				if err = f.d.request(http.MethodPost, f.path("run-reviews/"+review.ID.String()+"/cancel"), f.requester.token,
					map[string]any{"submission_key": key}, &result); err != nil {
					return err
				}
				if result.ID != review.ID || result.Status != "cancelled" || result.CandidateID != nil {
					return errors.New("run-reviews: cancellation did not replay the same cancelled review")
				}
			}
		}
		if _, err = f.waitReview(run, "conversation", "cancelled"); err != nil {
			return err
		}
		if _, err = f.observe(token, "release"); err != nil {
			return err
		}
		settleUntil := time.Now().Add(600 * time.Millisecond)
		for time.Now().Before(settleUntil) {
			cancelled, err := f.detail(f.requester, review.ID)
			if err != nil {
				return err
			}
			if cancelled.Status != "cancelled" || cancelled.Candidate != nil || cancelled.CandidateID != nil {
				return errors.New("run-reviews: released late assessment escaped cancellation fence")
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err = f.unchanged(run); err != nil {
			return err
		}
		if err = f.calls(token, "assess"); err != nil {
			return err
		}
		if disable {
			disabledToken, err := f.arm("disabled-new-run", "valuable", "")
			if err != nil {
				return err
			}
			disabled, err := f.start(disabledToken, "conversation", uuid.NewString(), nil)
			if err != nil {
				return err
			}
			disabled, err = f.d.waitRun(f.requester, disabled.ID)
			if err != nil {
				return err
			}
			if disabled.Status != "succeeded" || disabled.ResultText != "RUN_REVIEW_RESULT:"+disabledToken {
				return errors.New("run-reviews: disabling review disabled original work")
			}
			disabledReview, err := f.waitReview(disabled, "conversation", "cancelled")
			if err != nil {
				return err
			}
			if disabledReview.ModelCalls != 0 || disabledReview.CandidateID != nil || disabledReview.Candidate != nil {
				return errors.New("run-reviews: disabled scope read material or generated candidate")
			}
			if err = f.noReview(disabled, "conversation", true); err != nil {
				return err
			}
			if err = f.calls(disabledToken); err != nil {
				return err
			}
			personal, err := f.policy(f.requester, "personal")
			if err != nil || !personal.Enabled {
				return fmt.Errorf("run-reviews: conversation disable changed independent personal policy: %v", err)
			}
			if _, err = f.setPolicy(f.admin, "conversation", true, uuid.NewString()); err != nil {
				return err
			}
			// Re-enabling must not backfill a Run which finished while disabled.
			if err = f.noReview(disabled, "conversation", true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *runReviewFixture) modifyCandidate() error {
	// Establish the baseline using the already-public manual skill lifecycle.
	// The assessment, not extraction output, chooses the target version.
	token, err := f.arm("baseline-evaluation", "valuable", "")
	if err != nil {
		return err
	}
	instructions := "---\nname: review-fixed-rows\ndescription: Summarize fixed rows deterministically\nlicense: MIT\n---\n\nCount input rows. BASELINE_" + f.suffix
	manual := skillFixture{recoveryFixture: f.recoveryFixture}
	candidate, err := manual.importPackage(instructions, "", "", token, "RUN_REVIEW_RESULT:"+token, uuid.NewString())
	if err != nil {
		return err
	}
	for _, operation := range []string{"evaluate", "approve"} {
		if _, err = manual.action(candidate.ID, operation); err != nil {
			return err
		}
	}
	raw, err := manual.action(candidate.ID, "publish")
	if err != nil {
		return err
	}
	var version skillVersionView
	if err = json.Unmarshal(raw, &version); err != nil || !version.Enabled || version.VersionID == "" {
		return fmt.Errorf("run-reviews: manual baseline was not published: %s (%v)", raw, err)
	}
	token, err = f.arm("modify", "valuable", version.VersionID)
	if err != nil {
		return err
	}
	run, err := f.start(token, "conversation", uuid.NewString(), []entityID{version.VersionID})
	if err != nil {
		return err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if run.Status != "succeeded" || len(run.FrozenSkills) != 1 || run.FrozenSkills[0].VersionID != version.VersionID || !run.FrozenSkills[0].Loaded {
		return errors.New("run-reviews: baseline was not frozen in original Run")
	}
	review, err := f.waitReview(run, "conversation", "candidate_generated")
	if err != nil {
		return err
	}
	if err = f.candidate(review, run, "modify", token); err != nil {
		return err
	}
	if !hasEntityID(review.Candidate.BaselineVersionID, version.VersionID) || !strings.Contains(review.Candidate.BaselineInstructions, "BASELINE_"+f.suffix) ||
		review.Candidate.Instructions == review.Candidate.BaselineInstructions || review.Candidate.SkillID != candidate.SkillID {
		return errors.New("run-reviews: modification did not bind exact baseline and readable before/after difference")
	}
	if err = f.pendingOnly(review); err != nil {
		return err
	}
	var catalog []availableSkillView
	if err = f.d.request(http.MethodGet, f.path("skill-catalog"), f.requester.token, nil, &catalog); err != nil {
		return err
	}
	if len(catalog) != 1 || catalog[0].VersionID != version.VersionID {
		return errors.New("run-reviews: unvalidated modification replaced enabled baseline")
	}
	if err = f.calls(token, "assess", "extract"); err != nil {
		return err
	}
	if err := f.unchanged(run); err != nil {
		return err
	}
	// An unused but authorized catalog version may be the matching baseline;
	// its presence must never be reported as actual use by the original Run.
	unusedToken, err := f.arm("modify-catalog-only", "catalog-only", version.VersionID)
	if err != nil {
		return err
	}
	unused, err := f.start(unusedToken, "conversation", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	unused, err = f.d.waitRun(f.requester, unused.ID)
	if err != nil {
		return err
	}
	if len(unused.FrozenSkills) != 1 || unused.FrozenSkills[0].Loaded {
		return errors.New("run-reviews: unused catalog baseline was falsely recorded as loaded")
	}
	unusedReview, err := f.waitReview(unused, "conversation", "candidate_generated")
	if err != nil {
		return err
	}
	if err := f.candidate(unusedReview, unused, "modify", unusedToken); err != nil {
		return err
	}
	if !hasEntityID(unusedReview.Candidate.BaselineVersionID, version.VersionID) {
		return errors.New("run-reviews: matching unused baseline was duplicated as a new skill")
	}
	return f.calls(unusedToken, "assess", "extract")
}

// Every public source Run has exactly one review. No evaluation, extraction,
// repeated read or legacy candidate denial may recursively create work.
func (f *runReviewFixture) complete() error {
	seen := make(map[entityID]bool)
	for _, visibility := range []string{"conversation", "personal"} {
		rows, err := f.list(f.requester, visibility)
		if err != nil {
			return err
		}
		for _, review := range rows {
			if seen[review.RunID] || f.sources[review.RunID] != visibility || !reviewTerminal(review.Status) || review.ModelCalls > 2 {
				return fmt.Errorf("run-reviews: duplicate, recursive, wrong-scope or incomplete review: %+v", review)
			}
			seen[review.RunID] = true
		}
	}
	if len(seen) != len(f.sources) {
		return fmt.Errorf("run-reviews: terminal sources=%d reviews=%d", len(f.sources), len(seen))
	}
	for _, label := range []string{"valuable", "original-failed", "script", "personal", "waiting-stopped", "modify"} {
		if err := f.calls("RUN_REVIEW_"+label+"_"+f.suffix, "assess", "extract"); err != nil {
			return err
		}
	}
	for _, label := range []string{"simple", "review-error", "review-budget", "explicit-cancel", "disable-cancel"} {
		if err := f.calls("RUN_REVIEW_"+label+"_"+f.suffix, "assess"); err != nil {
			return err
		}
	}
	return f.calls("RUN_REVIEW_disabled-new-run_" + f.suffix)
}
