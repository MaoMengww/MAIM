package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Governance is exercised through authenticated gateway requests, never private
// tables. Real Docker validation is a separate smoke with the human-frozen worker.
type governedCandidate struct {
	runReviewCandidateView
	Name               string                `json:"name"`
	Description        string                `json:"description"`
	ParentCandidateID  *entityID             `json:"parent_candidate_id"`
	SourceReviewID     entityID              `json:"source_review_id"`
	SourceVersionIDs   []entityID            `json:"source_version_ids"`
	CurrentVersionID   *entityID             `json:"current_version_id"`
	BaselineStale      *bool                 `json:"baseline_stale"`
	BaselineContent    *candidatePackageView `json:"baseline_content"`
	CurrentContent     *candidatePackageView `json:"current_content"`
	CanMaintain        bool                  `json:"can_maintain"`
	CanPublish         bool                  `json:"can_publish"`
	CanViewMaterials   bool                  `json:"can_view_materials"`
	EvidenceID         string                `json:"evidence_id"`
	EvidenceRevision   int64                 `json:"evidence_revision"`
	Evidence           string                `json:"evidence"`
	RepresentativeTask string                `json:"representative_task"`
	ExpectedOutput     string                `json:"expected_output"`
	Reason             string                `json:"reason"`
	Evaluation         *struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
		Digest string `json:"digest"`
	} `json:"evaluation"`
}

type candidatePackageView struct {
	Instructions string            `json:"instructions"`
	Resources    map[string]string `json:"resources"`
	Scripts      map[string]string `json:"scripts"`
	ImageDigest  string            `json:"image_digest"`
	EntryPoint   string            `json:"entry_point"`
}

func (f *runReviewFixture) governed(caller account, id entityID) (governedCandidate, error) {
	var result governedCandidate
	err := f.d.request(http.MethodGet, f.path("skill-candidates/"+id.String()), caller.token, nil, &result)
	return result, err
}

func (f *runReviewFixture) govern(caller account, candidate governedCandidate, operation, key string, fields map[string]any, target any) error {
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["submission_key"], fields["digest"] = key, candidate.Digest
	return f.d.request(http.MethodPost, f.path("skill-candidates/"+candidate.ID.String()+"/"+operation), caller.token, fields, target)
}

func (f *runReviewFixture) newGoverned(label, mode string) (governedCandidate, publicRun, string, error) {
	token, err := f.arm("governance-"+label, mode, "")
	if err != nil {
		return governedCandidate{}, publicRun{}, token, err
	}
	run, err := f.start(token, "conversation", uuid.NewString(), nil)
	if err != nil {
		return governedCandidate{}, run, token, err
	}
	run, err = f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return governedCandidate{}, run, token, err
	}
	review, err := f.waitReview(run, "conversation", "candidate_generated")
	if err != nil {
		return governedCandidate{}, run, token, err
	}
	if label == "expiry" && review.ExpiresAt-review.CreatedAt != int64(30*24*time.Hour/time.Second) {
		return governedCandidate{}, run, token, errors.New("candidate retention: default evidence retention is not 30 days from collection")
	}
	candidate, err := f.governed(f.owner, review.Candidate.ID)
	return candidate, run, token, err
}

func (f *runReviewFixture) readyGoverned(candidate governedCandidate, token string) (governedCandidate, error) {
	var revised governedCandidate
	err := f.govern(f.owner, candidate, "revise", uuid.NewString(), map[string]any{
		"representative_task": "Return the accepted fixture result. " + token,
		"expected_output":     "RUN_REVIEW_RESULT:" + token,
	}, &revised)
	if err != nil {
		return revised, err
	}
	var evaluation struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
	}
	key := uuid.NewString()
	if err = f.govern(f.owner, revised, "evaluate", key, nil, &evaluation); err != nil {
		return revised, err
	}
	if evaluation.Status != "passed" || !evaluation.Passed {
		return revised, fmt.Errorf("candidate governance: representative task verdict %+v", evaluation)
	}
	if err = f.govern(f.owner, revised, "evaluate", key, nil, &evaluation); err != nil {
		return revised, err
	}
	if err = f.govern(f.owner, revised, "approve", uuid.NewString(), nil, &revised); err != nil {
		return revised, err
	}
	if revised.Status != "approved" {
		return revised, fmt.Errorf("candidate governance: not approved: %s", revised.Status)
	}
	return revised, nil
}

