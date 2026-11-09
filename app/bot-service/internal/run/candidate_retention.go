package run

import "time"

func candidateEvidenceCurrent(state *candidateState) bool {
	c := &state.Content
	now := time.Now()
	if c.ExpiresAt <= now.Unix() || c.Status == "expired" {
		return false
	}
	if c.EvidenceID != "" {
		return c.EvidenceLicenseExpiresAt > now.Unix() && c.ExpiresAt <= c.EvidenceLicenseExpiresAt
	}
	return state.Review.ExpiresAt.After(now) && c.ExpiresAt <= state.Review.ExpiresAt.Unix()
}

// Expired evidence has an allowlisted maintenance projection, never a partially
// redacted content object. Digest remains the optimistic concurrency identity.
func candidateView(state *candidateState) reviewCandidate {
	c := state.Content
	if candidateEvidenceCurrent(state) {
		return c
	}
	statusValue := "expired"
	if state.Row.Status == "published" || state.Row.Status == "rejected" {
		statusValue = state.Row.Status
	}
	return reviewCandidate{ID: c.ID, Origin: c.Origin, PackageDigest: c.PackageDigest, PublisherAuthorizationVersion: c.PublisherAuthorizationVersion, ParentCandidateID: c.ParentCandidateID, SourceReviewID: c.SourceReviewID, SkillID: c.SkillID, SourceRunID: c.SourceRunID, PublisherID: c.PublisherID, ScopeKey: c.ScopeKey, Intent: c.Intent, Digest: c.Digest, BaselineVersionID: c.BaselineVersionID, PublishedVersionID: c.PublishedVersionID, Status: statusValue, CodeReviewRequired: c.CodeReviewRequired, EvidenceID: c.EvidenceID, EvidenceDigest: c.EvidenceDigest, EvidenceLicenseExpiresAt: c.EvidenceLicenseExpiresAt, EvidenceRevision: c.EvidenceRevision, ExpiresAt: c.ExpiresAt, CanMaintain: c.CanMaintain, Reason: "evidence expired; submit newly authorized package, samples and independent expected results, then validate and approve again"}
}

// Immutable publication retains execution identity and non-sensitive conclusions,
// not representative business inputs, expected results, evidence or free text.
func candidatePublishedContract(c *reviewCandidate) map[string]any {
	var conclusion map[string]any
	if c.Evaluation != nil {
		e := c.Evaluation
		conclusion = map[string]any{"status": e.Status, "passed": e.Passed, "digest": e.Digest, "task_digest": e.TaskDigest, "expected_digest": e.ExpectedDigest, "output_digest": e.OutputDigest, "model_version": e.ModelVersion, "environment_digest": e.EnvironmentDigest, "baseline_digest": e.BaselineDigest, "evidence_digest": e.EvidenceDigest, "execution_id": e.ExecutionID, "code_review_identity": e.CodeReviewIdentity, "created_at": e.CreatedAt}
	}
	return map[string]any{"id": c.ID, "origin": c.Origin, "package_digest": c.PackageDigest, "digest": c.Digest, "baseline_version_id": c.BaselineVersionID, "scripts": c.Scripts, "image_digest": c.ImageDigest, "entry_point": c.EntryPoint, "code_review_digest": c.CodeReviewDigest, "code_review_identity": c.CodeReviewIdentity, "evaluation": conclusion, "io_contract_digest": mustReviewDigest([]string{c.RepresentativeTask, c.ExpectedOutput, c.InputBase64, c.ExpectedOutputBase64})}
}
