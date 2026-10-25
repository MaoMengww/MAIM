package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (f *runReviewFixture) retentionGovernance() error {
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err := f.d.request(http.MethodPost, "/convs", f.owner.token, map[string]any{"type": "group", "group_name": "retention-" + f.suffix, "member_ids": []entityID{f.requester.id, f.admin.id}}, &conv); err != nil {
		return err
	}
	if err := f.d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", f.owner.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
		return err
	}
	isolated := *f
	isolated.conv, isolated.sources = conv.ID, make(map[entityID]string)
	expiring, _, token, err := isolated.newGoverned("expiry", "valuable")
	if err != nil {
		return err
	}
	deadline := time.Now().Add(8 * time.Second).Unix()
	if err = isolated.govern(f.owner, expiring, "revise", uuid.NewString(), map[string]any{"expires_at": deadline}, &expiring); err != nil {
		return err
	}
	if expiring, err = isolated.readyGoverned(expiring, token); err != nil {
		return err
	}
	time.Sleep(time.Until(time.Unix(deadline, 0).Add(100 * time.Millisecond)))
	for _, operation := range []string{"approve", "publish"} {
		if err = f.d.expectStatus(isolated.govern(f.owner, expiring, operation, uuid.NewString(), nil, nil), http.StatusConflict, "expired approved candidate "+operation); err != nil {
			return err
		}
	}
	return isolated.candidateEvidenceRenewal(expiring, token)
}