func (f *runReviewFixture) candidateGovernance() error {
	f.browser = map[string]any{
		"accounts": map[string]any{
			"owner":     map[string]any{"id": f.owner.id, "username": f.owner.username, "password": f.owner.password, "token": f.owner.token},
			"requester": map[string]any{"id": f.requester.id, "username": f.requester.username, "password": f.requester.password, "token": f.requester.token},
			"admin":     map[string]any{"id": f.admin.id, "username": f.admin.username, "password": f.admin.password, "token": f.admin.token},
		},
		"bot_id": f.bot.ID, "conversation_id": f.conv, "model_id": f.model,
	}
	if err := f.save(); err != nil {
		return err
	}
	first, source, firstToken, err := f.newGoverned("first", "valuable")
	if err != nil {
		return err
	}
	competitor, _, competitorToken, err := f.newGoverned("competitor", "valuable")
	if err != nil {
		return err
	}
	denied, _, _, err := f.newGoverned("reject", "valuable")
	if err != nil {
		return err
	}
	script, _, _, err := f.newGoverned("script", "script")
	if err != nil {
		return err
	}

	// Bot ownership does not grant source-scope maintenance authority.
	if err = f.d.expectStatus(f.govern(f.requester, first, "approve", uuid.NewString(), nil, nil), http.StatusForbidden, "candidate maintenance authority"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, first, "approve", uuid.NewString(), nil, nil), http.StatusForbidden, "Bot owner without scope maintenance authority"); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPut, "/convs/"+f.conv.String()+"/members/"+f.owner.id.String()+"/role", f.admin.token, map[string]any{"role": 2}, nil); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, first, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "unverified candidate approval"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, first, "publish", uuid.NewString(), nil, nil), http.StatusConflict, "unapproved candidate publication"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, script, "evaluate", uuid.NewString(), nil, nil), http.StatusConflict, "unreviewed generated script execution"); err != nil {
		return err
	}
	if err = f.govern(f.owner, script, "code-review", uuid.NewString(), map[string]any{"confirmed": true}, nil); err == nil {
		return errors.New("candidate governance: untrusted generated code obtained execution admission")
	}
	if err = f.candidateModelRecovery(); err != nil {
		return err
	}

	var rejected governedCandidate
	key := uuid.NewString()
	if err = f.govern(f.owner, denied, "reject", key, map[string]any{"reason": "Incorrect evidence attribution"}, &rejected); err != nil {
		return err
	}
	if rejected.Status != "rejected" {
		return fmt.Errorf("candidate governance: rejection state %s", rejected.Status)
	}
	if err = f.govern(f.owner, denied, "reject", key, map[string]any{"reason": "Incorrect evidence attribution"}, &rejected); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, denied, "reject", key, map[string]any{"reason": "Different decision under the same key"}, nil), http.StatusConflict, "candidate submission payload conflict"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, rejected, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "rejected candidate approval"); err != nil {
		return err
	}

	var requested governedCandidate
	if err = f.govern(f.owner, first, "request-evidence", uuid.NewString(), map[string]any{"reason": "Supply an independently specified expected result"}, &requested); err != nil {
		return err
	}
	if requested.Status != "awaiting_evidence" {
		return fmt.Errorf("candidate governance: missing evidence status %s", requested.Status)
	}
	if err = f.d.expectStatus(f.govern(f.owner, requested, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "insufficient evidence approval"); err != nil {
		return err
	}
	if err = f.govern(f.owner, requested, "revise", uuid.NewString(), map[string]any{
		"instructions": requested.Instructions + "\nCorrection A: preserve rows whose total is zero.",
		"resources":    map[string]string{"references/format.md": "Report a literal total.", "references/correction-a.md": "Preserve zero totals."},
	}, &requested); err != nil {
		return err
	}
	first, err = f.readyGoverned(requested, firstToken)
	if err != nil {
		return err
	}
	if err = f.govern(f.owner, competitor, "revise", uuid.NewString(), map[string]any{
		"instructions": competitor.Instructions + "\nCorrection B: trim surrounding whitespace.",
		"resources":    map[string]string{"references/format.md": "Report a literal total.", "references/correction-b.md": "Trim surrounding whitespace."},
	}, &competitor); err != nil {
		return err
	}
	competitor, err = f.readyGoverned(competitor, competitorToken)
	if err != nil {
		return err
	}

	// Changing evidence after approval fences both stale forms and publication.
	old := first
	if err = f.govern(f.owner, first, "revise", uuid.NewString(), map[string]any{"expected_output": "RUN_REVIEW_RESULT:"}, &first); err != nil {
		return err
	}
	if first.Status == "approved" || first.Digest == old.Digest {
		return errors.New("candidate governance: revised evidence retained approval or content identity")
	}
	if err = f.d.expectStatus(f.govern(f.owner, old, "publish", uuid.NewString(), nil, nil), http.StatusConflict, "stale approved package"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, first, "publish", uuid.NewString(), nil, nil), http.StatusConflict, "changed evidence publication"); err != nil {
		return err
	}
	var bad struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
	}
	if err = f.govern(f.owner, first, "evaluate", uuid.NewString(), nil, &bad); err != nil {
		return err
	}
	if bad.Status != "failed" || bad.Passed {
		return fmt.Errorf("candidate governance: wrong expected output was not failed: %+v", bad)
	}
	first, err = f.readyGoverned(first, firstToken)
	if err != nil {
		return err
	}

	if err = f.retentionGovernance(); err != nil {
		return err
	}

	// Actual membership revocation after approval blocks material and publish.
	if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/kick", f.admin.token, map[string]any{"user_ids": []entityID{f.owner.id}}, nil); err != nil {
		return err
	}
	if _, err = f.governed(f.owner, first.ID); err == nil {
		return errors.New("candidate governance: Bot ownership bypassed revoked material access")
	}
	if err = f.d.expectStatus(f.govern(f.owner, first, "publish", uuid.NewString(), nil, nil), http.StatusForbidden, "revoked approver publication"); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/invite", f.admin.token, map[string]any{"user_ids": []entityID{f.owner.id}}, nil); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPut, "/convs/"+f.conv.String()+"/members/"+f.owner.id.String()+"/role", f.admin.token, map[string]any{"role": 2}, nil); err != nil {
		return err
	}
	first, err = f.readyGoverned(first, firstToken)
	if err != nil {
		return err
	}
	var published, retry skillVersionView
	publishKey := uuid.NewString()
	if err = f.govern(f.owner, first, "publish", publishKey, nil, &published); err != nil {
		return err
	}
	if published.VersionID == "" || published.Status != "published" {
		return fmt.Errorf("candidate governance: publication %+v", published)
	}
	if err = f.govern(f.owner, first, "publish", publishKey, nil, &retry); err != nil {
		return err
	}
	if retry.VersionID != published.VersionID {
		return errors.New("candidate governance: retry minted duplicate immutable version")
	}
	if err = f.govern(f.owner, first, "publish", uuid.NewString(), nil, &retry); err != nil {
		return err
	}
	if retry.VersionID != published.VersionID {
		return errors.New("candidate governance: new submission minted duplicate immutable version")
	}
	if err = f.d.expectStatus(f.govern(f.owner, competitor, "publish", uuid.NewString(), nil, nil), http.StatusConflict, "stale candidate baseline"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, first, "revise", uuid.NewString(), map[string]any{"instructions": "replace published content"}, nil), http.StatusConflict, "published package mutation"); err != nil {
		return err
	}
	var catalog []availableSkillView
	if err = f.d.request(http.MethodGet, f.path("skill-catalog"), f.requester.token, nil, &catalog); err != nil {
		return err
	}
	for _, item := range catalog {
		if item.VersionID == published.VersionID {
			return errors.New("candidate governance: source-scoped version leaked into global catalog")
		}
	}
	var other struct {
		ID entityID `json:"conversation_id"`
	}
	if err = f.d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{"type": "group", "group_name": "candidate-other-scope", "member_ids": []entityID{f.owner.id, f.requester.id}}, &other); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/convs/"+other.ID.String()+"/bots", f.admin.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
		return err
	}
	if err = f.d.authorizationReject(f.path("runs"), f.requester.token, map[string]any{"conv_id": other.ID, "visibility": "conversation", "submission_key": uuid.NewString(), "message": "Do not reuse another scope's material", "skill_version_ids": []entityID{published.VersionID}}); err != nil {
		return err
	}
	if err = f.candidateBaselineRegression(published); err != nil {
		return err
	}
	if err = f.candidateReintegrationRegression(first, competitor, published, firstToken); err != nil {
		return err
	}
	if err = f.candidateFileViewer(); err != nil {
		return err
	}
	if err = f.unchanged(source); err != nil {
		return err
	}
	f.browser["published"] = map[string]any{"candidate_id": first.ID, "digest": first.Digest, "submission_key": publishKey, "version_id": published.VersionID}
	if err = f.save(); err != nil {
		return err
	}

	var rows []json.RawMessage
	if err = f.d.request(http.MethodGet, f.path("skill-candidates"), f.owner.token, nil, &rows); err != nil {
		return err
	}
	fmt.Println("E2E PASS: candidate governance rejection/evidence/revision/verification/authority/revocation/expiry/baseline/idempotent publication")
	return f.complete()
}

