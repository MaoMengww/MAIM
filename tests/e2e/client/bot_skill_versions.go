package main

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
)

func (f *skillFixture) versionLifecycle(instructions, resource, task, evaluation, bodyMarker, resourceMarker string, finished publicRun) error {
	waitToken := "WAIT_" + uuid.NewString()
	if err := f.d.executorProvider("/instruction-skills/arm", map[string]any{"token": waitToken, "version_id": f.version.VersionID.String(), "evaluation": evaluation, "answer": "OLD_RESUMED", "body_marker": bodyMarker, "resource": "references/format.md", "resource_marker": resourceMarker, "mode": "waiting"}, nil); err != nil {
		return err
	}
	waiting, err := f.startRun("Wait before publication. "+waitToken, nil)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.requester, waiting.ID, "waiting_input"); err != nil {
		return err
	}
	candidate, err := f.importPackage(instructions+"\nUpdated reviewed instructions.\n", "references/format.md", resource, task, evaluation, uuid.NewString())
	if err != nil {
		return err
	}
	for _, action := range []string{"evaluate", "approve"} {
		if _, err = f.action(candidate.ID, action); err != nil {
			return err
		}
	}
	raw, err := f.action(candidate.ID, "publish")
	if err != nil {
		return err
	}
	var next skillVersionView
	if err = json.Unmarshal(raw, &next); err != nil {
		return err
	}
	if !next.Enabled || next.VersionID == f.version.VersionID {
		return fmt.Errorf("skills: compatible publication not adopted: %s", raw)
	}
	old, err := f.d.getRun(f.requester, finished.ID)
	if err != nil {
		return err
	}
	if len(old.FrozenSkills) != 1 || old.FrozenSkills[0].VersionID != f.version.VersionID {
		return fmt.Errorf("skills: publication changed frozen run")
	}
	if err = f.arm(next.VersionID, evaluation, "NEW_VERSION_RESULT", bodyMarker, "references/format.md", resourceMarker); err != nil {
		return err
	}
	fresh, err := f.startRun("Use automatically adopted version. "+f.token, nil)
	if err != nil {
		return err
	}
	fresh, err = f.d.waitRun(f.requester, fresh.ID)
	if err != nil {
		return err
	}
	if fresh.Status != "succeeded" || fresh.ResultText != "NEW_VERSION_RESULT" || len(fresh.FrozenSkills) != 1 || fresh.FrozenSkills[0].VersionID != next.VersionID || !fresh.FrozenSkills[0].Loaded {
		return fmt.Errorf("skills: subsequent Run did not actually load upgraded version: %+v", fresh)
	}
	if _, err = f.d.interactionControl(f.requester, waiting.ID, "input", "SKILL_RESUME "+waitToken, uuid.NewString(), nil); err != nil {
		return err
	}
	resumed, err := f.d.waitRun(f.requester, waiting.ID)
	if err != nil {
		return err
	}
	if resumed.Status != "succeeded" || resumed.ResultText != "OLD_RESUMED" || len(resumed.FrozenSkills) != 1 || resumed.FrozenSkills[0].VersionID != f.version.VersionID || !resumed.FrozenSkills[0].Loaded {
		return fmt.Errorf("skills: waiting Run did not resume with its original loaded version: %+v", resumed)
	}
	var packages []skillPackageView
	if err = f.d.request(http.MethodGet, "/bots/"+f.bot.ID.String()+"/skill-versions", f.owner.token, nil, &packages); err != nil {
		return err
	}
	var current skillPackageView
	for _, pkg := range packages {
		if pkg.SkillID == candidate.SkillID {
			current = pkg
		}
	}
	if current.EnabledVersion == nil || current.EnabledVersion.VersionID != next.VersionID {
		return fmt.Errorf("skills: public governance query omitted adopted version")
	}
	change := func(version skillVersionView, action, key string) error {
		body := map[string]any{"submission_key": key, "digest": version.Digest, "expected_revision": current.BindingRevision, "reason": "public lifecycle verification"}
		current = skillPackageView{}
		return f.d.request(http.MethodPost, "/bots/"+f.bot.ID.String()+"/skill-versions/"+version.VersionID.String()+"/"+action, f.owner.token, body, &current)
	}
	rollbackKey := uuid.NewString()
	if err = change(f.version, "rollback", rollbackKey); err != nil {
		return err
	}
	if current.EnabledVersion == nil || current.EnabledVersion.VersionID != f.version.VersionID || !current.RollbackHold {
		return fmt.Errorf("skills: rollback did not retain explicit old version")
	}
	replay := current
	if err = f.d.request(http.MethodPost, "/bots/"+f.bot.ID.String()+"/skill-versions/"+f.version.VersionID.String()+"/rollback", f.owner.token, map[string]any{"submission_key": rollbackKey, "digest": f.version.Digest, "expected_revision": current.BindingRevision - 1, "reason": "public lifecycle verification"}, &replay); err != nil {
		return err
	}
	if replay.BindingRevision != current.BindingRevision {
		return fmt.Errorf("skills: rollback retry mutated binding")
	}
	blocked, err := f.importPackage(instructions+"\nChanged input output contract.\n", "references/format.md", resource, task+" Contract changed.", evaluation, uuid.NewString())
	if err != nil {
		return err
	}
	for _, action := range []string{"evaluate", "approve"} {
		if _, err = f.action(blocked.ID, action); err != nil {
			return err
		}
	}
	raw, err = f.action(blocked.ID, "publish")
	if err != nil {
		return err
	}
	var changed skillVersionView
	if err = json.Unmarshal(raw, &changed); err != nil {
		return err
	}
	if changed.Enabled {
		return fmt.Errorf("skills: changed IO contract automatically adopted")
	}
	if err = f.d.request(http.MethodGet, "/bots/"+f.bot.ID.String()+"/skill-versions", f.owner.token, nil, &packages); err != nil {
		return err
	}
	for _, pkg := range packages {
		if pkg.SkillID == candidate.SkillID {
			current = pkg
		}
	}
	if current.EnabledVersion == nil || current.EnabledVersion.VersionID != f.version.VersionID || current.BlockedUpgrade == nil || current.BlockedUpgrade.Version.VersionID != changed.VersionID {
		return fmt.Errorf("skills: blocked upgrade lost prior effective version")
	}
	if err = change(changed, "confirm", uuid.NewString()); err != nil {
		return err
	}
	if current.EnabledVersion == nil || current.EnabledVersion.VersionID != changed.VersionID || current.BlockedUpgrade != nil {
		return fmt.Errorf("skills: explicit confirmation did not adopt reviewed contract")
	}
	revokeToken := "REVOKE_" + uuid.NewString()
	if err = f.d.executorProvider("/instruction-skills/arm", map[string]any{"token": revokeToken, "version_id": changed.VersionID.String(), "evaluation": evaluation, "answer": "MUST_NOT_RESUME", "body_marker": bodyMarker, "resource": "references/format.md", "resource_marker": resourceMarker, "mode": "waiting"}, nil); err != nil {
		return err
	}
	active, err := f.startRun("Wait for version revocation. "+revokeToken, nil)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.requester, active.ID, "waiting_input"); err != nil {
		return err
	}
	if err = change(changed, "disable", uuid.NewString()); err != nil {
		return err
	}
	if current.EnabledVersion != nil {
		return fmt.Errorf("skills: disabled version silently fell back")
	}
	revoked, err := f.d.waitRun(f.requester, active.ID)
	if err != nil {
		return err
	}
	if revoked.Status != "stopped" || revoked.StopReason != "skill_version_disabled" || revoked.StopStatus != "confirmed" || revoked.ResultText != "" {
		return fmt.Errorf("skills: disabled frozen version did not stop waiting Run: %+v", revoked)
	}
	old, err = f.d.getRun(f.requester, finished.ID)
	if err != nil {
		return err
	}
	if len(old.FrozenSkills) != 1 || old.FrozenSkills[0].VersionID != f.version.VersionID {
		return fmt.Errorf("skills: governance rewrote historical freeze")
	}
	return nil
}