// The client selects a short absolute deadline through the real public contract;
// no production clock override or private database mutation is needed.
func (f *runReviewFixture) candidateEvidenceRenewal(old governedCandidate, token string) error {
	expired, err := f.governed(f.owner, old.ID)
	if err != nil {
		return err
	}
	if err = expiredCandidateMetadata(expired, token); err != nil {
		return err
	}
	var rows []governedCandidate
	if err = f.d.request(http.MethodGet, f.path("skill-candidates"), f.owner.token, nil, &rows); err != nil {
		return err
	}
	found := false
	for _, row := range rows {
		if row.ID == expired.ID {
			found = true
			if err = expiredCandidateMetadata(row, token); err != nil {
				return err
			}
		}
	}
	if !found {
		return errors.New("candidate retention: expired candidate disappeared instead of offering fresh evidence")
	}
	if err = f.d.expectStatus(f.govern(f.owner, expired, "revise", uuid.NewString(), map[string]any{"expires_at": time.Now().Add(time.Hour).Unix()}, nil), http.StatusConflict, "timestamp-only evidence renewal"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, expired, "evaluate", uuid.NewString(), nil, nil), http.StatusConflict, "expired evidence validation"); err != nil {
		return err
	}

	// New consent and complete synthetic content establish a new evidence identity,
	// not permission to restore the deleted source archive or its prior approval.
	deadline := time.Now().Add(12 * time.Second).Unix()
	fields := map[string]any{
		"evidence_authorized": true, "source_license_expires_at": deadline,
		"name": "renewed-synthetic-evidence", "description": "Synthetic fixed-result instruction",
		"target_goal": "Summarize fixed rows", "applicability": "Fixed CSV input only",
		"rationale":    "New independently permitted synthetic example",
		"instructions": old.Instructions, "resources": old.Resources,
		"scripts": map[string]string{}, "image_digest": "", "entry_point": "",
		"representative_task": "Return the accepted fixture result. " + token,
		"expected_output":     "RUN_REVIEW_RESULT:" + token,
	}
	key := uuid.NewString()
	var renewed governedCandidate
	if err = f.govern(f.owner, expired, "revise", key, fields, &renewed); err != nil {
		return fmt.Errorf("candidate retention: independently authorized renewal: %w", err)
	}
	if renewed.Status != "pending_validation" || !renewed.CanViewMaterials || renewed.EvidenceID == "" || renewed.EvidenceID == old.EvidenceID || renewed.EvidenceDigest == old.EvidenceDigest || renewed.EvidenceRevision <= old.EvidenceRevision || renewed.Digest == old.Digest || renewed.ExpiresAt != deadline || renewed.Evaluation != nil {
		return errors.New("candidate retention: renewed evidence retained old identity, approval, validation or exceeded its license")
	}
	var replay governedCandidate
	if err = f.govern(f.owner, expired, "revise", key, fields, &replay); err != nil {
		return err
	}
	if replay.EvidenceID != renewed.EvidenceID || replay.Digest != renewed.Digest || replay.ExpiresAt != deadline {
		return errors.New("candidate retention: retry renewed evidence identity or deadline")
	}
	if err = f.d.expectStatus(f.govern(f.owner, renewed, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "renewed evidence without fresh validation"); err != nil {
		return err
	}
	if err = f.d.expectStatus(f.govern(f.owner, old, "publish", uuid.NewString(), nil, nil), http.StatusConflict, "stale approval after renewal"); err != nil {
		return err
	}
	var verdict struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
	}
	evaluationKey := uuid.NewString()
	if err = f.govern(f.owner, renewed, "evaluate", evaluationKey, nil, &verdict); err != nil || !verdict.Passed || verdict.Status != "passed" {
		return fmt.Errorf("candidate retention: renewed verification failed: %+v %v", verdict, err)
	}
	var approved governedCandidate
	approvalKey := uuid.NewString()
	if err = f.govern(f.owner, renewed, "approve", approvalKey, nil, &approved); err != nil {
		return err
	}
	var version skillVersionView
	publishKey := uuid.NewString()
	if err = f.govern(f.owner, approved, "publish", publishKey, nil, &version); err != nil {
		return err
	}
	if version.VersionID == "" || version.Status != "published" {
		return errors.New("candidate retention: renewed evidence did not publish")
	}
	for range 2 {
		view, err := f.governed(f.owner, approved.ID)
		if err != nil || view.ExpiresAt != deadline {
			return fmt.Errorf("candidate retention: read extended retention: %v", err)
		}
	}
	time.Sleep(time.Until(time.Unix(deadline, 0).Add(100 * time.Millisecond)))
	published, err := f.governed(f.owner, approved.ID)
	if err != nil {
		return err
	}
	if published.Status != "published" || published.CanViewMaterials || published.RepresentativeTask != "" || published.ExpectedOutput != "" || published.Evidence != "" {
		return errors.New("candidate retention: published conclusion retained expired examples")
	}
	for _, intent := range []struct{ operation, key string }{{"evaluate", evaluationKey}, {"approve", approvalKey}} {
		if err = f.d.expectStatus(f.govern(f.owner, approved, intent.operation, intent.key, nil, nil), http.StatusConflict, "expired successful operation replay"); err != nil {
			return err
		}
	}
	// Material TTL does not silently disable an independently published package.
	run, err := f.start(token, "conversation", uuid.NewString(), []entityID{version.VersionID})
	if err != nil {
		return fmt.Errorf("candidate retention: published package unusable after sample expiry: %w", err)
	}
	if _, err = f.d.waitRun(f.requester, run.ID); err != nil {
		return err
	}
	if err = f.candidateExpiredBaselineRenewal(published, version); err != nil {
		return err
	}
	f.browser["retention"] = map[string]any{"candidate_id": old.ID, "version_id": version.VersionID, "evidence_id": renewed.EvidenceID, "expires_at": deadline}
	if err = f.save(); err != nil {
		return err
	}
	fmt.Println("E2E PASS: expired safe metadata, timestamp-only denial, new licensed evidence identity, non-renewing retry/read, fresh validation/approval/publication, published package surviving sample expiry, independently licensed baseline regression samples")
	return nil
}