func (f *runReviewFixture) candidateBaselineRegression(baseline skillVersionView) error {
	token, err := f.arm("governance-baseline-regression", "catalog-only", baseline.VersionID)
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
	frozen := false
	for _, entry := range run.FrozenSkills {
		if entry.VersionID == baseline.VersionID {
			frozen = true
		}
	}
	if !frozen {
		return errors.New("candidate governance: source-scoped publication was not available to a subsequent same-scope Run")
	}
	review, err := f.waitReview(run, "conversation", "candidate_generated")
	if err != nil {
		return err
	}
	if review.Candidate == nil || review.Candidate.BaselineVersionID == nil || *review.Candidate.BaselineVersionID != baseline.VersionID {
		return errors.New("candidate governance: modification lost its exact published baseline")
	}
	candidate, err := f.governed(f.owner, review.Candidate.ID)
	if err != nil {
		return err
	}
	var displayed struct {
		BaselineContent *struct {
			Instructions string            `json:"instructions"`
			Resources    json.RawMessage   `json:"resources"`
			Scripts      map[string]string `json:"scripts"`
			ImageDigest  string            `json:"image_digest"`
			EntryPoint   string            `json:"entry_point"`
		} `json:"baseline_content"`
		Digest string `json:"digest"`
	}
	if err = f.d.request(http.MethodGet, f.path("skill-candidates/"+candidate.ID.String()), f.owner.token, nil, &displayed); err != nil {
		return err
	}
	if displayed.BaselineContent == nil || displayed.BaselineContent.Instructions != candidate.BaselineInstructions || len(displayed.BaselineContent.Resources) == 0 || displayed.Digest != candidate.Digest {
		return errors.New("candidate governance: immutable baseline display content missing or changed candidate identity")
	}
	baselineDisplay, err := json.Marshal(displayed.BaselineContent)
	if err != nil {
		return err
	}
	if err = f.govern(f.owner, candidate, "revise", uuid.NewString(), map[string]any{"representative_task": "Return the new scenario result. " + token, "expected_output": "RUN_REVIEW_RESULT:" + token}, &candidate); err != nil {
		return err
	}
	if err = f.d.request(http.MethodGet, f.path("skill-candidates/"+candidate.ID.String()), f.owner.token, nil, &displayed); err != nil {
		return err
	}
	afterDisplay, err := json.Marshal(displayed.BaselineContent)
	if err != nil || string(afterDisplay) != string(baselineDisplay) {
		return errors.New("candidate governance: candidate revision changed immutable baseline display")
	}
	var evaluation struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
	}
	if err = f.govern(f.owner, candidate, "evaluate", uuid.NewString(), nil, &evaluation); err != nil {
		return err
	}
	// The provider satisfies the new expected answer but not the original
	// version's representative task. New-sample success must not hide regression.
	if evaluation.Status != "failed" || evaluation.Passed {
		return fmt.Errorf("candidate governance: baseline regression accepted: %+v", evaluation)
	}
	if err = f.d.expectStatus(f.govern(f.owner, candidate, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "baseline regression approval"); err != nil {
		return err
	}
	return f.unchanged(run)
}

