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
	defer conn.Close()
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
		if record.ModelName != name || record.Capability != capability || record.BotID != nil || record.InputTokens != input || record.OutputTokens != output || math.Abs(record.TotalCost-(float64(input)*2+float64(output)*3)/1e6) > 1e-12 {
			return errors.New("components.billing: 公开记录模型/owner/usage/费用失配")
		}
	}
	if matched != count {
		return fmt.Errorf("components.billing: %s期望%d条正usage记录，实际%d；缺失/零/失败/取消不得计费", capability, count, matched)
	}
	return nil
}