func (f *runReviewFixture) candidateExpiredBaselineRenewal(baseline governedCandidate, version skillVersionView) error {
	token, err := f.arm("governance-expired-baseline-renewal", "catalog-only", version.VersionID)
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
		if entry.VersionID == version.VersionID && entry.Digest == version.Digest {
			frozen = true
		}
	}
	if !frozen {
		return errors.New("candidate retention: expired-sample baseline package was not frozen unchanged in the same scope")
	}
	review, err := f.waitReview(run, "conversation", "candidate_generated")
	if err != nil {
		return err
	}
	if review.Candidate == nil || review.Candidate.Intent != "modify" || review.Candidate.BaselineVersionID == nil || *review.Candidate.BaselineVersionID != version.VersionID {
		return errors.New("candidate retention: fresh Run did not produce a modification bound to the expired-sample baseline")
	}
	candidate, err := f.governed(f.owner, review.Candidate.ID)
	if err != nil {
		return err
	}
	if err = f.govern(f.owner, candidate, "revise", uuid.NewString(), map[string]any{
		"representative_task": "Return the new scenario result. " + token,
		"expected_output":     "RUN_REVIEW_RESULT:" + token,
	}, &candidate); err != nil {
		return err
	}
	var evaluation struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
	}
	if err = f.govern(f.owner, candidate, "evaluate", uuid.NewString(), nil, &evaluation); err != nil {
		return err
	}
	if evaluation.Status != "insufficient" || evaluation.Passed {
		return fmt.Errorf("candidate retention: expired baseline samples were reused or silently skipped: %+v", evaluation)
	}
	if err = f.d.expectStatus(f.govern(f.owner, candidate, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "expired baseline without replacement samples"); err != nil {
		return err
	}
	type baselineSample struct {
		VersionID            entityID `json:"version_id"`
		RepresentativeTask   string   `json:"representative_task"`
		ExpectedOutput       string   `json:"expected_output"`
		InputBase64          string   `json:"input_b64"`
		ExpectedOutputBase64 string   `json:"expected_output_b64"`
	}
	sample := baselineSample{
		VersionID: version.VersionID, RepresentativeTask: "Newly licensed baseline task. " + token,
		ExpectedOutput: "RUN_REVIEW_RESULT:" + token,
	}
	deadline := time.Now().Add(time.Hour).Unix()
	fields := map[string]any{
		"evidence_authorized": true, "source_license_expires_at": deadline,
		"name": "renewed-baseline-regression", "description": "Independently authorized synthetic regression suite",
		"target_goal": candidate.TargetGoal, "applicability": candidate.Applicability,
		"rationale":    "New consent for the candidate and its exact ancestor's regression example",
		"instructions": candidate.Instructions, "resources": candidate.Resources,
		"scripts": map[string]string{}, "image_digest": "", "entry_point": "",
		"representative_task": "Newly licensed candidate task. " + token,
		"expected_output":     "RUN_REVIEW_RESULT:" + token, "baseline_samples": []baselineSample{sample},
	}
	var renewed struct {
		governedCandidate
		BaselineSamples []baselineSample `json:"baseline_samples"`
	}
	key := uuid.NewString()
	if err = f.govern(f.owner, candidate, "revise", key, fields, &renewed); err != nil {
		return fmt.Errorf("candidate retention: newly licensed baseline samples: %w", err)
	}
	if renewed.Status != "pending_validation" || !renewed.CanViewMaterials || renewed.Evaluation != nil || renewed.EvidenceID == "" || renewed.EvidenceID == candidate.EvidenceID || renewed.EvidenceID == baseline.EvidenceID || renewed.EvidenceDigest == candidate.EvidenceDigest || renewed.Digest == candidate.Digest || renewed.ExpiresAt != deadline || len(renewed.BaselineSamples) != 1 || renewed.BaselineSamples[0] != sample || renewed.BaselineVersionID == nil || *renewed.BaselineVersionID != version.VersionID {
		return errors.New("candidate retention: replacement baseline suite lost its exact version, new evidence identity, license or pending validation state")
	}
	var replay governedCandidate
	if err = f.govern(f.owner, candidate, "revise", key, fields, &replay); err != nil {
		return err
	}
	if replay.EvidenceID != renewed.EvidenceID || replay.Digest != renewed.Digest || replay.ExpiresAt != deadline {
		return errors.New("candidate retention: baseline renewal replay changed evidence identity or extended its deadline")
	}
	if err = f.d.expectStatus(f.govern(f.owner, renewed.governedCandidate, "approve", uuid.NewString(), nil, nil), http.StatusConflict, "replacement baseline samples without fresh validation"); err != nil {
		return err
	}
	if err = f.govern(f.owner, renewed.governedCandidate, "evaluate", uuid.NewString(), nil, &evaluation); err != nil || evaluation.Status != "passed" || !evaluation.Passed {
		return fmt.Errorf("candidate retention: licensed baseline regression suite failed: %+v %v", evaluation, err)
	}
	observation, err := f.observe(token, "observe")
	if err != nil {
		return err
	}
	var candidateExecuted, baselineExecuted bool
	for _, request := range observation.Requests {
		if request.Phase != "run" {
			continue
		}
		raw, err := json.Marshal(request.Body)
		if err != nil {
			return err
		}
		candidateExecuted = candidateExecuted || strings.Contains(string(raw), fields["representative_task"].(string))
		baselineExecuted = baselineExecuted || strings.Contains(string(raw), sample.RepresentativeTask)
	}
	if !candidateExecuted || !baselineExecuted {
		return errors.New("candidate retention: passing verdict did not execute both newly licensed tasks through the provider")
	}
	var approved governedCandidate
	if err = f.govern(f.owner, renewed.governedCandidate, "approve", uuid.NewString(), nil, &approved); err != nil {
		return err
	}
	var published skillVersionView
	if err = f.govern(f.owner, approved, "publish", uuid.NewString(), nil, &published); err != nil {
		return err
	}
	if published.VersionID == "" || published.VersionID == version.VersionID || published.Version <= version.Version || published.Status != "published" {
		return errors.New("candidate retention: licensed baseline suite did not publish a distinct successor")
	}
	original, err := f.governed(f.owner, baseline.ID)
	if err != nil {
		return err
	}
	if original.Status != "published" || original.Digest != baseline.Digest || original.EvidenceID != baseline.EvidenceID || original.ExpiresAt != baseline.ExpiresAt || original.CanViewMaterials || original.RepresentativeTask != "" || original.ExpectedOutput != "" || original.Evidence != "" {
		return errors.New("candidate retention: new regression evidence restored or extended the expired baseline's materials or changed its published conclusion")
	}
	var history []skillPackageView
	if err = f.d.request(http.MethodGet, f.path("skill-versions"), f.owner.token, nil, &history); err != nil {
		return err
	}
	unchanged := false
	for _, pkg := range history {
		for _, current := range pkg.Versions {
			if current.VersionID == version.VersionID {
				unchanged = current.Digest == version.Digest && current.Version == version.Version && current.Status == "published"
			}
		}
	}
	if !unchanged {
		return errors.New("candidate retention: replacement samples modified or removed the immutable published baseline version")
	}
	return f.unchanged(run)
}

func expiredCandidateMetadata(value governedCandidate, token string) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if value.Status != "expired" || value.CanViewMaterials || !value.CanMaintain || value.Reason == "" || value.Instructions != "" || value.TargetGoal != "" || value.Rationale != "" || value.RepresentativeTask != "" || value.ExpectedOutput != "" || value.Evidence != "" || len(value.Resources) != 0 || len(value.Scripts) != 0 || value.Evaluation != nil || strings.Contains(string(raw), token) {
		return errors.New("candidate retention: expired response did not preserve safe metadata while removing original material")
	}
	return nil
}
