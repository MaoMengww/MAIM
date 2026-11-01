package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"

	llmpb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const componentPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jfWQAAAAASUVORK5CYII="
const componentKey = "e2e-fixture-not-a-secret"

// Model registration, authorization, quota and billing remain real AIM services.
// Only the external provider's HTTP responses are controlled by the fixture.
func (d *driver) llmComponentsScenario() (result error) {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("components", suffix)
	if err != nil {
		return err
	}
	outsider, err := d.register("components_outside", suffix)
	if err != nil {
		return err
	}
	var models []entityID
	defer func() {
		for _, id := range models {
			result = errors.Join(result, d.request(http.MethodDelete, "/models/"+id.String(), owner.token, nil, nil))
		}
	}()
	create := func(capability string) (entityID, string, error) {
		name := "e2e-component-" + capability + "-" + suffix
		id, err := d.fixtureModel(owner, capability, name)
		if err == nil {
			models = append(models, id)
		}
		return id, name, err
	}
	vlmID, vlmName, err := create("vlm")
	if err != nil {
		return err
	}
	embedID, embedName, err := create("embed")
	if err != nil {
		return err
	}
	cohereName := "e2e-component-cohere-" + suffix
	cohereID, err := d.fixtureModel(owner, "embed", cohereName)
	if err != nil {
		return err
	}
	models = append(models, cohereID)
	// Explicit provider selection must retain the native protocol even at a
	// configured endpoint whose URL does not contain the provider name.
	if err := d.request(http.MethodPut, "/models/"+cohereID.String(), owner.token,
		map[string]string{"provider": "cohere", "base_url": d.provider + "/native"}, nil); err != nil {
		return err
	}
	rerankID, rerankName, err := create("rerank")
	if err != nil {
		return err
	}
	if err := d.request(http.MethodPut, "/models/"+rerankID.String(), owner.token,
		map[string]string{"provider": "cohere", "base_url": d.provider + "/native"}, nil); err != nil {
		return err
	}
	conn, err := grpc.NewClient(d.llmRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func(close func() error) {
		// Read-only transport cleanup does not affect the scenario verdict.
		_ = close()
	}(conn.Close)
	cli := llmpb.NewLLMGatewayClient(conn)
	ownerID, outsiderID := owner.id.String(), outsider.id.String()
	contextFor := func(user string) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", user)), cancel
	}
	visionRequest := func(token string) *llmpb.VlmChatReq {
		return &llmpb.VlmChatReq{ModelId: vlmID.String(), OwnerId: &ownerID, SystemPrompt: "component system",
			UserPrompt: token, ImageData: componentPNG, Temperature: .25, MaxTokens: 37}
	}
	vision := func(token string) (*llmpb.ChatResp, error) {
		ctx, cancel := contextFor(ownerID)
		defer cancel()
		return cli.VlmChat(ctx, visionRequest(token))
	}
	embedRequest := func(id entityID, token string) *llmpb.EmbedReq {
		return &llmpb.EmbedReq{ModelId: id.String(), OwnerId: &ownerID, Input: []string{token, "COMPONENT_SECOND"}}
	}
	embed := func(id entityID, token string) (*llmpb.EmbedResp, error) {
		ctx, cancel := contextFor(ownerID)
		defer cancel()
		return cli.Embed(ctx, embedRequest(id, token))
	}

	ctx, cancel := contextFor(outsiderID)
	foreignVision := visionRequest("COMPONENT_FORBIDDEN")
	foreignVision.OwnerId = &outsiderID
	_, err = cli.VlmChat(ctx, foreignVision)
	cancel()
	if status.Code(err) != codes.PermissionDenied {
		return errors.New("components.vlm: 私有模型未拒绝其他用户")
	}
	ctx, cancel = contextFor(outsiderID)
	foreignEmbed := embedRequest(embedID, "COMPONENT_FORBIDDEN")
	foreignEmbed.OwnerId = &outsiderID
	_, err = cli.Embed(ctx, foreignEmbed)
	cancel()
	if status.Code(err) != codes.PermissionDenied {
		return errors.New("components.embed: 私有模型未拒绝其他用户")
	}
	ctx, cancel = contextFor(ownerID)
	_, err = cli.Embed(ctx, embedRequest(vlmID, "COMPONENT_WRONG_CAPABILITY"))
	cancel()
	if status.Code(err) != codes.InvalidArgument {
		return errors.New("components.embed: capability不匹配未拒绝")
	}

	for _, token := range []string{"COMPONENT_VLM_POSITIVE", "COMPONENT_VLM_NO_USAGE", "COMPONENT_VLM_ZERO"} {
		resp, err := vision(token)
		if err != nil {
			return fmt.Errorf("components.vlm %s: %w", token, err)
		}
		if err := requireChatResponse(resp, vlmName, "component-vision:"+token, "stop"); err != nil {
			return err
		}
		if token == "COMPONENT_VLM_NO_USAGE" {
			if resp.Usage != nil {
				return errors.New("components.vlm: 缺失usage被伪造")
			}
		} else {
			input, output := int32(7), int32(5)
			if token == "COMPONENT_VLM_ZERO" {
				input, output = 0, 0
			}
			if err := requireReportedUsage(resp.Usage, input, output, input+output); err != nil {
				return err
			}
		}
		if err := d.requireComponentObservation(vlmName, token, "completed", "/v1/chat/completions"); err != nil {
			return err
		}
	}
	if err := d.requireComponentBilling(owner, vlmID, vlmName, "vlm", 1, 7, 5); err != nil {
		return err
	}
	for _, token := range []string{"COMPONENT_VLM_HTTP_ERROR", "COMPONENT_VLM_REDIRECT", "COMPONENT_VLM_OVERSIZED"} {
		_, err := vision(token)
		if err := requireProviderErrorRedaction(err, token, componentKey); err != nil {
			return err
		}
		state := "http_error"
		if token == "COMPONENT_VLM_REDIRECT" {
			state = "redirected"
		}
		if token == "COMPONENT_VLM_OVERSIZED" {
			state = "oversized"
		}
		if err := d.requireComponentObservation(vlmName, token, state, "/v1/chat/completions"); err != nil {
			return err
		}
	}

	for _, token := range []string{"COMPONENT_EMBED_POSITIVE", "COMPONENT_EMBED_NO_USAGE", "COMPONENT_EMBED_ZERO"} {
		resp, err := embed(embedID, token)
		if err != nil {
			return fmt.Errorf("components.embed %s: %w", token, err)
		}
		if err := requireComponentVectors(resp, embedName); err != nil {
			return err
		}
		if token == "COMPONENT_EMBED_NO_USAGE" {
			if resp.Usage != nil {
				return errors.New("components.embed: 缺失usage被伪造")
			}
		} else {
			count := int32(9)
			if token == "COMPONENT_EMBED_ZERO" {
				count = 0
			}
			if err := requireReportedUsage(resp.Usage, count, 0, count); err != nil {
				return err
			}
		}
		if err := d.requireComponentObservation(embedName, token, "completed", "/v1/embeddings"); err != nil {
			return err
		}
	}
	for _, token := range []string{"COMPONENT_EMPTY", "COMPONENT_INVALID_INDEX", "COMPONENT_NEGATIVE_INDEX", "COMPONENT_DUPLICATE", "COMPONENT_MISSING_INDEX", "COMPONENT_MISSING_VECTOR", "COMPONENT_WRONG_DIMENSION", "COMPONENT_HTTP_ERROR", "COMPONENT_REDIRECT", "COMPONENT_OVERSIZED"} {
		resp, err := embed(embedID, token)
		if err == nil || resp != nil {
			return fmt.Errorf("components.embed: 接受畸形/失败响应 %s", token)
		}
		if token == "COMPONENT_HTTP_ERROR" && status.Code(err) != codes.InvalidArgument {
			return errors.New("components.embed: provider HTTP400未映射InvalidArgument")
		}
		if err := requireProviderErrorRedaction(err, token, componentKey); err != nil {
			return err
		}
		state := "completed"
		if token == "COMPONENT_HTTP_ERROR" {
			state = "http_error"
		} else if token == "COMPONENT_REDIRECT" {
			state = "redirected"
		}
		if token == "COMPONENT_OVERSIZED" {
			state = "oversized"
		}
		if err := d.requireComponentObservation(embedName, token, state, "/v1/embeddings"); err != nil {
			return err
		}
	}
	ctx, cancel = contextFor(ownerID)
	_, err = cli.Embed(ctx, &llmpb.EmbedReq{ModelId: embedID.String(), OwnerId: &ownerID})
	cancel()
	if status.Code(err) != codes.InvalidArgument {
		return errors.New("components.embed: 空输入未映射InvalidArgument")
	}
	if err := d.requireComponentBilling(owner, embedID, embedName, "embed", 1, 9, 0); err != nil {
		return err
	}

	// More sequential cancellations than each real configured pool can hold:
	// a canceled-context DECR leak makes subsequent admission fail observably.
	for _, target := range []struct {
		name, route string
		count       int
		call        func(context.Context, string) error
	}{
		{vlmName, "/v1/chat/completions", 11, func(ctx context.Context, token string) error {
			_, err := cli.VlmChat(ctx, visionRequest(token))
			return err
		}},
		{embedName, "/v1/embeddings", 9, func(ctx context.Context, token string) error {
			_, err := cli.Embed(ctx, embedRequest(embedID, token))
			return err
		}},
	} {
		for i := range target.count {
			token := fmt.Sprintf("COMPONENT_CANCEL_%d", i)
			ctx, cancel := contextFor(ownerID)
			finished := make(chan error, 1)
			go func() { finished <- target.call(ctx, token) }()
			observed := d.requireComponentObservation(target.name, token, "received", target.route)
			cancel()
			callErr := <-finished
			if observed != nil {
				return observed
			}
			if status.Code(callErr) != codes.Canceled {
				return errors.New("components.cancel: RPC未返回Canceled")
			}
			if err := d.requireComponentObservation(target.name, token, "cancelled", target.route); err != nil {
				return err
			}
		}
	}
	resp, err := vision("COMPONENT_VLM_RECOVERED")
	if err != nil {
		return err
	}
	if err := requireChatResponse(resp, vlmName, "component-vision:COMPONENT_VLM_RECOVERED", "stop"); err != nil {
		return err
	}
	embedded, err := embed(embedID, "COMPONENT_EMBED_RECOVERED")
	if err != nil {
		return err
	}
	if err := requireComponentVectors(embedded, embedName); err != nil {
		return err
	}
	for _, token := range []string{"COMPONENT_COHERE_NATIVE", "COMPONENT_COHERE_NO_USAGE", "COMPONENT_COHERE_ZERO"} {
		cohere, err := embed(cohereID, token)
		if err != nil {
			return err
		}
		if err := requireComponentVectors(cohere, cohereName); err != nil {
			return err
		}
		if token == "COMPONENT_COHERE_NO_USAGE" {
			if cohere.Usage != nil {
				return errors.New("components.cohere: 缺失usage被伪造")
			}
		} else {
			count := int32(9)
			if token == "COMPONENT_COHERE_ZERO" {
				count = 0
			}
			if err := requireReportedUsage(cohere.Usage, count, 0, count); err != nil {
				return err
			}
		}
		if err := d.requireComponentObservation(cohereName, token, "completed", "/native/v2/embed"); err != nil {
			return err
		}
	}
	for _, token := range []string{"COMPONENT_COHERE_MISSING_VECTOR", "COMPONENT_COHERE_WRONG_DIMENSION", "COMPONENT_COHERE_OVERSIZED"} {
		response, callErr := embed(cohereID, token)
		if callErr == nil || response != nil {
			return fmt.Errorf("components.cohere: accepted malformed or unbounded native response %s", token)
		}
		if err := requireProviderErrorRedaction(callErr, token, componentKey); err != nil {
			return err
		}
	}
	query := "COMPONENT_RERANK_NATIVE_" + suffix
	documents := []string{"unrelated weather", "relevant " + query}
	ctx, cancel = contextFor(ownerID)
	ranking, err := cli.Rerank(ctx, &llmpb.RerankReq{ModelId: rerankID.String(), OwnerId: &ownerID,
		Query: query, Documents: documents, TopN: 1, ReturnDocuments: true})
	cancel()
	if err != nil {
		return fmt.Errorf("components.rerank native configured endpoint: %w", err)
	}
	if ranking.Model != rerankName || len(ranking.Results) != 1 || ranking.Results[0].Index != 1 ||
		ranking.Results[0].Document != documents[1] || math.Abs(float64(ranking.Results[0].RelevanceScore)-.95) > .00001 {
		return errors.New("components.rerank: remote semantic rank/document was not preserved")
	}
	if err := d.requireComponentObservation(rerankName, query, "completed", "/native/v2/rerank"); err != nil {
		return err
	}
	ctx, cancel = contextFor(ownerID)
	ranking, err = cli.Rerank(ctx, &llmpb.RerankReq{ModelId: rerankID.String(), OwnerId: &ownerID,
		Query: query, Documents: documents, TopN: 1, ReturnDocuments: false})
	cancel()
	if err != nil || ranking == nil || len(ranking.Results) != 1 || ranking.Results[0].Index != 1 || ranking.Results[0].Document != "" {
		return fmt.Errorf("components.rerank: optional document disclosure not respected: %v", err)
	}
	for _, token := range []string{"COMPONENT_RERANK_INVALID_INDEX", "COMPONENT_RERANK_HTTP_ERROR"} {
		ctx, cancel = contextFor(ownerID)
		response, callErr := cli.Rerank(ctx, &llmpb.RerankReq{ModelId: rerankID.String(), OwnerId: &ownerID,
			Query: token, Documents: []string{"unrelated", token}, TopN: 1, ReturnDocuments: true})
		cancel()
		if callErr == nil || response != nil {
			return fmt.Errorf("components.rerank: accepted invalid native response %s", token)
		}
		if err := requireProviderErrorRedaction(callErr, token, componentKey); err != nil {
			return err
		}
	}
	for _, expected := range []struct {
		id               entityID
		name, capability string
		count            int
		input, output    int32
	}{{vlmID, vlmName, "vlm", 2, 7, 5}, {embedID, embedName, "embed", 2, 9, 0}, {cohereID, cohereName, "embed", 1, 9, 0}} {
		if err := d.requireComponentBilling(owner, expected.id, expected.name, expected.capability, expected.count, expected.input, expected.output); err != nil {
			return err
		}
	}
	return d.judgeComponentsScenario(owner, outsider, suffix, cli, embedID)
}

