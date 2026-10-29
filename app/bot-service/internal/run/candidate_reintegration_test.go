package run

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func reintegrationFixture() (*candidateState, candidateRequest, *candidateBaselineContent) {
	parent := "019b24d0-0000-7000-8000-000000000001"
	current := "019b24d0-0000-7000-8000-000000000002"
	review := "019b24d0-0000-7000-8000-000000000003"
	publisher := "019b24d0-0000-7000-8000-000000000004"
	deadline := time.Now().Add(time.Hour)
	c := reviewCandidate{ID: parent, SourceReviewID: review, SkillID: "skill", SourceRunID: "run", PublisherID: publisher, ScopeKey: "conversation:binding", Name: "compatible-fixes", Description: "Preserve independent fixes", Rationale: "An independently authorized correction", Instructions: "Keep the candidate correction", Resources: map[string]string{"references/candidate.txt": "candidate correction"}, Scripts: map[string]string{"scripts/main.sh": "printf correction"}, ImageDigest: "sha256:old", EntryPoint: "scripts/main.sh", RepresentativeTask: "Return the exact result", ExpectedOutput: "correction", EvidenceDigest: "authorized-evidence", ExpiresAt: deadline.Unix(), Status: "approved", CodeReviewRequired: true, CodeReviewDigest: "reviewed-script", CodeReviewIdentity: "trusted-admission", CodeReviewedBy: publisher, CodeReviewedMemberID: "reviewer-member", ApprovedMemberID: "approver-member", CanViewMaterials: true, CanMaintain: true, CanPublish: true, CanReviewCode: true, Evaluation: &candidateEvaluation{Status: "passed", Passed: true}}
	c.Digest = candidateContentDigest(&c)
	c.CodeReviewDigest = candidateScriptDigest(&c)
	state := &candidateState{Content: c, Row: governedCandidate{ID: parent, BotID: "bot", SkillID: c.SkillID, SourceReviewID: &review, Digest: c.Digest, Status: "approved", EvaluationDigest: "evaluation", ApprovedDigest: c.Digest, ApprovedEvaluationDigest: "evaluation", ApprovedBy: &publisher}, Review: runReview{ID: review, CandidateID: &parent, ExpiresAt: deadline, EvidenceDigest: c.EvidenceDigest}}
	name, description, rationale := c.Name, c.Description, c.Rationale
	instructions, image, entry := "Keep the current correction AND the candidate correction", c.ImageDigest, c.EntryPoint
	resources := map[string]string{"references/current.txt": "published correction", "references/candidate.txt": "candidate correction"}
	scripts := map[string]string{"scripts/main.sh": "printf correction"}
	req := candidateRequest{CandidateID: parent, Digest: c.Digest, SubmissionKey: "one-intent", CurrentVersionID: &current, Name: &name, Description: &description, Rationale: &rationale, Instructions: &instructions, Resources: &resources, Scripts: &scripts, ImageDigest: &image, EntryPoint: &entry}
	baseline := &candidateBaselineContent{Instructions: "Keep the current correction", Resources: json.RawMessage(`{"references/current.txt":"published correction"}`), Scripts: map[string]string{}, ImageDigest: "", EntryPoint: ""}
	return state, req, baseline
}

func TestCandidateReintegrationRequiresExactObservedBaselineAndCompletePackage(t *testing.T) {
	for _, field := range []string{"current_version_id", "name", "description", "rationale", "instructions", "resources", "scripts", "image_digest", "entry_point"} {
		t.Run(field, func(t *testing.T) {
			_, req, _ := reintegrationFixture()
			switch field {
			case "current_version_id":
				req.CurrentVersionID = nil
			case "name":
				req.Name = nil
			case "description":
				req.Description = nil
			case "rationale":
				req.Rationale = nil
			case "instructions":
				req.Instructions = nil
			case "resources":
				req.Resources = nil
			case "scripts":
				req.Scripts = nil
			case "image_digest":
				req.ImageDigest = nil
			case "entry_point":
				req.EntryPoint = nil
			}
			body, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			var service Service
			_, err = service.CandidateOperation(t.Context(), "019b24d0-0000-7000-8000-000000000004", "019b24d0-0000-7000-8000-000000000005", "candidate-reintegrate", body)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("missing %s: got %v, want InvalidArgument before database access", field, err)
			}
		})
	}
	_, req, _ := reintegrationFixture()
	invalid := "not-a-version"
	req.CurrentVersionID = &invalid
	if err := validateCandidateReintegration(req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid observed version accepted: %v", err)
	}
}

