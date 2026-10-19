package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type skillCandidateView struct {
	ID                 entityID  `json:"id"`
	SkillID            entityID  `json:"skill_id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	Digest             string    `json:"digest"`
	License            string    `json:"license"`
	Status             string    `json:"status"`
	Failure            string    `json:"failure"`
	BaselineVersionID  *entityID `json:"baseline_version_id"`
	PublishedVersionID *entityID `json:"published_version_id"`
	ApprovedDigest     string    `json:"approved_digest"`
	Evaluation         *struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
		Reason string `json:"reason"`
		Digest string `json:"digest"`
	} `json:"evaluation"`
}

type skillVersionView struct {
	VersionID entityID `json:"version_id"`
	Version   int      `json:"version"`
	Name      string   `json:"name"`
	Digest    string   `json:"digest"`
	Status    string   `json:"status"`
	Enabled   bool     `json:"enabled"`
}

type skillPackageView struct {
	SkillID        entityID             `json:"skill_id"`
	Name           string               `json:"name"`
	License        string               `json:"license"`
	EnabledVersion *skillVersionView    `json:"enabled_version"`
	Versions       []skillVersionView   `json:"versions"`
	Candidates     []skillCandidateView `json:"candidates"`
}

type availableSkillView struct {
	VersionID   entityID `json:"version_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     int      `json:"version"`
}