func (d *driver) judgeComponentsScenario(owner, outsider account, suffix string, cli llmpb.LLMGatewayClient, wrongCapability entityID) (result error) {
	name := "e2e-component-judge-" + suffix
	type judgeModelView struct {
		runtimeModel
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
	}
	var model runtimeModel
	if err := d.request(http.MethodPost, "/models", owner.token, map[string]any{
		"model_name": name + "-initial", "provider": "typesafe", "capability": "judge",
		"base_url": d.provider + "/native", "api_key": componentKey,
		"input_price_per_mtok": 2, "output_price_per_mtok": 3,
	}, &model); err != nil {
		return fmt.Errorf("components.judge.register: %w", err)
	}
	if model.ID == "" {
		return errors.New("components.judge: 注册未返回模型身份")
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+model.ID.String(), owner.token, nil, nil))
	}()
	if err := d.request(http.MethodPut, "/models/"+model.ID.String(), owner.token,
		map[string]any{"model_name": name, "output_price_per_mtok": 0}, nil); err != nil {
		return err
	}
	var listed struct {
		Items []judgeModelView `json:"items"`
	}
	if err := d.request(http.MethodGet, "/models?capability=judge", owner.token, nil, &listed); err != nil {
		return err
	}
	if !slices.ContainsFunc(listed.Items, func(item judgeModelView) bool {
		return item.ID == model.ID && item.Name == name && item.Capability == "judge" && item.Provider == "typesafe" &&
			item.BaseURL == d.provider+"/native" && item.Status == "active" && item.OwnerType == "user" &&
			hasEntityID(item.OwnerID, owner.id) && item.InputPrice == 2 && item.OutputPrice == 0
	}) {
		return errors.New("components.judge: 创建/编辑/能力筛选/免费输出价未持久化回显")
	}
	if err := d.runtimeReject(http.MethodPost, "/models", owner, map[string]any{
		"model_name": name + "-unsupported", "provider": "openai", "capability": "judge",
		"base_url": d.provider + "/native", "api_key": componentKey,
	}, http.StatusBadRequest); err != nil {
		return err
	}
	for _, alias := range []string{"jev-latest", "jev-preview"} {
		if err := d.runtimeReject(http.MethodPost, "/models", owner, map[string]any{
			"model_name": alias, "provider": "typesafe", "capability": "judge",
			"base_url": d.provider + "/native", "api_key": componentKey,
		}, http.StatusBadRequest); err != nil {
			return err
		}
	}
	ownerID, outsiderID := owner.id.String(), outsider.id.String()
	contextFor := func(user string) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", user)), cancel
	}
	request := func(token string) *llmpb.JudgeReq {
		falseCriteria, trueCriteria := "Unrelated", "Direct evidence"
		return &llmpb.JudgeReq{ModelId: model.ID.String(), OwnerId: &ownerID, State: token,
			Questions: map[string]*llmpb.JudgeQuestion{
				"z_relevant": {Instructions: "Does this material answer the question?", Kind: &llmpb.JudgeQuestion_Noul{
					Noul: &llmpb.NoulQuestion{FalseCriteria: &falseCriteria, TrueCriteria: &trueCriteria}}},
				"a_irrelevant": {Instructions: "Is this material unrelated?", Kind: &llmpb.JudgeQuestion_Noul{Noul: &llmpb.NoulQuestion{}}},
				"tone": {Instructions: "Choose the tone", Kind: &llmpb.JudgeQuestion_Choice{
					Choice: &llmpb.ChoiceQuestion{Criteria: map[string]string{"calm": "Neutral", "angry": "Hostile"}}}},
				"urgency": {Instructions: "Rate urgency", Kind: &llmpb.JudgeQuestion_Score{
					Score: &llmpb.ScoreQuestion{Criteria: []string{"Can wait", "This week", "Today"}}}},
			}}
	}
	call := func(token string) (*llmpb.JudgeResp, error) {
		ctx, cancel := contextFor(ownerID)
		defer cancel()
		return cli.Judge(ctx, request(token))
	}
	for _, spoofOwner := range []bool{false, true} {
		ctx, cancel := contextFor(outsiderID)
		foreign := request("COMPONENT_JUDGE_FORBIDDEN")
		if !spoofOwner {
			foreign.OwnerId = &outsiderID
		}
		_, err := cli.Judge(ctx, foreign)
		cancel()
		if status.Code(err) != codes.PermissionDenied {
			return fmt.Errorf("components.judge: 越权/伪造owner未拒绝: %v", err)
		}
	}
	ctx, cancel := contextFor("not-a-user-id")
	_, err := cli.Judge(ctx, request("COMPONENT_JUDGE_INVALID_IDENTITY"))
	cancel()
	if status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("components.judge: 非法身份未拒绝: %v", err)
	}
	ctx, cancel = contextFor(ownerID)
	ctx = metadata.AppendToOutgoingContext(ctx, "user-id", outsiderID)
	_, err = cli.Judge(ctx, request("COMPONENT_JUDGE_DUPLICATE_IDENTITY"))
	cancel()
	if status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("components.judge: 重复身份未拒绝: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), d.timeout)
	unowned := request("COMPONENT_JUDGE_PRIVATE_WITHOUT_OWNER")
	unowned.OwnerId = nil
	_, err = cli.Judge(ctx, unowned)
	cancel()
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("components.judge: 无owner的内部调用错误获得私有模型: %v", err)
	}
	for _, token := range []string{"COMPONENT_JUDGE_WRONG_CAPABILITY", "COMPONENT_JUDGE_MISSING_KIND", "COMPONENT_JUDGE_EMPTY_QUESTIONS"} {
		req := request(token)
		switch token {
		case "COMPONENT_JUDGE_WRONG_CAPABILITY":
			req.ModelId = wrongCapability.String()
		case "COMPONENT_JUDGE_MISSING_KIND":
			req.Questions["unsupported"] = &llmpb.JudgeQuestion{Instructions: "Unsupported kind"}
		case "COMPONENT_JUDGE_EMPTY_QUESTIONS":
			req.Questions = nil
		}
		ctx, cancel := contextFor(ownerID)
		_, err := cli.Judge(ctx, req)
		cancel()
		if status.Code(err) != codes.InvalidArgument {
			return fmt.Errorf("components.judge: 无效能力/题型/问题未拒绝 %s: %v", token, err)
		}
	}
	ctx, cancel = contextFor(ownerID)
	inactive := request("COMPONENT_JUDGE_INACTIVE")
	inactive.ModelId = "01900000-0000-7000-8000-000000000012"
	_, err = cli.Judge(ctx, inactive)
	cancel()
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("components.judge: 失效模型未拒绝: %v", err)
	}
	var before struct {
		Requests []llmChatObservation `json:"requests"`
	}
	if err := d.executorProvider("/llm-components/observe", map[string]string{"model": name}, &before); err != nil {
		return err
	}
	if len(before.Requests) != 0 {
		return errors.New("components.judge: 被拒绝的调用到达了供应商")
	}
	for _, token := range []string{"COMPONENT_JUDGE_POSITIVE", "COMPONENT_JUDGE_NO_USAGE", "COMPONENT_JUDGE_INPUT_ONLY", "COMPONENT_JUDGE_OUTPUT_ONLY", "COMPONENT_JUDGE_ZERO", "COMPONENT_JUDGE_NO_CONFIDENCE"} {
		resp, err := call(token)
		if err != nil {
			return fmt.Errorf("components.judge %s: %w", token, err)
		}
		if err := requireJudgeAnswers(resp, name, token == "COMPONENT_JUDGE_NO_CONFIDENCE"); err != nil {
			return err
		}
		if token == "COMPONENT_JUDGE_NO_USAGE" {
			if resp.Usage != nil {
				return errors.New("components.judge: 未知用量被伪造")
			}
		} else {
			if resp.Usage == nil {
				return errors.New("components.judge: 已报告用量丢失")
			}
			wantInput, wantOutput := int32(13), int32(7)
			if token == "COMPONENT_JUDGE_ZERO" {
				wantInput, wantOutput = 0, 0
			}
			if !judgeTokenCount(resp.Usage.InputTokens, token != "COMPONENT_JUDGE_OUTPUT_ONLY", wantInput) ||
				!judgeTokenCount(resp.Usage.OutputTokens, token != "COMPONENT_JUDGE_INPUT_ONLY", wantOutput) {
				return errors.New("components.judge: 输入/输出用量未分别保留已知值和未知presence")
			}
		}
		if err := d.requireComponentObservation(name, token, "completed", "/native/v1/systemone"); err != nil {
			return err
		}
	}
	malformed := []string{"MISSING_ANSWER", "EXTRA_ANSWER", "WRONG_TYPE", "BAD_PROBABILITY", "MISSING_VALUE", "BAD_CHOICE", "BAD_DISTRIBUTION", "BAD_CONFIDENCE", "BAD_SCORE", "BAD_LEGEND", "MISSING_MODEL"}
	for _, mode := range append(slices.Clone(malformed), "HTTP_ERROR") {
		token := "COMPONENT_JUDGE_" + mode
		resp, err := call(token)
		if err == nil || resp != nil {
			return fmt.Errorf("components.judge: 畸形/失败响应被接受 %s", mode)
		}
		if err := requireProviderErrorRedaction(err, token, componentKey); err != nil {
			return err
		}
		state := "completed"
		if mode == "HTTP_ERROR" {
			state = "http_error"
		}
		if err := d.requireComponentObservation(name, token, state, "/native/v1/systemone"); err != nil {
			return err
		}
	}
	// More cancellations than the model's configured admission pool can hold.
	for i := range 11 {
		token := fmt.Sprintf("COMPONENT_JUDGE_CANCEL_%d", i)
		ctx, cancel := contextFor(ownerID)
		finished := make(chan error, 1)
		go func() { _, err := cli.Judge(ctx, request(token)); finished <- err }()
		observed := d.requireComponentObservation(name, token, "received", "/native/v1/systemone")
		cancel()
		callErr := <-finished
		if observed != nil {
			return observed
		}
		if status.Code(callErr) != codes.Canceled {
			return fmt.Errorf("components.judge: 取消未返回Canceled: %v", callErr)
		}
		if err := d.requireComponentObservation(name, token, "cancelled", "/native/v1/systemone"); err != nil {
			return err
		}
	}
	ctx, cancel = contextFor(ownerID)
	_, err = cli.Judge(ctx, request("COMPONENT_JUDGE_DEADLINE"))
	cancel()
	if status.Code(err) != codes.DeadlineExceeded {
		return fmt.Errorf("components.judge: 总体截止未返回DeadlineExceeded: %v", err)
	}
	if err := d.requireComponentObservation(name, "COMPONENT_JUDGE_DEADLINE", "cancelled", "/native/v1/systemone"); err != nil {
		return err
	}
	resp, err := call("COMPONENT_JUDGE_RECOVERED")
	if err != nil {
		return fmt.Errorf("components.judge: 取消后限流配额未释放: %w", err)
	}
	if err := requireJudgeAnswers(resp, name, false); err != nil {
		return err
	}
	// The real service configuration admits ten concurrent requests per model.
	poolCtx, cancelPool := contextFor(ownerID)
	poolFinished := make(chan error, 10)
	defer cancelPool()
	for i := range 10 {
		token := fmt.Sprintf("COMPONENT_JUDGE_CANCEL_POOL_%d", i)
		go func() { _, err := cli.Judge(poolCtx, request(token)); poolFinished <- err }()
		if err := d.requireComponentObservation(name, token, "received", "/native/v1/systemone"); err != nil {
			return err
		}
	}
	_, err = call("COMPONENT_JUDGE_CONCURRENCY_REJECTED")
	if status.Code(err) != codes.ResourceExhausted {
		return fmt.Errorf("components.judge: 并发超限未拒绝: %v", err)
	}
	cancelPool()
	for range 10 {
		if err := <-poolFinished; status.Code(err) != codes.Canceled {
			return fmt.Errorf("components.judge: 并发池取消未传播: %v", err)
		}
	}
	for i := range 10 {
		if err := d.requireComponentObservation(name, fmt.Sprintf("COMPONENT_JUDGE_CANCEL_POOL_%d", i), "cancelled", "/native/v1/systemone"); err != nil {
			return err
		}
	}
	// A distinct current model name starts a fresh real 100-RPM budget, so
	// cancellation/deadline work cannot move this check across an old window.
	rpmName := name + "-rpm"
	if err := d.request(http.MethodPut, "/models/"+model.ID.String(), owner.token, map[string]string{"model_name": rpmName}, nil); err != nil {
		return err
	}
	// Unknown-usage replies do not manufacture billing facts.
	rejectedToken := ""
	for i := range 101 {
		token := fmt.Sprintf("COMPONENT_JUDGE_NO_USAGE_RPM_%d", i)
		resp, err := call(token)
		if status.Code(err) == codes.ResourceExhausted {
			rejectedToken = token
			break
		}
		if err != nil {
			return fmt.Errorf("components.judge: RPM试验调用失败: %w", err)
		}
		if err := requireJudgeAnswers(resp, rpmName, false); err != nil {
			return err
		}
	}
	if rejectedToken == "" {
		return errors.New("components.judge: RPM超限未拒绝")
	}
	for _, observedName := range []string{name, rpmName} {
		var observed struct {
			Requests []llmChatObservation `json:"requests"`
		}
		if err := d.executorProvider("/llm-components/observe", map[string]string{"model": observedName}, &observed); err != nil {
			return err
		}
		if slices.ContainsFunc(observed.Requests, func(row llmChatObservation) bool {
			return row.Token == rejectedToken || row.Token == "COMPONENT_JUDGE_CONCURRENCY_REJECTED"
		}) {
			return errors.New("components.judge: 超限请求仍到达供应商")
		}
	}
	if err := d.requireJudgeBilling(owner, model.ID, name, len(malformed)+3); err != nil {
		return err
	}
	platformID := entityID("01900000-0000-7000-8000-000000000011")
	platformName := "e2e-component-judge-platform-2026-10-01"
	if !slices.ContainsFunc(listed.Items, func(item judgeModelView) bool {
		return item.ID == platformID && item.Name == platformName && item.OwnerType == "platform" &&
			item.OwnerID == nil && item.Provider == "typesafe" && item.Capability == "judge" && item.APIKey == ""
	}) {
		return errors.New("components.judge: 平台模型不可见或泄露供应凭据")
	}
	platformCall := func(token string) (*llmpb.JudgeResp, error) {
		req := request(token)
		req.ModelId = platformID.String()
		ctx, cancel := contextFor(ownerID)
		defer cancel()
		return cli.Judge(ctx, req)
	}
	balance := func(user account) (float64, error) {
		var profile struct {
			Balance float64 `json:"balance"`
		}
		err := d.request(http.MethodGet, "/users/me", user.token, nil, &profile)
		return profile.Balance, err
	}
	if value, err := balance(owner); err != nil || value != 0 {
		return fmt.Errorf("components.judge: 自有模型调用不应扣余额: balance=%g error=%v", value, err)
	}
	_, err = platformCall("COMPONENT_JUDGE_PLATFORM_INSUFFICIENT")
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("components.judge: 平台模型余额不足未拒绝: %v", err)
	}
	var denied struct {
		Requests []llmChatObservation `json:"requests"`
	}
	if err := d.executorProvider("/llm-components/observe", map[string]string{"model": platformName}, &denied); err != nil {
		return err
	}
	if len(denied.Requests) != 0 {
		return errors.New("components.judge: 余额不足仍向供应商发送材料")
	}
	if err := d.request(http.MethodPost, "/users/me/recharge", owner.token, map[string]float64{"amount": 2}, nil); err != nil {
		return err
	}
	for _, mode := range []string{"POSITIVE", "INPUT_ONLY", "OUTPUT_ONLY", "ZERO", "NO_USAGE", "MISSING_ANSWER"} {
		token := "COMPONENT_JUDGE_PLATFORM_" + mode
		resp, err := platformCall(token)
		if mode == "MISSING_ANSWER" {
			if err == nil || resp != nil {
				return errors.New("components.judge: 平台畸形判断被接受")
			}
		} else if err != nil {
			return err
		} else if err := requireJudgeAnswers(resp, platformName, false); err != nil {
			return err
		}
		if err := d.requireComponentObservation(platformName, token, "completed", "/native/v1/systemone"); err != nil {
			return err
		}
	}
	if value, err := balance(owner); err != nil || math.Abs(value-(2-.000078)) > 1e-9 {
		return fmt.Errorf("components.judge: 平台已知用量费用未扣到调用owner: balance=%g error=%v", value, err)
	}
	if value, err := balance(outsider); err != nil || value != 0 {
		return fmt.Errorf("components.judge: 平台费用错误归属其他用户: balance=%g error=%v", value, err)
	}
	return d.requireJudgeBilling(owner, platformID, platformName, 2)
}

