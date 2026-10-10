package handler

import (
	"context"
	pb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *KnowledgeBaseHandler) ResourceAuthorization(ctx context.Context, in *pb.ResourceAuthorizationReq) (*pb.ResourceAuthorizationResp, error) {
	if h.DB == nil {
		return nil, status.Error(codes.Unavailable, "resource authorization unavailable")
	}
	body, err := resourcegrant.Manage(ctx, h.DB, getCallerID(ctx), "knowledge", in.Body)
	if err != nil {
		return nil, err
	}
	return &pb.ResourceAuthorizationResp{Body: body}, nil
}
