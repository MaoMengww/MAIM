package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// fileExecutionView is the public Run projection of a fixed-script execution.
// It carries no host path, stderr, stdout or container address.
type fileExecutionView struct {
	Status          string   `json:"status"`
	Outcome         string   `json:"outcome"`
	FailureClass    string   `json:"failure_class"`
	SkillVersionID  entityID `json:"skill_version_id"`
	InputFileID     entityID `json:"input_file_id"`
	ExecutionID     entityID `json:"execution_id"`
	ContainerID     string   `json:"container_id"`
	InputSHA256     string   `json:"input_sha256"`
	ImageID         string   `json:"image_id"`
	WorkerSHA256    string   `json:"worker_sha256"`
	EntryPoint      string   `json:"entry_point"`
	OutputSHA256    string   `json:"output_sha256"`
	OutputBytes     int64    `json:"output_bytes"`
	ResultFileID    entityID `json:"result_file_id"`
	StopConfirmed   bool     `json:"stop_confirmed"`
	Removed         bool     `json:"removed"`
	ResourcesAbsent bool     `json:"resources_absent"`
	UpdatedAt       int64    `json:"updated_at"`
}

type executionPolicyView struct {
	ImageID      string   `json:"image_id"`
	WorkerSHA256 string   `json:"worker_sha256"`
	Entrypoint   []string `json:"entrypoint"`
}

type executionFixture struct {
	runReviewFixture
	member, stranger account
	file             uploadedAttachment
	grant            resourceAuthorization
}

func (f *executionFixture) arm(label, mode string, target, file entityID, repeat bool) (string, error) {
	token := "RUN_REVIEW_" + label + "_" + f.suffix
	var armed struct {
		OK   bool   `json:"ok"`
		Mode string `json:"mode"`
	}
	err := f.d.executorProvider("/run-reviews/arm", map[string]any{
		"token": token, "mode": mode, "target_version_id": target.String(), "file_id": file.String(),
		"repeat_tool": repeat, "callback_url": f.d.provider + "/run-reviews/executed",
	}, &armed)
	if err == nil && (!armed.OK || armed.Mode != mode) {
		err = fmt.Errorf("file-processing: provider did not arm mode %q (got %q)", mode, armed.Mode)
	}
	return token, err
}

func (f *executionFixture) startWithFile(token, visibility string, file entityID, grant resourceAuthorization, versions []entityID) (publicRun, error) {
	body := map[string]any{
		"conv_id": f.conv, "visibility": visibility, "submission_key": uuid.NewString(), "message": token,
		"file_ids": []entityID{file}, "resource_grant_ids": []entityID{grant.ID},
	}
	if len(versions) > 0 {
		body["skill_version_ids"] = versions
	}
	var run publicRun
	err := f.d.request(http.MethodPost, f.path("runs"), f.requester.token, body, &run)
	if err == nil {
		f.sources[run.ID] = visibility
	}
	return run, err
}