func judgeTokenCount(value *int32, known bool, want int32) bool {
	if !known {
		return value == nil
	}
	return value != nil && *value == want
}

func requireJudgeAnswers(resp *llmpb.JudgeResp, requestedModel string, noConfidence bool) error {
	if resp == nil || resp.Model != requestedModel+"-actual-2026-10-01" || len(resp.Answers) != 4 {
		return errors.New("components.judge: 实际模型版本或命名答案不匹配")
	}
	if resp.Answers["z_relevant"].GetNoul() == nil || resp.Answers["a_irrelevant"].GetNoul() == nil ||
		resp.Answers["z_relevant"].GetNoul().Probability != .93 || resp.Answers["a_irrelevant"].GetNoul().Probability != .07 {
		return errors.New("components.judge: Noul题型/概率/同名关联丢失")
	}
	choice, score := resp.Answers["tone"].GetChoice(), resp.Answers["urgency"].GetScore()
	if choice == nil || choice.Choice != "calm" || len(choice.Probabilities) != 2 ||
		choice.Probabilities["calm"] != .8 || choice.Probabilities["angry"] != .2 ||
		len(choice.Criteria) != 2 || choice.Criteria["calm"] != "Neutral" || choice.Criteria["angry"] != "Hostile" {
		return errors.New("components.judge: Choice选项/分布/定义丢失")
	}
	if score == nil || score.Score != 1.7 || len(score.Probabilities) != 3 ||
		score.Probabilities["0"] != .1 || score.Probabilities["1"] != .1 || score.Probabilities["2"] != .8 ||
		len(score.Legend) != 3 || score.Legend["0"] != "Can wait" || score.Legend["1"] != "This week" || score.Legend["2"] != "Today" {
		return errors.New("components.judge: Score评分/等级/分布丢失")
	}
	if noConfidence {
		if choice.Confidence != nil || score.Confidence != nil {
			return errors.New("components.judge: 缺失/null置信信息被伪造")
		}
	} else if choice.Confidence == nil || *choice.Confidence != .7 || score.Confidence == nil || *score.Confidence != .55 {
		return errors.New("components.judge: 供应商置信信息丢失")
	}
	return nil
}