func (f *runReviewFixture) candidateFileViewer() error {
	for _, visibility := range []string{"conversation", "personal"} {
		file, err := f.d.attachmentUpload(f.requester, "candidate-source-"+visibility+"-"+f.suffix+".txt", "text/plain", []byte("authorized candidate source"), 1)
		if err != nil {
			return err
		}
		grant, err := f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", file.info.FileID, visibility, visibility == "conversation")
		if err != nil {
			return err
		}
		token, err := f.arm("governance-file-viewer-"+visibility, "valuable", "")
		if err != nil {
			return err
		}
		var run publicRun
		if err = f.d.request(http.MethodPost, f.path("runs"), f.requester.token, map[string]any{
			"conv_id": f.conv, "visibility": visibility, "submission_key": uuid.NewString(), "message": token,
			"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{grant.ID},
		}, &run); err != nil {
			return err
		}
		f.sources[run.ID] = visibility
		run, err = f.d.waitRun(f.requester, run.ID)
		if err != nil {
			return err
		}
		review, err := f.waitReview(run, visibility, "candidate_generated")
		if err != nil {
			return err
		}
		if review.Candidate == nil {
			return errors.New("candidate governance: authorized file source produced no candidate")
		}
		view, err := f.governed(f.requester, review.Candidate.ID)
		if err != nil {
			return fmt.Errorf("candidate governance: authorized file owner could not view source candidate: %w", err)
		}
		if view.Instructions == "" || view.SourceRunID != run.ID {
			return errors.New("candidate governance: authorized file viewer lost candidate material")
		}
		if review.ExpiresAt != grant.ExpiresAt.Unix() || view.ExpiresAt != grant.ExpiresAt.Unix() {
			return errors.New("candidate retention: shorter source license did not cap archived review and candidate")
		}
		_, ownerErr := f.governed(f.owner, review.Candidate.ID)
		if visibility == "personal" {
			if err = f.d.expectStatus(ownerErr, http.StatusForbidden, "Bot owner reading private source candidate"); err != nil {
				return err
			}
		} else if ownerErr != nil {
			return fmt.Errorf("candidate governance: conversation share did not authorize current member: %w", ownerErr)
		}
		if err = f.unchanged(run); err != nil {
			return err
		}
		if visibility == "conversation" {
			if err = f.d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
				return err
			}
			f.hiddenSources[run.ID] = true
			for _, caller := range []account{f.owner, f.requester} {
				_, denied := f.governed(caller, view.ID)
				if err = f.d.expectStatus(denied, http.StatusForbidden, "revoked source candidate read"); err != nil {
					return err
				}
				for _, operation := range []string{"evaluate", "approve", "publish", "revise"} {
					if err = f.d.expectStatus(f.govern(caller, view, operation, uuid.NewString(), map[string]any{"evidence_authorized": true, "source_license_expires_at": time.Now().Add(time.Hour).Unix()}, nil), http.StatusForbidden, "revoked source "+operation); err != nil {
						return err
					}
				}
			}
			f.browser["retention"].(map[string]any)["revoked_candidate_id"] = view.ID
		}
	}
	return nil
}

