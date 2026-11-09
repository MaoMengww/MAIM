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

// Controlled model responses prove the public revision contract, not whether a
// real model can distinguish useful corrections. No generated candidate publishes.
type runReviewAssessmentView struct {
	Valuable        bool   `json:"valuable"`
	Reason          string `json:"reason"`
	TargetVersionID string `json:"target_version_id"`
	TargetGoal      string `json:"target_goal"`
	Applicability   string `json:"applicability"`
}

func (f *runReviewFixture) submitRevision(caller account, run publicRun, kind, feedback, key string, parent *entityID) (runReviewView, error) {
	body := map[string]any{"trigger_kind": kind, "submission_key": key}
	if feedback != "" {
		body["feedback"] = feedback
	}
	if parent != nil {
		body["parent_review_id"] = *parent
	}
	var result runReviewView
	err := f.d.request(http.MethodPost, f.path("runs/"+run.ID.String()+"/reviews"), caller.token, body, &result)
	if err == nil {
		if f.revisions == nil {
			f.revisions = make(map[entityID]runReviewView)
		}
		f.revisions[result.ID] = result
	}
	return result, err
}

// Explicit revisions poll their own identity; waitReview intentionally keeps its
// original invariant of exactly one automatic review for an ordinary Run.
func (f *runReviewFixture) waitRevision(run publicRun, visibility string, id entityID, status string) (runReviewView, error) {
	deadline := time.Now().Add(f.d.timeout)
	var current runReviewView
	for time.Now().Before(deadline) {
		rows, err := f.list(f.requester, visibility)
		if err != nil {
			return current, err
		}
		for _, row := range rows {
			if row.ID != id {
				continue
			}
			current = row
			if row.RunID != run.ID {
				return row, errors.New("run-review revisions: review changed its source Run")
			}
			if row.Status == status {
				if status == "cancelled" {
					return row, nil
				}
				return f.detail(f.requester, id)
			}
			if reviewTerminal(row.Status) {
				return row, fmt.Errorf("run-review revisions: expected %s, got %s (%s)", status, row.Status, row.Reason)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return current, fmt.Errorf("run-review revisions: %s did not reach %s (last=%s)", id, status, current.Status)
}

func (f *runReviewFixture) revisionIdentity(review runReviewView, run publicRun, parent runReviewView, kind, feedback string) error {
	if review.ID == "" || review.ID == parent.ID || review.RunID != run.ID || review.BotID != f.bot.ID || review.ConvID != f.conv ||
		review.Revision != parent.Revision+1 || review.TriggerKind != kind || !hasEntityID(review.SubmittedBy, f.requester.id) ||
		!hasEntityID(review.ParentReviewID, parent.ID) || review.Feedback != feedback || review.ModelCalls > 2 {
		return fmt.Errorf("run-review revisions: lost source, revision, authenticated submitter or parent: %+v", review)
	}
	return nil
}

func (f *runReviewFixture) revisionRows(run publicRun, visibility string, expected ...runReviewView) error {
	rows, err := f.list(f.requester, visibility)
	if err != nil {
		return err
	}
	seen := make(map[entityID]bool)
	for _, row := range rows {
		if row.RunID != run.ID {
			continue
		}
		index := slices.IndexFunc(expected, func(value runReviewView) bool { return value.ID == row.ID })
		if index < 0 || seen[row.ID] {
			return fmt.Errorf("run-review revisions: unexpected or duplicate revision for Run %s: %+v", run.ID, row)
		}
		want := expected[index]
		if row.Revision != want.Revision || row.TriggerKind != want.TriggerKind || !reflect.DeepEqual(row.ParentReviewID, want.ParentReviewID) ||
			!reflect.DeepEqual(row.SubmittedBy, want.SubmittedBy) || !reflect.DeepEqual(row.CandidateID, want.CandidateID) ||
			row.Feedback != "" || row.Assessment != nil || row.Candidate != nil {
			return fmt.Errorf("run-review revisions: list lost metadata or exposed detail material: %+v", row)
		}
		seen[row.ID] = true
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("run-review revisions: expected %d independent revisions, got %d", len(expected), len(seen))
	}
	return nil
}

func (f *runReviewFixture) revisionCalls(token string, phases ...string) error {
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
			return errors.New("run-review revisions: revision used a different model")
		}
		if tools, ok := request.Body["tools"].([]any); ok && len(tools) != 0 {
			return errors.New("run-review revisions: revision advertised executable tools")
		}
	}
	if !slices.Equal(actual, phases) || observed.Executions != 0 {
		return fmt.Errorf("run-review revisions: phases=%v expected=%v executions=%d", actual, phases, observed.Executions)
	}
	return nil
}

func (f *runReviewFixture) feedbackEvidence(token string, review runReviewView) error {
	observed, err := f.observe(token, "observe")
	if err != nil {
		return err
	}
	var phases []string
	for _, request := range observed.Requests {
		messages, _ := request.Body["messages"].([]any)
		for _, value := range messages {
			message, ok := value.(map[string]any)
			if !ok || message["role"] != "user" {
				continue
			}
			text, _ := message["content"].(string)
			var envelope map[string]json.RawMessage
			if json.Unmarshal([]byte(text), &envelope) != nil {
				continue
			}
			if evidence, ok := envelope["evidence"]; ok {
				text = string(evidence)
			}
			var material struct {
				Feedback *struct {
					Text        string   `json:"text"`
					SubmittedBy entityID `json:"submitted_by"`
					ReviewID    entityID `json:"review_id"`
				} `json:"feedback"`
			}
			if json.Unmarshal([]byte(text), &material) != nil || material.Feedback == nil || material.Feedback.ReviewID != review.ID {
				continue
			}
			if material.Feedback.Text != review.Feedback || material.Feedback.SubmittedBy != f.requester.id {
				return errors.New("run-review revisions: model feedback lost exact text or authenticated provenance")
			}
			phases = append(phases, request.Phase)
		}
	}
	want := []string{"assess", "extract"}
	if review.Status == "no_candidate" {
		want = []string{"assess"}
	}
	if !slices.Equal(phases, want) {
		return fmt.Errorf("run-review revisions: structured feedback phases=%v expected=%v", phases, want)
	}
	return nil
}

func (f *runReviewFixture) revisionSource(label, visibility string) (publicRun, runReviewView, string, error) {
	token, err := f.arm(label, "valuable", "")
	if err != nil {
		return publicRun{}, runReviewView{}, token, err
	}
	run, err := f.start(token, visibility, uuid.NewString(), nil)
	if err != nil {
		return run, runReviewView{}, token, err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return run, runReviewView{}, token, err
	}
	review, err := f.waitReview(run, visibility, "candidate_generated")
	if err == nil && (review.Revision != 1 || review.TriggerKind != "automatic" || review.ParentReviewID != nil || review.SubmittedBy != nil) {
		err = fmt.Errorf("run-review revisions: invalid automatic revision metadata: %+v", review)
	}
	return run, review, token, err
}

func (f *runReviewFixture) feedbackAndHistorical() error {
	if err := f.historicalRevisions(); err != nil {
		return err
	}
	if err := f.privateRevisions(); err != nil {
		return err
	}
	for _, revoke := range []bool{false, true} {
		if err := f.revisionAuthorityChange(revoke); err != nil {
			return err
		}
	}
	fmt.Println("E2E PASS: feedback/historical public revisions, retry/conflict, independent candidates, private denial, revocation/disable fencing (controlled provider; not real-model evidence)")
	return nil
}

func (f *runReviewFixture) historicalRevisions() error {
	if _, err := f.setPolicy(f.admin, "conversation", false, uuid.NewString()); err != nil {
		return err
	}
	token, err := f.arm("historical-disabled", "valuable", "")
	if err != nil {
		return err
	}
	run, err := f.start(token, "conversation", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	original, err := f.waitReview(run, "conversation", "cancelled")
	if err != nil {
		return err
	}
	if original.Revision != 1 || original.TriggerKind != "automatic" || original.ModelCalls != 0 || original.CandidateID != nil {
		return errors.New("run-review revisions: disabled Run unexpectedly assessed or lost its automatic record")
	}
	for _, kind := range []string{"historical", "feedback"} {
		feedback := ""
		if kind == "feedback" {
			feedback = "Count only data rows, not the header."
		}
		_, err = f.submitRevision(f.requester, run, kind, feedback, uuid.NewString(), nil)
		if err = f.reject(err, "new revision while policy disabled"); err != nil {
			return err
		}
	}
	if _, err = f.setPolicy(f.admin, "conversation", true, uuid.NewString()); err != nil {
		return err
	}
	original, err = f.detail(f.requester, original.ID)
	if err != nil {
		return err
	}
	if err = f.noReview(run, "conversation", true); err != nil {
		return err
	}
	if err = f.revisionCalls(token); err != nil {
		return err
	}
	key := uuid.NewString()
	historical, err := f.submitRevision(f.requester, run, "historical", "", key, nil)
	if err != nil {
		return err
	}
	historical, err = f.waitRevision(run, "conversation", historical.ID, "candidate_generated")
	if err != nil {
		return err
	}
	if err = f.revisionIdentity(historical, run, original, "historical", ""); err != nil {
		return err
	}
	if historical.Assessment == nil || !historical.Assessment.Valuable || historical.Assessment.Reason == "" || historical.Assessment.TargetGoal != "Summarize fixed rows" || historical.Assessment.Applicability != "Fixed CSV input only" {
		return errors.New("run-review revisions: historical assessment detail missing")
	}
	if err = f.candidate(historical, run, "create", token); err != nil {
		return err
	}
	for range 2 {
		replay, replayErr := f.submitRevision(f.requester, run, "historical", "", key, nil)
		if replayErr != nil || replay.ID != historical.ID || replay.Revision != historical.Revision {
			return fmt.Errorf("run-review revisions: historical retry changed revision: %v", replayErr)
		}
	}
	_, err = f.submitRevision(f.requester, run, "feedback", "Different payload under the historical key", key, nil)
	if err = f.d.expectStatus(err, http.StatusConflict, "review submission payload conflict"); err != nil {
		return err
	}
	all := []runReviewView{original, historical}
	parent := historical
	feedback := "Count only data rows; exclude the header and report the literal total."
	for index := range 2 {
		parentID := &historical.ID
		if index == 1 {
			parentID = nil // Omission links the latest revision, never overwrites it.
		}
		feedbackKey := uuid.NewString()
		review, err := f.submitRevision(f.requester, run, "feedback", feedback, feedbackKey, parentID)
		if err != nil {
			return err
		}
		review, err = f.waitRevision(run, "conversation", review.ID, "candidate_generated")
		if err != nil {
			return err
		}
		if err = f.revisionIdentity(review, run, parent, "feedback", feedback); err != nil {
			return err
		}
		if err = f.candidate(review, run, "create", token); err != nil {
			return err
		}
		if review.Candidate.ID == parent.Candidate.ID || !strings.Contains(review.Candidate.Instructions, feedback) {
			return errors.New("run-review revisions: distinct submission keys merged candidates or lost correction")
		}
		if err = f.pendingOnly(review); err != nil {
			return err
		}
		replay, replayErr := f.submitRevision(f.requester, run, "feedback", feedback, feedbackKey, parentID)
		if replayErr != nil || replay.ID != review.ID {
			return fmt.Errorf("run-review revisions: feedback retry changed identity: %v", replayErr)
		}
		_, err = f.submitRevision(f.requester, run, "feedback", feedback+" Changed.", feedbackKey, parentID)
		if err = f.d.expectStatus(err, http.StatusConflict, "feedback submission payload conflict"); err != nil {
			return err
		}
		if err = f.feedbackEvidence(token, review); err != nil {
			return err
		}
		all = append(all, review)
		parent = review
	}
	invalid := "RUN_REVIEW_FEEDBACK_NO_VALUE: Unrelated weather preference, with no evidence about counting rows."
	noValue, err := f.submitRevision(f.requester, run, "feedback", invalid, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	noValue, err = f.waitRevision(run, "conversation", noValue.ID, "no_candidate")
	if err != nil {
		return err
	}
	if err = f.revisionIdentity(noValue, run, parent, "feedback", invalid); err != nil {
		return err
	}
	if noValue.CandidateID != nil || noValue.Candidate != nil || noValue.ModelCalls != 1 || noValue.Assessment == nil || noValue.Assessment.Valuable || noValue.Assessment.Reason == "" {
		return errors.New("run-review revisions: unrelated feedback became a candidate or lost assessment reason")
	}
	if err = f.feedbackEvidence(token, noValue); err != nil {
		return err
	}
	all = append(all, noValue)
	if err = f.revisionRows(run, "conversation", all...); err != nil {
		return err
	}
	otherRun, otherReview, _, err := f.revisionSource("feedback-parent-other", "conversation")
	if err != nil {
		return err
	}
	_, err = f.submitRevision(f.requester, run, "feedback", feedback, uuid.NewString(), &otherReview.ID)
	if err = f.reject(err, "parent from another Run"); err != nil {
		return err
	}
	for _, body := range []map[string]any{
		{"trigger_kind": "feedback", "feedback": "   ", "submission_key": uuid.NewString()},
		{"trigger_kind": "feedback", "feedback": strings.Repeat("界", 2731), "submission_key": uuid.NewString()},
		{"trigger_kind": "historical", "feedback": "Not allowed on a historical request", "submission_key": uuid.NewString()},
		{"trigger_kind": "automatic", "submission_key": uuid.NewString()},
	} {
		err = f.d.request(http.MethodPost, f.path("runs/"+run.ID.String()+"/reviews"), f.requester.token, body, nil)
		if err = f.d.expectStatus(err, http.StatusBadRequest, "invalid explicit review payload"); err != nil {
			return err
		}
	}
	var pathBound runReviewView
	err = f.d.request(http.MethodPost, f.path("runs/"+run.ID.String()+"/reviews"), f.requester.token,
		map[string]any{"trigger_kind": "historical", "submission_key": key, "run_id": otherRun.ID, "submitted_by": f.owner.id}, &pathBound)
	if err != nil || pathBound.ID != historical.ID || pathBound.RunID != run.ID || !hasEntityID(pathBound.SubmittedBy, f.requester.id) {
		return fmt.Errorf("run-review revisions: payload redirected path Run or forged authenticated submitter: %v", err)
	}
	for _, previous := range []runReviewView{original, historical, all[2], all[3]} {
		current, err := f.detail(f.requester, previous.ID)
		if err != nil || !reflect.DeepEqual(current, previous) {
			return fmt.Errorf("run-review revisions: later feedback rewrote prior review %s: %v", previous.ID, err)
		}
	}
	if err = f.revisionCalls(token, "assess", "extract", "assess", "extract", "assess", "extract", "assess"); err != nil {
		return err
	}
	if err = f.revisionRows(run, "conversation", all...); err != nil {
		return err
	}
	f.reviewBrowser = map[string]any{"run_id": run.ID, "historical_review_id": historical.ID, "feedback_review_id": all[2].ID, "no_candidate_review_id": noValue.ID, "disabled_run_id": run.ID}
	if err = f.unchanged(otherRun); err != nil {
		return err
	}
	return f.unchanged(run)
}

func (f *runReviewFixture) privateRevisions() error {
	run, original, token, err := f.revisionSource("feedback-private", "personal")
	if err != nil {
		return err
	}
	feedback := "Private correction: exclude the header when counting data rows."
	review, err := f.submitRevision(f.requester, run, "feedback", feedback, uuid.NewString(), &original.ID)
	if err != nil {
		return err
	}
	review, err = f.waitRevision(run, "personal", review.ID, "candidate_generated")
	if err != nil {
		return err
	}
	if err = f.revisionIdentity(review, run, original, "feedback", feedback); err != nil {
		return err
	}
	for _, caller := range []account{f.owner, f.admin} {
		for _, kind := range []string{"historical", "feedback"} {
			text := ""
			if kind == "feedback" {
				text = feedback
			}
			_, err = f.submitRevision(caller, run, kind, text, uuid.NewString(), &review.ID)
			if err = f.d.expectStatus(err, http.StatusForbidden, "non-owner private review submission"); err != nil {
				return err
			}
		}
		// Caller identity always comes from authentication, not JSON metadata.
		err = f.d.request(http.MethodPost, f.path("runs/"+run.ID.String()+"/reviews"), caller.token, map[string]any{
			"trigger_kind": "feedback", "feedback": feedback, "submission_key": uuid.NewString(),
			"submitted_by": f.requester.id, "requester_id": f.requester.id, "user_id": f.requester.id,
		}, nil)
		if err = f.d.expectStatus(err, http.StatusForbidden, "forged authenticated private submitter"); err != nil {
			return err
		}
		_, err = f.detail(caller, review.ID)
		if err = f.d.expectStatus(err, http.StatusForbidden, "private feedback readback by Bot owner or admin"); err != nil {
			return err
		}
		rows, err := f.list(caller, "personal")
		if err != nil {
			return err
		}
		if slices.ContainsFunc(rows, func(value runReviewView) bool { return value.RunID == run.ID }) {
			return errors.New("run-review revisions: private revision leaked through another user's personal list")
		}
	}
	if err = f.revisionRows(run, "personal", original, review); err != nil {
		return err
	}
	if err = f.feedbackEvidence(token, review); err != nil {
		return err
	}
	if err = f.revisionCalls(token, "assess", "extract", "assess", "extract"); err != nil {
		return err
	}
	f.reviewBrowser["private_run_id"], f.reviewBrowser["private_review_id"] = run.ID, review.ID
	return f.unchanged(run)
}

func (f *runReviewFixture) revisionAuthorityChange(revoke bool) error {
	label := "feedback-disable"
	if revoke {
		label = "feedback-revoke"
	}
	token, err := f.arm(label, "valuable", "")
	if err != nil {
		return err
	}
	defer f.observe(token, "release")
	var run publicRun
	var grant resourceAuthorization
	var sourceFile entityID
	if revoke {
		file, err := f.d.attachmentUpload(f.requester, label+"-"+f.suffix+".txt", "text/plain", []byte("Synthetic authorized row-count source"), 1)
		if err != nil {
			return err
		}
		grant, err = f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", file.info.FileID, "conversation", true)
		if err != nil {
			return err
		}
		sourceFile = file.info.FileID
		err = f.d.request(http.MethodPost, f.path("runs"), f.requester.token, map[string]any{
			"conv_id": f.conv, "visibility": "conversation", "submission_key": uuid.NewString(), "message": token,
			"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{grant.ID},
		}, &run)
		if err == nil {
			f.sources[run.ID] = "conversation"
		}
	} else {
		run, err = f.start(token, "conversation", uuid.NewString(), nil)
	}
	if err != nil {
		return err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	original, err := f.waitReview(run, "conversation", "candidate_generated")
	if err != nil {
		return err
	}
	feedback := "RUN_REVIEW_FEEDBACK_HOLD: Exclude the header, retain the original result until this correction is reviewed."
	review, err := f.submitRevision(f.requester, run, "feedback", feedback, uuid.NewString(), &original.ID)
	if err != nil {
		return err
	}
	if err = f.held(token); err != nil {
		return err
	}
	if revoke {
		err = f.d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil)
	} else {
		_, err = f.setPolicy(f.admin, "conversation", false, uuid.NewString())
	}
	if err != nil {
		return err
	}
	if _, err = f.observe(token, "release"); err != nil {
		return err
	}
	if revoke {
		// held("assess") proves the provider already received this revision's
		// evidence before revocation; release must allow extraction to finish.
		completed, err := f.waitRevision(run, "conversation", review.ID, "candidate_generated")
		if err != nil {
			return err
		}
		if err = f.revisionIdentity(completed, run, original, "feedback", feedback); err != nil {
			return err
		}
		if completed.Assessment == nil || completed.Candidate == nil || completed.ModelCalls != 2 || !strings.Contains(completed.Candidate.Instructions, feedback) {
			return errors.New("run-review revisions: source revocation blocked or stripped the already-read feedback revision")
		}
		if err = f.feedbackEvidence(token, completed); err != nil {
			return err
		}
		for _, caller := range []account{f.requester, f.owner} {
			archived, err := f.detail(caller, original.ID)
			if err != nil || !reflect.DeepEqual(archived, original) {
				return fmt.Errorf("run-review revisions: source revocation changed original review history: %v", err)
			}
			current, err := f.detail(caller, completed.ID)
			if err != nil || !reflect.DeepEqual(current, completed) {
				return fmt.Errorf("run-review revisions: source revocation hid completed feedback history: %v", err)
			}
		}
		historical, err := f.submitRevision(f.requester, run, "historical", "", uuid.NewString(), &completed.ID)
		if err != nil {
			return fmt.Errorf("run-review revisions: source revocation blocked a fresh authorized history review: %w", err)
		}
		historical, err = f.waitRevision(run, "conversation", historical.ID, "candidate_generated")
		if err != nil {
			return err
		}
		if err = f.revisionIdentity(historical, run, completed, "historical", ""); err != nil {
			return err
		}
		if err = f.candidate(historical, run, "create", token); err != nil {
			return err
		}
		if err = f.revisionRows(run, "conversation", original, completed, historical); err != nil {
			return err
		}
		var fresh publicRun
		err = f.d.request(http.MethodPost, f.path("runs"), f.requester.token, map[string]any{
			"conv_id": f.conv, "visibility": "conversation", "submission_key": uuid.NewString(), "message": token,
			"file_ids": []entityID{sourceFile}, "resource_grant_ids": []entityID{grant.ID},
		}, &fresh)
		if err = f.d.expectStatus(err, http.StatusForbidden, "new source acquisition after grant revocation"); err != nil {
			return err
		}
		if err = f.revisionCalls(token, "assess", "extract", "assess", "extract", "assess", "extract"); err != nil {
			return err
		}
		return f.unchanged(run)
	}
	cancelled, err := f.waitRevision(run, "conversation", review.ID, "cancelled")
	if err != nil {
		return err
	}
	if cancelled.CandidateID != nil || cancelled.Candidate != nil || cancelled.ModelCalls > 1 {
		return errors.New("run-review revisions: disabled revision extracted or committed a candidate")
	}
	settleUntil := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(settleUntil) {
		if _, err = f.waitRevision(run, "conversation", review.ID, "cancelled"); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, id := range []entityID{original.ID, review.ID} {
		hidden, err := f.detail(f.requester, id)
		if err != nil || hidden.Feedback != "" || hidden.Assessment != nil || hidden.Candidate != nil {
			return fmt.Errorf("run-review revisions: disabled scope exposed feedback, assessment or candidate: %v", err)
		}
	}
	_, err = f.submitRevision(f.requester, run, "historical", "", uuid.NewString(), nil)
	if err = f.reject(err, "new historical revision while scope disabled"); err != nil {
		return err
	}
	if _, err = f.setPolicy(f.admin, "conversation", true, uuid.NewString()); err != nil {
		return err
	}
	f.reviewBrowser["cancelled_run_id"], f.reviewBrowser["cancelled_review_id"] = run.ID, review.ID
	if err = f.revisionCalls(token, "assess", "extract", "assess"); err != nil {
		return err
	}
	return f.unchanged(run)
}