func (d *driver) botFileProcessing() error {
	if d.workerPath == "" || d.executionPolicy == "" {
		return errors.New("bot-file-processing requires -worker-path and -execution-policy mounted from the trusted execution node")
	}
	worker, err := os.ReadFile(d.workerPath)
	if err != nil || len(worker) == 0 || len(worker) > 64<<10 {
		return fmt.Errorf("bot-file-processing: reviewed worker source unavailable: %w", err)
	}
	var policy executionPolicyView
	rawPolicy, err := os.ReadFile(d.executionPolicy)
	if err != nil || json.Unmarshal(rawPolicy, &policy) != nil || policy.ImageID == "" || policy.WorkerSHA256 == "" || len(policy.Entrypoint) == 0 {
		return errors.New("bot-file-processing: trusted execution policy unavailable")
	}
	workerSum := sha256.Sum256(worker)
	if hex.EncodeToString(workerSum[:]) != policy.WorkerSHA256 {
		return errors.New("bot-file-processing: mounted worker differs from the human-reviewed policy")
	}
	entry := strings.Join(policy.Entrypoint, " ")
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := &executionFixture{runReviewFixture: runReviewFixture{recoveryFixture: recoveryFixture{authorizationFixture: authorizationFixture{d: d, suffix: suffix}}}}
	f.sources = make(map[entityID]string)
	for _, item := range []struct {
		name   string
		target *account
	}{{"owner", &f.owner}, {"requester", &f.requester}, {"admin", &f.admin}, {"member", &f.member}, {"stranger", &f.stranger}} {
		*item.target, err = d.register("file_processing_"+item.name, suffix)
		if err != nil {
			return err
		}
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{
		"type": "group", "group_name": "file-processing-" + suffix, "member_ids": []entityID{f.owner.id, f.requester.id, f.member.id},
	}, &conv); err != nil {
		return err
	}
	f.conv = conv.ID
	if f.model, err = d.fixtureModel(f.owner, "chat", "fixture-run-review-"+suffix); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{
		"name": "file-processing-" + suffix, "type": "self_deployed", "model_id": f.model,
		"streaming_enabled": false, "response_triggers": []string{"mention"}, "max_step": 6,
	}, &f.bot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
		return err
	}
	if err = f.defaultPolicies(); err != nil {
		return err
	}
	// Source-scope maintenance is an explicit conversation role, not Bot ownership.
	// It is granted only after the default-policy authorization checks.
	if err = d.request(http.MethodPut, "/convs/"+f.conv.String()+"/members/"+f.owner.id.String()+"/role", f.admin.token, map[string]any{"role": 2}, nil); err != nil {
		return err
	}

	input := []byte("value\n10\n20\n30\n")
	expected := []byte("rows,total,mean,min,max\n3,60,20,10,30\n")
	if f.file, err = d.attachmentUpload(f.requester, "authorized-"+suffix+".csv", "text/csv", input, 2); err != nil {
		return err
	}
	if f.grant, err = d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", f.file.info.FileID, "conversation", true); err != nil {
		return err
	}
	// Import is not execution authority. The owner's authorized independent CSV
	// samples enter the same governance gates as Run-derived candidates.
	manifest, err := json.Marshal(map[string]string{"image_digest": policy.ImageID, "entry_point": entry})
	if err != nil {
		return err
	}
	archive, err := rawZip(map[string]string{
		"SKILL.md":          "---\nname: fixed-csv-" + suffix + "\ndescription: Process one accepted CSV with the reviewed fixed capability.\nlicense: MIT\n---\nUse run_skill_script with this exact version and the accepted CSV file_id. Never supply code, commands or paths.",
		"scripts/worker.py": string(worker), "execution.json": string(manifest),
	})
	if err != nil {
		return err
	}
	importBody := map[string]any{"archive": archive, "submission_key": uuid.NewString(),
		"representative_task": "Process the authorized CSV sample exactly.", "expected_output": "rows,total,mean,min,max",
		"input_b64": base64.StdEncoding.EncodeToString(input), "expected_output_b64": base64.StdEncoding.EncodeToString(expected),
		"evidence_authorized": true, "source_license_expires_at": time.Now().Add(time.Hour).Unix(),
		"target_goal": "Process one accepted CSV file", "applicability": "csv-v1 fixed capability only"}
	if err := d.expectStatus(d.request(http.MethodPost, f.path("skill-candidates/import"), f.requester.token, importBody, nil), http.StatusForbidden, "non-owner script import"); err != nil {
		return err
	}
	var revised governedCandidate
	if err := d.request(http.MethodPost, f.path("skill-candidates/import"), f.owner.token, importBody, &revised); err != nil {
		return err
	}
	if revised.Status != "pending_validation" || !revised.CodeReviewRequired || revised.SourceRunID != "" || revised.ScopeKey != "bot" || len(revised.Scripts) != 1 {
		return errors.New("file-processing: archive did not produce an unreviewed, source-free pending candidate")
	}
	var repeatedImport governedCandidate
	if err := d.request(http.MethodPost, f.path("skill-candidates/import"), f.owner.token, importBody, &repeatedImport); err != nil || repeatedImport.ID != revised.ID || repeatedImport.Digest != revised.Digest {
		return fmt.Errorf("file-processing: archive import intent did not replay the same candidate: %v", err)
	}
	changedArchive, err := rawZip(map[string]string{
		"SKILL.md":          "---\nname: fixed-csv-" + suffix + "\ndescription: Changed unreviewed archive.\nlicense: MIT\n---\nA changed candidate still requires review.",
		"scripts/worker.py": string(worker) + "\n# changed archive identity\n", "execution.json": string(manifest),
	})
	if err != nil {
		return err
	}
	importBody["archive"] = changedArchive
	if err := d.expectStatus(d.request(http.MethodPost, f.path("skill-candidates/import"), f.owner.token, importBody, nil), http.StatusConflict, "same import key with changed archive"); err != nil {
		return err
	}
	importBody["archive"] = archive
	if _, err := f.governed(f.requester, revised.ID); d.expectStatus(err, http.StatusForbidden, "non-owner imported candidate access") != nil {
		return fmt.Errorf("file-processing: imported script material visible without owner permission: %v", err)
	}
	for _, operation := range []string{"evaluate", "approve", "publish"} {
		if err := d.expectStatus(f.govern(f.owner, revised, operation, uuid.NewString(), nil, nil), http.StatusConflict, "unreviewed archive "+operation); err != nil {
			return err
		}
	}
	var unpublished []availableSkillView
	if err := d.request(http.MethodGet, f.path("skill-catalog"), f.requester.token, nil, &unpublished); err != nil || len(unpublished) != 0 {
		return fmt.Errorf("file-processing: imported unapproved package leaked into executable catalog: %v", err)
	}
	if err = f.govern(f.owner, revised, "code-review", uuid.NewString(), map[string]any{"confirmed": true}, &revised); err != nil {
		return fmt.Errorf("file-processing: trusted manager did not admit exact worker identity: %w", err)
	}
	var evaluation struct {
		Status string `json:"status"`
		Passed bool   `json:"passed"`
	}
	if err = f.govern(f.owner, revised, "evaluate", uuid.NewString(), nil, &evaluation); err != nil {
		return err
	}
	if evaluation.Status != "passed" || !evaluation.Passed {
		return fmt.Errorf("file-processing: exact Docker validation did not pass: %+v", evaluation)
	}
	if err = f.govern(f.owner, revised, "approve", uuid.NewString(), nil, &revised); err != nil {
		return err
	}
	if revised.Status != "approved" {
		return fmt.Errorf("file-processing: candidate not approved: %s", revised.Status)
	}
	var version skillVersionView
	if err = f.govern(f.owner, revised, "publish", uuid.NewString(), nil, &version); err != nil {
		return err
	}
	if version.VersionID == "" || version.Status != "published" || !version.Enabled {
		return fmt.Errorf("file-processing: fixed script version not published: %+v", version)
	}

	runToken, err := f.arm("file-run", "file-processing", version.VersionID, f.file.info.FileID, false)
	if err != nil {
		return err
	}
	run, err := f.startWithFile(runToken, "conversation", f.file.info.FileID, f.grant, []entityID{version.VersionID})
	if err != nil {
		return err
	}
	run, err = d.waitRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if err = d.assertFileExecution(run, version.VersionID, f.file.info.FileID, policy, entry, input, expected); err != nil {
		return err
	}
	if err = f.assertFormalDelivery(run, expected); err != nil {
		return err
	}

	// A second identical tool call inside one Run must not create a second
	// container, result file or final message.
	repeatToken, err := f.arm("file-repeat", "file-processing", version.VersionID, f.file.info.FileID, true)
	if err != nil {
		return err
	}
	repeat, err := f.startWithFile(repeatToken, "conversation", f.file.info.FileID, f.grant, []entityID{version.VersionID})
	if err != nil {
		return err
	}
	repeat, err = d.waitRun(f.requester, repeat.ID)
	if err != nil {
		return err
	}
	if err = d.assertFileExecution(repeat, version.VersionID, f.file.info.FileID, policy, entry, input, expected); err != nil {
		return err
	}
	if repeat.MessageID == nil || run.MessageID == nil || *repeat.MessageID == *run.MessageID {
		return errors.New("file-processing: repeated Run did not publish one independent final message")
	}
	if repeat.Execution == nil || run.Execution == nil || repeat.Execution.ResultFileID == "" || repeat.Execution.ResultFileID == run.Execution.ResultFileID {
		return errors.New("file-processing: each Run must register its own formal result file")
	}
	if err = f.assertSingleFileMessage(repeat, repeat.Execution.ResultFileID); err != nil {
		return err
	}

	// Accepted input after script completion must supersede the old formal file,
	// even when the model's earlier closing response is still in flight.
	raceToken, err := f.arm("file-input-race", "file-processing", version.VersionID, f.file.info.FileID, false)
	if err != nil {
		return err
	}
	if err = d.executorProvider("/run-reviews/arm", map[string]any{
		"token": raceToken, "mode": "file-processing", "target_version_id": version.VersionID,
		"file_id": f.file.info.FileID, "hold_final": true,
	}, nil); err != nil {
		return err
	}
	race, err := f.startWithFile(raceToken, "conversation", f.file.info.FileID, f.grant, []entityID{version.VersionID})
	if err != nil {
		return err
	}
	held := false
	for deadline := time.Now().Add(d.timeout); time.Now().Before(deadline); {
		var observation struct {
			Held string `json:"held"`
		}
		if err = d.executorProvider("/run-reviews/observe", map[string]any{"token": raceToken}, &observation); err != nil {
			return err
		}
		if observation.Held == "file-final" {
			held = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !held {
		return errors.New("file-processing: model did not reach finalization gate")
	}
	if err = d.request(http.MethodPost, "/runs/"+race.ID.String()+"/control", f.requester.token, map[string]any{
		"operation": "input", "submission_key": uuid.NewString(), "message": "Do not deliver the earlier CSV. Answer with a text explanation instead.",
	}, nil); err != nil {
		return err
	}
	if err = d.executorProvider("/run-reviews/release", map[string]any{"token": raceToken}, nil); err != nil {
		return err
	}
	race, err = d.waitRun(f.requester, race.ID)
	if err != nil {
		return err
	}
	if race.Status != "succeeded" || race.ResultType == "file" || race.ResultText != "FILE_PROCESSING_RESULT:"+raceToken {
		return fmt.Errorf("file-processing: accepted input did not supersede the prior file result: %+v", race)
	}

	// A CSV beyond the fixed 64 KiB input budget is accepted as Run material but
	// must fail closed before any execution identity or result is created.
	oversized := []byte("value\n" + strings.Repeat("1\n", 33000))
	large, err := d.attachmentUpload(f.requester, "oversized-"+suffix+".csv", "text/csv", oversized, 2)
	if err != nil {
		return err
	}
	largeGrant, err := d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", large.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	largeToken, err := f.arm("file-large", "file-processing", version.VersionID, large.info.FileID, false)
	if err != nil {
		return err
	}
	largeRun, err := f.startWithFile(largeToken, "conversation", large.info.FileID, largeGrant, []entityID{version.VersionID})
	if err != nil {
		return err
	}
	largeRun, err = d.waitRun(f.requester, largeRun.ID)
	if err != nil || largeRun.Status != "failed" || largeRun.MessageID != nil {
		return fmt.Errorf("file-processing: oversized input was not rejected as failed work: %+v error=%v", largeRun, err)
	}

	// A non-member cannot read the formal result even with its file identity.
	var download struct {
		URL string `json:"download_url"`
	}
	if err = d.request(http.MethodGet, "/files/"+run.Execution.ResultFileID.String()+"/download", f.stranger.token, nil, &download); err == nil {
		return errors.New("file-processing: non-member obtained the formal result")
	} else if api, ok := err.(*apiError); !ok || api.status != http.StatusForbidden {
		return fmt.Errorf("file-processing: non-member denial was not explicit 403: %v", err)
	}
	// The source grant is the only authority for the derived output. Revoking it
	// must deny new downloads; the already committed message remains a fact.
	if err = d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{
		"op": "revoke", "grant_id": f.grant.ID,
	}, nil); err != nil {
		return err
	}
	var denied struct {
		URL string `json:"download_url"`
	}
	if err = d.request(http.MethodGet, "/files/"+run.Execution.ResultFileID.String()+"/download", f.requester.token, nil, &denied); err == nil {
		return errors.New("file-processing: revoked source still allowed result download")
	} else if api, ok := err.(*apiError); !ok || api.status != http.StatusForbidden && api.status != http.StatusNotFound {
		return fmt.Errorf("file-processing: revoked result was not explicitly denied: %v", err)
	}
	// Revocation closes the source-scoped Run projection as well as the derived
	// file. An explicit denial is the required outcome, not a partial read.
	var revoked publicRun
	if err = d.request(http.MethodGet, "/runs/"+run.ID.String(), f.requester.token, nil, &revoked); err == nil {
		return errors.New("file-processing: revoked source still exposed the Run")
	} else if api, ok := err.(*apiError); !ok || api.status != http.StatusForbidden {
		return fmt.Errorf("file-processing: revoked Run was not explicitly denied: %v", err)
	}
	// Keep a separately authorized Run for actual browser verification. Private
	// fixture credentials stay in the runner's mode-0600 local control file.
	if d.controlDir != "" {
		browserGrant, err := d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", f.file.info.FileID, "conversation", true)
		if err != nil {
			return err
		}
		browserToken, err := f.arm("file-browser", "file-processing", version.VersionID, f.file.info.FileID, false)
		if err != nil {
			return err
		}
		browserRun, err := f.startWithFile(browserToken, "conversation", f.file.info.FileID, browserGrant, []entityID{version.VersionID})
		if err != nil {
			return err
		}
		browserRun, err = d.waitRun(f.requester, browserRun.ID)
		if err != nil {
			return err
		}
		if err = f.assertFormalDelivery(browserRun, expected); err != nil {
			return err
		}
		f.browser = map[string]any{
			"scenario": "bot-file-processing", "conv_id": f.conv, "bot_id": f.bot.ID, "run_id": browserRun.ID,
			"requester": map[string]any{"username": f.requester.username, "password": f.requester.password},
		}
		if err = f.save(); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) assertFileExecution(run publicRun, version, file entityID, policy executionPolicyView, entry string, input, expected []byte) error {
	if run.Status != "succeeded" || run.OutputStatus != "committed" || run.MessageID == nil || run.ResultText != "result.csv" || run.ResultType != "file" {
		return fmt.Errorf("file-processing: Run did not commit one file result: %+v", run)
	}
	if len(run.FrozenSkills) != 1 || run.FrozenSkills[0].VersionID != version {
		return fmt.Errorf("file-processing: published fixed-script version was not frozen: %+v", run.FrozenSkills)
	}
	execution := run.Execution
	if execution == nil {
		return errors.New("file-processing: public Run did not expose execution facts")
	}
	sum := sha256.Sum256(input)
	output := sha256.Sum256(expected)
	if execution.Status != "succeeded" || execution.Outcome != "succeeded" || execution.SkillVersionID != version || execution.InputFileID != file ||
		execution.ImageID != policy.ImageID || execution.WorkerSHA256 != policy.WorkerSHA256 || execution.EntryPoint != entry ||
		execution.InputSHA256 != hex.EncodeToString(sum[:]) || execution.OutputSHA256 != hex.EncodeToString(output[:]) ||
		execution.OutputBytes != int64(len(expected)) || execution.ResultFileID == "" || execution.ContainerID == "" ||
		execution.ExecutionID == "" || !execution.Removed || !execution.ResourcesAbsent || !execution.StopConfirmed {
		return fmt.Errorf("file-processing: execution identity or cleanup proof incomplete: %+v", execution)
	}
	return nil
}

func (f *executionFixture) assertFormalDelivery(run publicRun, expected []byte) error {
	if err := f.assertSingleFileMessage(run, run.Execution.ResultFileID); err != nil {
		return err
	}
	var download struct {
		URL  string         `json:"download_url"`
		File attachmentInfo `json:"file"`
	}
	if err := f.d.request(http.MethodGet, "/files/"+run.Execution.ResultFileID.String()+"/download", f.requester.token, nil, &download); err != nil {
		return err
	}
	code, raw, err := f.d.attachmentObject(http.MethodGet, download.URL, "", nil)
	if err != nil || code != http.StatusOK || !bytes.Equal(raw, expected) {
		return fmt.Errorf("file-processing: downloaded result bytes differ: code=%d err=%v", code, err)
	}
	return nil
}

func (f *executionFixture) assertSingleFileMessage(run publicRun, file entityID) error {
	if run.MessageID == nil {
		return errors.New("file-processing: missing final message identity")
	}
	message, err := f.d.identityMessage(f.requester, *run.MessageID)
	if err != nil {
		return err
	}
	if message.SenderType != "bot" || !hasEntityID(message.SenderID, run.BotID) || message.File == nil || message.File.FileID != file {
		return errors.New("file-processing: final message is not the registered formal file")
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err = f.d.request(http.MethodGet, "/convs/"+f.conv.String()+"/messages?limit=100", f.requester.token, nil, &history); err != nil {
		return err
	}
	count := 0
	for _, item := range history.Messages {
		if item.File != nil && item.File.FileID == file {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("file-processing: result file appears in %d messages, expected 1", count)
	}
	return nil
}