func (f *runReviewFixture) candidateModelRecovery() (result error) {
	candidate, _, token, err := f.newGoverned("model-recovery", "valuable")
	if err != nil {
		return err
	}
	if err = f.govern(f.owner, candidate, "revise", uuid.NewString(), map[string]any{
		"representative_task": "Return the accepted fixture result. " + token,
		"expected_output":     "RUN_REVIEW_RESULT:" + token,
	}, &candidate); err != nil {
		return err
	}
	before, err := f.observe(token, "observe")
	if err != nil {
		return err
	}
	botPath := "/bots/" + f.bot.ID.String()
	if err = f.d.request(http.MethodPut, botPath, f.owner.token, map[string]bool{"clear_model_id": true}, nil); err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodPut, botPath, f.owner.token, map[string]any{"model_id": f.model}, nil))
	}()
	key := uuid.NewString()
	var failed json.RawMessage
	if err = f.govern(f.owner, candidate, "evaluate", key, nil, &failed); err != nil {
		return err
	}
	var verdict struct {
		Status      string `json:"status"`
		NotExecuted bool   `json:"not_executed"`
		Passed      bool   `json:"passed"`
	}
	if err = json.Unmarshal(failed, &verdict); err != nil || verdict.Status != "error" || !verdict.NotExecuted || verdict.Passed {
		return fmt.Errorf("candidate governance: unavailable model did not prove pre-execution error: %s %v", failed, err)
	}
	if err = f.d.request(http.MethodPut, botPath, f.owner.token, map[string]any{"model_id": f.model}, nil); err != nil {
		return err
	}
	var replay json.RawMessage
	if err = f.govern(f.owner, candidate, "evaluate", key, nil, &replay); err != nil {
		return err
	}
	if string(failed) != string(replay) {
		return errors.New("candidate governance: same-key pre-execution error receipt changed")
	}
	after, err := f.observe(token, "observe")
	if err != nil {
		return err
	}
	if len(after.Requests) != len(before.Requests) {
		return errors.New("candidate governance: same-key pre-execution failure replayed model")
	}
	if err = f.govern(f.owner, candidate, "evaluate", uuid.NewString(), nil, &verdict); err != nil {
		return err
	}
	if verdict.Status != "passed" || !verdict.Passed {
		return fmt.Errorf("candidate governance: explicit fresh intent could not recover unused model execution: %+v", verdict)
	}
	if err = f.govern(f.owner, candidate, "evaluate", key, nil, &replay); err != nil {
		return err
	}
	if string(failed) != string(replay) {
		return errors.New("candidate governance: later successful intent overwrote original error receipt")
	}
	return nil
}