type frozenSkillView struct {
	VersionID   entityID `json:"version_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Digest      string   `json:"digest"`
	Version     int      `json:"version"`
	Explicit    bool     `json:"explicit"`
	Loaded      bool     `json:"loaded"`
}

type skillObservation struct {
	Requests []map[string]any `json:"requests"`
}

func skillZip(instructions, resourcePath, resource string) (string, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("SKILL.md")
	if err != nil {
		return "", err
	}
	if _, err = entry.Write([]byte(instructions)); err != nil {
		return "", err
	}
	if resourcePath != "" {
		entry, err = writer.Create(resourcePath)
		if err != nil {
			return "", err
		}
		if _, err = entry.Write([]byte(resource)); err != nil {
			return "", err
		}
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

func rawZip(members map[string]string) (string, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range members {
		entry, err := writer.Create(name)
		if err != nil {
			return "", err
		}
		if _, err = entry.Write([]byte(content)); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

func (d *driver) expectStatus(err error, status int, label string) error {
	var api *apiError
	if !errors.As(err, &api) || api.status != status {
		return fmt.Errorf("skills: %s expected HTTP %d, got %v", label, status, err)
	}
	return nil
}

type skillFixture struct {
	recoveryFixture
	token       string
	packageName string
	archive     string
	candidate   skillCandidateView
	version     skillVersionView
	run         publicRun
}

func (f *skillFixture) arm(versionID entityID, evaluation, answer, bodyMarker, resource, resourceMarker string) error {
	return f.d.executorProvider("/instruction-skills/arm", map[string]any{
		"token": f.token, "version_id": versionID.String(), "evaluation": evaluation,
		"answer": answer, "body_marker": bodyMarker, "resource": resource, "resource_marker": resourceMarker,
	}, nil)
}

func (f *skillFixture) observe() (skillObservation, error) {
	var result skillObservation
	err := f.d.executorProvider("/instruction-skills/observe", map[string]string{"token": f.token}, &result)
	return result, err
}

func (f *skillFixture) importPackage(instructions, resourcePath, resource, task, expected, key string) (skillCandidateView, error) {
	archive, err := skillZip(instructions, resourcePath, resource)
	if err != nil {
		return skillCandidateView{}, err
	}
	var candidate skillCandidateView
	err = f.d.request(http.MethodPost, "/bots/"+f.bot.ID.String()+"/skills", f.owner.token, map[string]any{
		"archive": archive, "submission_key": key,
		"representative_task": task, "expected_output": expected,
	}, &candidate)
	return candidate, err
}

func (f *skillFixture) action(candidate entityID, operation string) ([]byte, error) {
	var raw json.RawMessage
	err := f.d.request(http.MethodPost, "/bots/"+f.bot.ID.String()+"/skills/"+candidate.String()+"/"+operation,
		f.owner.token, map[string]any{"submission_key": uuid.NewString()}, &raw)
	return raw, err
}

func (f *skillFixture) list() ([]skillPackageView, error) {
	var packages []skillPackageView
	err := f.d.request(http.MethodGet, "/bots/"+f.bot.ID.String()+"/skills", f.owner.token, nil, &packages)
	return packages, err
}

func (f *skillFixture) catalog() ([]availableSkillView, error) {
	var skills []availableSkillView
	err := f.d.request(http.MethodGet, "/bots/"+f.bot.ID.String()+"/skill-catalog", f.requester.token, nil, &skills)
	return skills, err
}

func (f *skillFixture) startRun(message string, skillIDs []entityID) (publicRun, error) {
	body := map[string]any{"conv_id": f.conv, "submission_key": uuid.NewString(), "message": message}
	if len(skillIDs) > 0 {
		body["skill_version_ids"] = skillIDs
	}
	var run publicRun
	err := f.d.request(http.MethodPost, "/bots/"+f.bot.ID.String()+"/runs", f.requester.token, body, &run)
	return run, err
}

func skillPromptEvidence(requests []map[string]any, versionID entityID, name, bodyMarker, resourceMarker string) error {
	var evaluated, toolsDeclared, catalogSeen, bodySeen, resourceSeen bool
	for _, request := range requests {
		messages, _ := request["messages"].([]any)
		tools, _ := request["tools"].([]any)
		if len(tools) == 0 {
			evaluated = true
		}
		names := make([]string, 0)
		for _, value := range tools {
			tool, _ := value.(map[string]any)
			function, _ := tool["function"].(map[string]any)
			if toolName, ok := function["name"].(string); ok {
				names = append(names, toolName)
			}
		}
		if slices.Contains(names, "load_skill") && slices.Contains(names, "read_skill_resource") {
			toolsDeclared = true
		}
		for _, value := range messages {
			message, _ := value.(map[string]any)
			role, _ := message["role"].(string)
			content, _ := message["content"].(string)
			if role == "system" && strings.Contains(content, name) && strings.Contains(content, versionID.String()) {
				catalogSeen = true
			}
			if role == "tool" && strings.Contains(content, bodyMarker) {
				bodySeen = true
			}
			if role == "tool" && strings.Contains(content, resourceMarker) {
				resourceSeen = true
			}
		}
	}
	if !evaluated {
		return errors.New("skills: representative task did not reach the model without tools")
	}
	if !toolsDeclared {
		return errors.New("skills: actual model request did not declare load_skill and read_skill_resource")
	}
	if !catalogSeen {
		return errors.New("skills: actual model request did not contain the frozen catalog name and version")
	}
	if !bodySeen {
		return errors.New("skills: loaded instruction body never reached the model request")
	}
	if !resourceSeen {
		return errors.New("skills: referenced resource never reached the model request")
	}
	return nil
}

func (d *driver) botSkills(address, secondAddress string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := skillFixture{recoveryFixture: recoveryFixture{authorizationFixture: authorizationFixture{d: d, suffix: suffix}}}
	for _, item := range []struct {
		name   string
		target *account
	}{{"owner", &f.owner}, {"requester", &f.requester}, {"admin", &f.admin}} {
		*item.target, err = d.register("skills_"+item.name, suffix)
		if err != nil {
			return err
		}
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{
		"type": "group", "group_name": "skills-" + suffix, "member_ids": []entityID{f.owner.id, f.requester.id},
	}, &conv); err != nil {
		return err
	}
	f.conv = conv.ID
	if f.model, err = d.fixtureModel(f.owner, "chat", "fixture-skill-"+suffix); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{
		"name": "skills-" + suffix, "type": "self_deployed", "model_id": f.model,
		"streaming_enabled": false, "response_triggers": []string{"mention"}, "max_step": 4,
	}, &f.bot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
		return err
	}

	f.token = "SKILL_" + suffix
	f.packageName = "csv-summary"
	bodyMarker := "BODY_MARKER_" + f.token
	resourceMarker := "RESOURCE_MARKER_" + f.token
	evaluation := "EVAL_RESULT_" + f.token
	instructions := "---\nname: " + f.packageName + "\ndescription: Summarize fixed CSV input precisely.\nlicense: MIT\n---\n\n# Workflow\nRead references/format.md before answering. " + bodyMarker + "\nThe accepted answer must echo the token " + f.token + "."
	resource := "# Format\nAlways report the total row count. " + resourceMarker
	if err = f.arm(entityID(""), evaluation, "", bodyMarker, "references/format.md", resourceMarker); err != nil {
		return err
	}
	task := "Summarize the fixed input. " + f.token
	// The runtime replica owns the LLM connection but may still be completing
	// startup after control becomes readable. The retry keeps one submission
	// identity, so recovery cannot create a duplicate candidate.
	importKey := uuid.NewString()
	for attempt := 0; ; attempt++ {
		f.candidate, err = f.importPackage(instructions, "references/format.md", resource, task, evaluation, importKey)
		var api *apiError
		if err == nil || !errors.As(err, &api) || api.status != http.StatusServiceUnavailable || attempt >= 25 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	if f.candidate.Status != "pending_validation" || f.candidate.Digest == "" || f.candidate.License != "MIT" {
		return fmt.Errorf("skills: import did not create a bounded pending candidate: %+v", f.candidate)
	}
	// Unverified and unapproved content must not publish.
	_, publishErr := f.action(f.candidate.ID, "publish")
	if err = f.d.expectStatus(publishErr, http.StatusConflict, "publish before verification"); err != nil {
		return err
	}
	var approval skillCandidateView
	raw, err := f.action(f.candidate.ID, "evaluate")
	if err != nil {
		return err
	}
	var result struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
		Reason string `json:"reason"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || result.Status != "passed" || !result.Passed {
		return fmt.Errorf("skills: representative evaluation did not pass: %s %v", string(raw), err)
	}
	raw, err = f.action(f.candidate.ID, "approve")
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &approval); err != nil || approval.Status != "approved" || approval.ApprovedDigest != f.candidate.Digest {
		return fmt.Errorf("skills: approval did not bind the exact digest: %s", string(raw))
	}
	raw, err = f.action(f.candidate.ID, "publish")
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &f.version); err != nil || f.version.Version != 1 || !f.version.Enabled || f.version.Status != "published" {
		return fmt.Errorf("skills: publish did not create an enabled immutable version: %s", string(raw))
	}
	// A retry under a new submission key must not create a second version.
	raw, err = f.action(f.candidate.ID, "publish")
	var repeated skillVersionView
	if err != nil || json.Unmarshal(raw, &repeated) != nil || repeated.VersionID != f.version.VersionID {
		return errors.New("skills: repeated publish did not replay the same immutable version")
	}
	packages, err := f.list()
	if err != nil {
		return err
	}
	if len(packages) != 1 || packages[0].EnabledVersion == nil || packages[0].EnabledVersion.VersionID != f.version.VersionID {
		return errors.New("skills: published version is not the enabled catalog entry")
	}
	catalog, err := f.catalog()
	if err != nil {
		return err
	}
	if len(catalog) != 1 || catalog[0].VersionID != f.version.VersionID || catalog[0].Name != f.packageName {
		return errors.New("skills: requester catalog does not expose the enabled version")
	}
	listErr := d.request(http.MethodGet, "/bots/"+f.bot.ID.String()+"/skills", f.requester.token, nil, nil)
	if err = f.d.expectStatus(listErr, http.StatusForbidden, "management list without Bot ownership"); err != nil {
		return err
	}

	if err = f.arm(f.version.VersionID, evaluation, "FINAL_"+f.token, bodyMarker, "references/format.md", resourceMarker); err != nil {
		return err
	}
	f.run, err = f.startRun("Use the authorized skill. "+f.token, []entityID{f.version.VersionID})
	if err != nil {
		return err
	}
	finished, err := d.waitRun(f.requester, f.run.ID)
	if err != nil {
		return err
	}
	f.run = finished
	if finished.Status != "succeeded" || finished.OutputStatus != "committed" || finished.ResultText != "FINAL_"+f.token {
		return fmt.Errorf("skills: run did not deliver the explicit expected result: status=%s output=%s result=%q error=%s", finished.Status, finished.OutputStatus, finished.ResultText, finished.Error)
	}
	if len(finished.FrozenSkills) != 1 || finished.FrozenSkills[0].VersionID != f.version.VersionID || !finished.FrozenSkills[0].Loaded || !finished.FrozenSkills[0].Explicit {
		return fmt.Errorf("skills: public Run did not freeze and load the selected version: %+v", finished.FrozenSkills)
	}
	if err = f.uniqueMessage(finished); err != nil {
		return err
	}
	observation, err := f.observe()
	if err != nil {
		return err
	}
	if err = skillPromptEvidence(observation.Requests, f.version.VersionID, f.packageName, bodyMarker, resourceMarker); err != nil {
		return err
	}

	// Another Bot cannot select this version, and management remains owner-only.
	second, err := d.fixtureModel(f.owner, "chat", "fixture-skill-second-"+suffix)
	if err != nil {
		return err
	}
	var other botView
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{
		"name": "skills-second-" + suffix, "type": "self_deployed", "model_id": second,
		"streaming_enabled": false, "response_triggers": []string{"mention"},
	}, &other); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": other.ID}, nil); err != nil {
		return err
	}
	var cross publicRun
	err = d.request(http.MethodPost, "/bots/"+other.ID.String()+"/runs", f.requester.token, map[string]any{
		"conv_id": f.conv, "submission_key": uuid.NewString(), "message": "cross scope " + f.token,
		"skill_version_ids": []entityID{f.version.VersionID},
	}, &cross)
	if err = f.d.expectStatus(err, http.StatusForbidden, "cross-Bot skill selection"); err != nil {
		return err
	}

	// Dangerous archives and package extensions are rejected with a diagnostic.
	escape, err := rawZip(map[string]string{"SKILL.md": instructions, "../escape.txt": "x"})
	if err != nil {
		return err
	}
	err = d.request(http.MethodPost, "/bots/"+f.bot.ID.String()+"/skills", f.owner.token, map[string]any{
		"archive": escape, "submission_key": uuid.NewString(), "representative_task": task, "expected_output": evaluation,
	}, nil)
	if err = f.d.expectStatus(err, http.StatusBadRequest, "path escape archive"); err != nil {
		return err
	}
	scripted, err := rawZip(map[string]string{"SKILL.md": strings.Replace(instructions, "license: MIT", "license: MIT\nallowed-tools: bash", 1), "scripts/run.sh": "rm -rf /"})
	if err != nil {
		return err
	}
	err = d.request(http.MethodPost, "/bots/"+f.bot.ID.String()+"/skills", f.owner.token, map[string]any{
		"archive": scripted, "submission_key": uuid.NewString(), "representative_task": task, "expected_output": evaluation,
	}, nil)
	if err = f.d.expectStatus(err, http.StatusBadRequest, "unsupported script extension"); err != nil {
		return err
	}

	// Duplicate submission identity with changed content conflicts explicitly.
	key := uuid.NewString()
	if _, err = f.importPackage(instructions, "", "", task, evaluation, key); err != nil {
		return err
	}
	changed := strings.Replace(instructions, "The accepted answer", "The changed answer", 1)
	_, err = f.importPackage(changed, "", "", task, evaluation, key)
	if err = f.d.expectStatus(err, http.StatusConflict, "submission key reuse"); err != nil {
		return err
	}

	// A newer compatible version is adopted by later Runs; the finished Run
	// keeps its frozen identity.
	time.Sleep(10 * time.Millisecond)
	versionTwo := strings.Replace(instructions, bodyMarker, bodyMarker+"_V2", 1)
	f.arm(f.version.VersionID, evaluation, "FINAL_"+f.token, bodyMarker, "references/format.md", resourceMarker)
	secondCandidate, err := f.importPackage(versionTwo, "references/format.md", resource, task, evaluation, uuid.NewString())
	if err != nil {
		return err
	}
	if _, err = f.action(secondCandidate.ID, "evaluate"); err != nil {
		return err
	}
	if _, err = f.action(secondCandidate.ID, "approve"); err != nil {
		return err
	}
	raw, err = f.action(secondCandidate.ID, "publish")
	var versionTwoView skillVersionView
	if err != nil || json.Unmarshal(raw, &versionTwoView) != nil || versionTwoView.Version != 2 {
		return fmt.Errorf("skills: compatible second version was not published: %s %v", string(raw), err)
	}
	frozen, err := d.getRun(f.requester, finished.ID)
	if err != nil {
		return err
	}
	if len(frozen.FrozenSkills) != 1 || frozen.FrozenSkills[0].VersionID != f.version.VersionID || frozen.FrozenSkills[0].Version != 1 {
		return errors.New("skills: published upgrade rewrote the frozen Run version")
	}
	if err = f.arm(versionTwoView.VersionID, evaluation, "FINAL_"+f.token, bodyMarker+"_V2", "references/format.md", resourceMarker); err != nil {
		return err
	}
	later, err := f.startRun("Use the authorized skill without explicit selection. "+f.token, nil)
	if err != nil {
		return err
	}
	laterDone, err := d.waitRun(f.requester, later.ID)
	if err != nil {
		return err
	}
	if len(laterDone.FrozenSkills) != 1 || laterDone.FrozenSkills[0].Version != 2 || laterDone.FrozenSkills[0].Explicit {
		return fmt.Errorf("skills: later Run did not adopt the compatible published version: %+v", laterDone.FrozenSkills)
	}
	fmt.Printf("Instruction skills: import->evaluate->approve->publish->enable->Run explicit result verified; frozen v1 retained after v2 adoption, cross-Bot selection denied\n")
	return nil
}

func (d *driver) getRun(caller account, id entityID) (publicRun, error) {
	var run publicRun
	err := d.request(http.MethodGet, "/runs/"+id.String(), caller.token, nil, &run)
	return run, err
}
