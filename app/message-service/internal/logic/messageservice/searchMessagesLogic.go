package messageservicelogic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type SearchMessagesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSearchMessagesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SearchMessagesLogic {
	return &SearchMessagesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func hasAnySearchCondition(in *message.SearchMessagesReq) bool {
	return in.GetKeyword() != "" ||
		in.GetSenderId() > 0 ||
		in.StartTime != nil ||
		in.EndTime != nil ||
		len(in.GetMessageTypes()) > 0
}

type esHitSource struct {
	MessageID string `json:"message_id"`
}

type esSearchHit struct {
	Source    esHitSource         `json:"_source"`
	Highlight map[string][]string `json:"highlight"`
}

type esSearchResponse struct {
	Hits struct {
		Total struct {
			Value int64 `json:"value"`
		} `json:"total"`
		Hits []esSearchHit `json:"hits"`
	} `json:"hits"`
	Aggregations struct {
		TypeCounts struct {
			Buckets []struct {
				Key   int32 `json:"key"`
				Count int64 `json:"doc_count"`
			} `json:"buckets"`
		} `json:"type_counts"`
	} `json:"aggregations"`
}

func buildESQuery(in *message.SearchMessagesReq, page, pageSize int, convIDs []int64) map[string]any {
	boolQ := map[string]any{}

	var musts []map[string]any
	var filters []map[string]any

	if in.ConversationId != nil && in.GetConversationId() > 0 {
		musts = append(musts, map[string]any{
			"term": map[string]any{"conv_id": in.GetConversationId()},
		})
	}

	if keyword := in.GetKeyword(); keyword != "" {
		musts = append(musts, map[string]any{
			"match": map[string]any{"text": keyword},
		})
	}

	if in.GetSenderId() > 0 {
		filters = append(filters, map[string]any{
			"term": map[string]any{"sender_id": in.GetSenderId()},
		})
	}

	if senderType := in.GetSenderType(); senderType != "" {
		filters = append(filters, map[string]any{
			"term": map[string]any{"sender_type": senderType},
		})
	}

	if len(in.GetMessageTypes()) > 0 {
		types := make([]int32, len(in.GetMessageTypes()))
		for i, t := range in.GetMessageTypes() {
			types[i] = int32(t)
		}
		filters = append(filters, map[string]any{
			"terms": map[string]any{"msg_type": types},
		})
	}

	var timeRange map[string]any
	if in.StartTime != nil || in.EndTime != nil {
		timeRange = map[string]any{}
		if in.StartTime != nil {
			timeRange["gte"] = *in.StartTime
		}
		if in.EndTime != nil {
			timeRange["lte"] = *in.EndTime
		}
	}

	if timeRange != nil {
		filters = append(filters, map[string]any{
			"range": map[string]any{"created_at": timeRange},
		})
	}

	if len(musts) > 0 {
		boolQ["must"] = musts
	}
	if len(convIDs) > 0 {
		filters = append(filters, map[string]any{
			"terms": map[string]any{"conv_id": convIDs},
		})
	}
	if len(filters) > 0 {
		boolQ["filter"] = filters
	}

	query := map[string]any{
		"query": map[string]any{
			"bool": boolQ,
		},
		"from": (page - 1) * pageSize,
		"size": pageSize,
		"sort": []any{
			map[string]any{"created_at": "desc"},
		},
		"highlight": map[string]any{
			"fields": map[string]any{
				"text": map[string]any{
					"pre_tags":            []string{"<em class=\"search-highlight\">"},
					"post_tags":           []string{"</em>"},
					"fragment_size":       60,
					"number_of_fragments": 1,
				},
			},
		},
		"aggs": map[string]any{
			"type_counts": map[string]any{
				"terms": map[string]any{
					"field": "msg_type",
					"size":  20,
				},
			},
		},
	}

	return query
}

func (l *SearchMessagesLogic) SearchMessages(in *message.SearchMessagesReq) (*message.SearchMessagesResp, error) {
	if l.svcCtx == nil || l.svcCtx.ConvClient == nil || in == nil {
		return nil, errors.New(errors.CodeInternal, "service not initialized")
	}

	isConvSearch := in.ConversationId != nil && in.GetConversationId() > 0
	if !isConvSearch && in.GetKeyword() == "" {
		return nil, errors.New(errors.CodeInvalidParam, "keyword is required for global search")
	}
	if isConvSearch && !hasAnySearchCondition(in) {
		return nil, errors.New(errors.CodeInvalidParam, "at least one search condition is required")
	}
	if in.GetSenderType() != "" && in.GetSenderType() != "user" && in.GetSenderType() != "bot" {
		return nil, errors.New(errors.CodeInvalidParam, "invalid sender_type")
	}
	if in.StartTime != nil && in.EndTime != nil && in.GetStartTime() > in.GetEndTime() {
		return nil, errors.New(errors.CodeInvalidParam, "invalid time range")
	}

	if isConvSearch {
		isMember, err := l.svcCtx.ConvClient.IsMember(l.ctx, in.GetConversationId(), in.GetUserId())
		if err != nil {
			return nil, errors.Wrap(errors.CodeRPCError, "check conversation member failed", err)
		}
		if !isMember {
			return nil, errors.ErrForbidden
		}
	}

	page := int(in.Pagination.GetPage())
	if page <= 0 {
		page = 1
	}
	pageSize := int(in.Pagination.GetPageSize())
	if pageSize <= 0 {
		pageSize = 20
	}
	maxPageSize := l.svcCtx.Config.Message.MaxPageSize
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	if l.svcCtx.ESClient == nil {
		return nil, errors.New(errors.CodeInternal, "es client not configured")
	}

	var convIDs []int64
	if !isConvSearch {
		ids, err := l.svcCtx.ConvClient.ListUserConvIDs(l.ctx, in.GetUserId())
		if err != nil {
			return nil, errors.Wrap(errors.CodeRPCError, "list user conversations failed", err)
		}
		convIDs = ids
	}

	esQuery := buildESQuery(in, page, pageSize, convIDs)
	raw, err := l.svcCtx.ESClient.Search(l.ctx, consts.ESIndexMessages, esQuery)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "es search failed", err)
	}

	var esResp esSearchResponse
	if err := json.Unmarshal(raw, &esResp); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "parse es response failed", err)
	}

	total := esResp.Hits.Total.Value

	msgIDs := make([]int64, 0, len(esResp.Hits.Hits))
	highlights := make(map[string]string)
	for _, hit := range esResp.Hits.Hits {
		var id int64
		if _, err := fmt.Sscanf(hit.Source.MessageID, "%d", &id); err != nil {
			continue
		}
		msgIDs = append(msgIDs, id)

		// Extract first highlight fragment
		if fragments, ok := hit.Highlight["text"]; ok && len(fragments) > 0 {
			highlights[hit.Source.MessageID] = fragments[0]
		}
	}

	var pbMsgs []*message.Message
	if len(msgIDs) > 0 {
		msgs, err := l.svcCtx.MessageRepo.GetByIDs(l.ctx, msgIDs)
		if err != nil {
			return nil, errors.Wrap(errors.CodeInternal, "fetch messages failed", err)
		}

		msgMap := make(map[int64]*model.Message, len(msgs))
		for i := range msgs {
			msgMap[msgs[i].ID] = &msgs[i]
		}
		for _, id := range msgIDs {
			if m, ok := msgMap[id]; ok {
				pbMsgs = append(pbMsgs, modelToPbMessage(m))
			}
		}

		hydrateReplySummaries(l.ctx, l.svcCtx.MessageRepo, l.svcCtx.UserClient, l.svcCtx.BotRepo, pbMsgs)
	}

	totalPages := int32(total / int64(pageSize))
	if total%int64(pageSize) != 0 {
		totalPages++
	}

	// Build type counts from aggregations
	typeCounts := make([]*message.TypeCount, 0, len(esResp.Aggregations.TypeCounts.Buckets))
	for _, b := range esResp.Aggregations.TypeCounts.Buckets {
		typeCounts = append(typeCounts, &message.TypeCount{
			MsgType: b.Key,
			Count:   b.Count,
		})
	}

	return &message.SearchMessagesResp{
		Messages:   pbMsgs,
		Highlights: highlights,
		TypeCounts: typeCounts,
		Pagination: &common.PaginationResp{
			Page:       int32(page),
			PageSize:   int32(pageSize),
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}