// These are public governance-contract checks using the controlled provider;
// they assert no model-quality claim and never execute generated scripts.
func (f *runReviewFixture) candidateReintegrationRegression(original, competitor governedCandidate, published skillVersionView, token string) error {
	stale, err := f.governed(f.owner, competitor.ID)
	if err != nil {
		return err
	}
	if stale.BaselineStale == nil || !*stale.BaselineStale || stale.CurrentVersionID == nil || *stale.CurrentVersionID != published.VersionID || stale.BaselineVersionID != nil || stale.CurrentContent == nil || stale.CurrentContent.Resources["references/correction-a.md"] != "Preserve zero totals." {
		return errors.New("candidate reintegration: competing publication did not expose the authorized current baseline")
	}
	outsider, err := f.d.register("candidate-comparison-outsider", f.suffix)
	if err != nil {
		return err
	}
	var denied governedCandidate
	if err = f.d.expectStatus(f.d.request(http.MethodGet, f.path("skill-candidates/"+competitor.ID.String()), outsider.token, nil, &denied), http.StatusForbidden, "unauthorized candidate comparison"); err != nil {
		return err
	}
	if denied.CurrentContent != nil || denied.BaselineContent != nil || denied.CurrentVersionID != nil || denied.Instructions != "" {
		return errors.New("candidate reintegration: unauthorized comparison exposed content")
	}
	resources := make(map[string]string)
	for path, body := range stale.CurrentContent.Resources {
		resources[path] = body
	}
	for path, body := range stale.Resources {
		resources[path] = body
	}
	// Explicitly resolve the package rather than asking the server to merge it.
	fields := map[string]any{
		"current_version_id": published.VersionID, "name": stale.Name, "description": stale.Description,
		"rationale":    "Preserve the independent zero-total and whitespace corrections.",
		"instructions": stale.CurrentContent.Instructions + "\nCorrection B: trim surrounding whitespace.",
		"resources":    resources, "scripts": map[string]string{}, "image_digest": "", "entry_point": "",
		"representative_task": "Return the accepted fixture result. " + token, "expected_output": "RUN_REVIEW_RESULT:" + token,
	}
	fields["current_version_id"] = uuid.NewString()
	if err = f.d.expectStatus(f.govern(f.owner, stale, "reintegrate", uuid.NewString(), fields, nil), http.StatusConflict, "unobserved reintegration baseline"); err != nil {
		return err
	}
	fields["current_version_id"] = published.VersionID
	delete(fields, "entry_point")
	if err = f.d.expectStatus(f.govern(f.owner, stale, "reintegrate", uuid.NewString(), fields, nil), http.StatusBadRequest, "partial reintegration package"); err != nil {
		return err
	}
	fields["entry_point"] = ""
	before, err := json.Marshal(stale)
	if err != nil {
		return err
	}
	key := uuid.NewString()
	var child, retry governedCandidate
	if err = f.govern(f.owner, stale, "reintegrate", key, fields, &child); err != nil {
		return err
	}
	if child.ID == stale.ID || child.ParentCandidateID == nil || *child.ParentCandidateID != stale.ID || child.SourceReviewID != stale.SourceReviewID || child.SourceRunID != stale.SourceRunID || child.ScopeKey != stale.ScopeKey || child.PublisherID != stale.PublisherID || child.BaselineVersionID == nil || *child.BaselineVersionID != published.VersionID || child.BaselineStale == nil || *child.BaselineStale || child.Status != "pending_validation" || child.Evaluation != nil || child.Digest == stale.Digest {
		return errors.New("candidate reintegration: child reused old validation/approval or lost exact lineage and baseline")
	}
	if child.Resources["references/correction-a.md"] != "Preserve zero totals." || child.Resources["references/correction-b.md"] != "Trim surrounding whitespace." {
		return errors.New("candidate reintegration: explicit package lost a compatible correction")
	}
	if err = f.govern(f.owner, stale, "reintegrate", key, fields, &retry); err != nil {
		return err
	}
	if retry.ID != child.ID || retry.Digest != child.Digest {
		return errors.New("candidate reintegration: retry created a second child")
	}
	if err = f.d.expectStatus(f.govern(f.owner, stale, "reintegrate", uuid.NewString(), fields, nil), http.StatusConflict, "identical reintegration under a distinct intent"); err != nil {
		return err
	}
	fields["instructions"] = "Different content under the same intent"
	if err = f.d.expectStatus(f.govern(f.owner, stale, "reintegrate", key, fields, nil), http.StatusConflict, "reintegration intent payload conflict"); err != nil {
		return err
	}
	fields["instructions"] = child.Instructions
	parent, err := f.governed(f.owner, stale.ID)
	if err != nil {
		return err
	}
	after, err := json.Marshal(parent)
	if err != nil || string(before) != string(after) {
		return errors.New("candidate reintegration: child creation changed the original candidate")
	}
	if err = f.d.expectStatus(f.govern(f.owner, child, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "reintegration requires new evaluation"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, child, "publish", uuid.NewString(), nil, nil), http.StatusConflict, "reintegration cannot reuse parent approval"); err != nil {
		return err
	}
	// A distinct maintenance intent may create a sibling, but neither sibling
	// can bypass the shared publication fence when the other publishes first.
	var sibling governedCandidate
	siblingFields := make(map[string]any, len(fields))
	for name, value := range fields {
		siblingFields[name] = value
	}
	siblingFields["rationale"] = "A distinct follow-up integration for the publication race."
	siblingFields["instructions"] = child.Instructions + "\nCorrection C: preserve the normalized header."
	siblingKey := uuid.NewString()
	if err = f.govern(f.owner, stale, "reintegrate", siblingKey, siblingFields, &sibling); err != nil {
		return err
	}
	child, err = f.readyGoverned(child, token)
	if err != nil {
		return err
	}
	sibling, err = f.readyGoverned(sibling, token)
	if err != nil {
		return err
	}
	type publication struct {
		candidate governedCandidate
		version   skillVersionView
		err       error
	}
	results := make(chan publication, 2)
	publishKey := uuid.NewString()
	siblingPublishKey := uuid.NewString()
	for _, attempt := range []struct {
		candidate governedCandidate
		key       string
	}{{child, publishKey}, {sibling, siblingPublishKey}} {
		go func() {
			result := publication{candidate: attempt.candidate}
			result.err = f.govern(f.owner, attempt.candidate, "publish", attempt.key, nil, &result.version)
			results <- result
		}()
	}
	winner, loser := <-results, <-results
	if winner.err != nil {
		winner, loser = loser, winner
	}
	if winner.err != nil {
		return winner.err
	}
	if err = f.d.expectStatus(loser.err, http.StatusConflict, "concurrent same-baseline publication"); err != nil {
		return err
	}
	if winner.candidate.ID != child.ID {
		child, sibling = sibling, child
		key, fields, publishKey = siblingKey, siblingFields, siblingPublishKey
	}
	integrated := winner.version
	var repeated skillVersionView
	if integrated.VersionID == published.VersionID || integrated.Version <= published.Version {
		return errors.New("candidate reintegration: publication did not create the next immutable version")
	}
	if err = f.govern(f.owner, child, "publish", publishKey, nil, &repeated); err != nil {
		return err
	}
	if repeated.VersionID != integrated.VersionID {
		return errors.New("candidate reintegration: repeated publication created another version")
	}
	if err = f.d.expectStatus(f.govern(f.owner, sibling, "publish", uuid.NewString(), nil, nil), http.StatusConflict, "publication advanced after reintegration"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, stale, "reintegrate", uuid.NewString(), fields, nil), http.StatusConflict, "reintegration cannot retry past a newer baseline"); err != nil {
		return err
	}
	if err = f.govern(f.owner, stale, "reintegrate", key, fields, &retry); err != nil {
		return err
	}
	if retry.ID != child.ID || retry.Status != "published" || retry.CurrentVersionID == nil || *retry.CurrentVersionID != integrated.VersionID {
		return errors.New("candidate reintegration: replay did not reauthorize and return the existing child")
	}
	originalReadback, err := f.governed(f.owner, original.ID)
	if err != nil {
		return err
	}
	if originalReadback.Resources["references/correction-a.md"] != "Preserve zero totals." || originalReadback.Digest != original.Digest {
		return errors.New("candidate reintegration: later publication changed its immutable predecessor")
	}
	return nil
}