func TestCandidateReintegrationPreservesParentAndBindsFreshChildIdentity(t *testing.T) {
	state, req, baseline := reintegrationFixture()
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var service Service
	child, err := service.newReintegratedCandidate(state, req, baseline)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("reintegration mutated the original candidate, evidence, approval or review")
	}
	c := &child.Content
	if c.ID == state.Content.ID || c.ParentCandidateID == nil || *c.ParentCandidateID != state.Content.ID || c.SourceReviewID != state.Review.ID || c.SourceRunID != state.Content.SourceRunID || c.PublisherID != state.Content.PublisherID || c.ScopeKey != state.Content.ScopeKey {
		t.Fatalf("child lost immutable source lineage: %+v", c)
	}
	if c.BaselineVersionID == nil || *c.BaselineVersionID != *req.CurrentVersionID || c.BaselineInstructions != baseline.Instructions || !reflect.DeepEqual(c.SourceVersionIDs, []string{*req.CurrentVersionID}) {
		t.Fatalf("child is not bound to the observed current baseline: %+v", c)
	}
	if c.Instructions != *req.Instructions || c.Resources["references/current.txt"] != "published correction" || c.Resources["references/candidate.txt"] != "candidate correction" {
		t.Fatal("explicit integrated content did not preserve both compatible corrections")
	}
	if c.Digest == state.Content.Digest || c.Digest != candidateContentDigest(c) || c.Status != "pending_validation" || c.Evaluation != nil || child.Row.EvaluationDigest != "" || child.Row.ApprovedDigest != "" || child.Row.ApprovedEvaluationDigest != "" || child.Row.ApprovedBy != nil || c.ApprovedMemberID != "" {
		t.Fatal("child reused its parent's identity, validation or approval")
	}
	if c.EvidenceDigest != state.Content.EvidenceDigest || c.ExpiresAt != state.Content.ExpiresAt || c.CodeReviewIdentity != state.Content.CodeReviewIdentity {
		t.Fatal("unchanged authorized evidence or script identity was not retained")
	}
	projected := *c
	projected.CurrentContent = &candidateBaselineContent{Instructions: "a later publication"}
	if candidateContentDigest(&projected) != c.Digest {
		t.Fatal("display-only current content changed candidate identity")
	}
}

func TestCandidateReintegrationInvalidatesChangedExecutionReview(t *testing.T) {
	for _, changed := range []string{"scripts", "image_digest", "entry_point"} {
		t.Run(changed, func(t *testing.T) {
			state, req, baseline := reintegrationFixture()
			switch changed {
			case "scripts":
				(*req.Scripts)["scripts/main.sh"] = "printf integrated"
			case "image_digest":
				*req.ImageDigest = "sha256:changed"
			case "entry_point":
				*req.EntryPoint = "scripts/other.sh"
			}
			var service Service
			child, err := service.newReintegratedCandidate(state, req, baseline)
			if err != nil {
				t.Fatal(err)
			}
			c := &child.Content
			if !c.CodeReviewRequired || c.CodeReviewDigest != "" || c.CodeReviewIdentity != "" || c.CodeReviewedBy != "" || c.CodeReviewedMemberID != "" || c.Evaluation != nil || child.Row.ApprovedDigest != "" {
				t.Fatalf("changed %s retained trusted review, validation or approval", changed)
			}
		})
	}
}

func TestExpiredCandidateDoesNotExposeComparisonMaterials(t *testing.T) {
	state, req, baseline := reintegrationFixture()
	stale := true
	state.Content.ExpiresAt = time.Now().Add(-time.Second).Unix()
	state.Content.BaselineContent, state.Content.CurrentContent = baseline, baseline
	state.Content.CurrentVersionID, state.Content.BaselineStale = req.CurrentVersionID, &stale
	view := candidateView(state)
	if view.BaselineContent != nil || view.CurrentContent != nil || view.CurrentVersionID != nil || view.BaselineStale != nil || view.Instructions != "" || view.Resources != nil || view.Scripts != nil {
		t.Fatal("expired evidence projection exposed comparison content or current version identity")
	}
}