func (d *driver) requireJudgeBilling(owner account, modelID entityID, name string, completeCount int) error {
	var response struct {
		Items []struct {
			ID           entityID  `json:"id"`
			ModelID      entityID  `json:"model_id"`
			ModelName    string    `json:"model_name"`
			Capability   string    `json:"capability"`
			OwnerID      *entityID `json:"owner_id"`
			OwnerType    string    `json:"owner_type"`
			BotID        *entityID `json:"bot_id"`
			InputTokens  *int32    `json:"input_tokens"`
			OutputTokens *int32    `json:"output_tokens"`
			InputCost    float64   `json:"input_cost"`
			OutputCost   float64   `json:"output_cost"`
			TotalCost    float64   `json:"total_cost"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := d.request(http.MethodGet, "/models/billing/records?page=1&page_size=100", owner.token, nil, &response); err != nil {
		return err
	}
	if response.Total != len(response.Items) {
		return errors.New("components.judge.billing: 公开记录未完整分页")
	}
	complete, inputOnly, outputOnly, zero := 0, 0, 0, 0
	seen := map[entityID]bool{}
	for _, row := range response.Items {
		if row.ModelID != modelID {
			continue
		}
		if row.ID == "" || seen[row.ID] || row.ModelName != name || row.Capability != "judge" ||
			!hasEntityID(row.OwnerID, owner.id) || row.OwnerType != "user" || row.BotID != nil {
			return errors.New("components.judge.billing: 模型/归属/记录身份不匹配")
		}
		seen[row.ID] = true
		wantCost := 0.0
		switch {
		case judgeTokenCount(row.InputTokens, true, 13) && judgeTokenCount(row.OutputTokens, true, 7):
			complete++
			wantCost = .000026
		case judgeTokenCount(row.InputTokens, true, 13) && row.OutputTokens == nil:
			inputOnly++
			wantCost = .000026
		case row.InputTokens == nil && judgeTokenCount(row.OutputTokens, true, 7):
			outputOnly++
		case judgeTokenCount(row.InputTokens, true, 0) && judgeTokenCount(row.OutputTokens, true, 0):
			zero++
		default:
			return errors.New("components.judge.billing: 未知用量变成零或已知数量丢失")
		}
		if math.IsNaN(row.TotalCost) || math.IsInf(row.TotalCost, 0) || row.OutputCost != 0 ||
			math.Abs(row.InputCost-wantCost) > 1e-12 || math.Abs(row.TotalCost-wantCost) > 1e-12 {
			return errors.New("components.judge.billing: 免费输出被错误收费或真实输入费用丢失")
		}
	}
	if complete != completeCount || inputOnly != 1 || outputOnly != 1 || zero != 1 {
		return fmt.Errorf("components.judge.billing: 完整/仅输入/仅输出/已知零记录=%d/%d/%d/%d，期望%d/1/1/1；失败结果不得抹掉已知用量，未知/取消不得编造", complete, inputOnly, outputOnly, zero, completeCount)
	}
	return nil
}

func requireComponentVectors(resp *llmpb.EmbedResp, model string) error {
	want := [][]float32{{float32(.1000000001), float32(-.2000000001), float32(1.00000006)}, {float32(.333333333333), float32(2.00000013), float32(-.123456789)}}
	if resp == nil || resp.Model != model || len(resp.Data) != len(want) {
		return errors.New("components.embed: 模型或向量数量不匹配")
	}
	for i, values := range want {
		if resp.Data[i] == nil || resp.Data[i].Index != int32(i) || !slices.Equal(resp.Data[i].Embedding, values) {
			return errors.New("components.embed: provider index对应/维度/float32转换不匹配")
		}
	}
	return nil
}

func (d *driver) requireComponentObservation(model, token, state, path string) error {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		var response struct {
			Requests []llmChatObservation `json:"requests"`
		}
		if err := d.executorProvider("/llm-components/observe", map[string]string{"model": model}, &response); err != nil {
			return err
		}
		for _, row := range response.Requests {
			if row.State == "redirect_followed" {
				return errors.New("components.provider: gateway跟随redirect泄露凭据")
			}
			if row.Token != token {
				continue
			}
			if row.State == state {
				if row.Path != path || row.AuthDigest != bearerDigest(componentKey) {
					return errors.New("components.provider: HTTP路由或当前凭据不匹配")
				}
				return nil
			}
			if row.State == "invalid_request" || row.State == "cancel_not_observed" {
				return fmt.Errorf("components.provider: %s state=%s", token, row.State)
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("components.provider: 未观察到%s state=%s", token, state)
}

func (d *driver) requireComponentBilling(owner account, id entityID, name, capability string, count int, input, output int32) error {
	records, err := d.runtimeBillingRecords(owner)
	if err != nil {
		return err
	}
	matched := 0
	for _, record := range records {
		if record.ModelID != id {
			continue
		}
		matched++
		if record.ModelName != name || record.Capability != capability || record.BotID != nil || record.InputTokens == nil || record.OutputTokens == nil || *record.InputTokens != input || *record.OutputTokens != output || math.Abs(record.TotalCost-(float64(input)*2+float64(output)*3)/1e6) > 1e-12 {
			return errors.New("components.billing: 公开记录模型/owner/usage/费用失配")
		}
	}
	if matched != count {
		return fmt.Errorf("components.billing: %s期望%d条正usage记录，实际%d；缺失/零/失败/取消不得计费", capability, count, matched)
	}
	return nil
}
