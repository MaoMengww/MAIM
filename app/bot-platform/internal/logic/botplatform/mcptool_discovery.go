package botplatform

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/bot-platform/internal/component"
	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/zeromicro/go-zero/core/logx"
)

type DiscoverMcpToolsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDiscoverMcpToolsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DiscoverMcpToolsLogic {
	return &DiscoverMcpToolsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DiscoverMcpToolsLogic) DiscoverMcpTools(in *pb.DiscoverMcpToolsReq) (*pb.DiscoverMcpToolsResp, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}

	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.McpServerId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "mcp server not found", err)
	}

	if !mcpCanModify(srv, in.UserId) {
		return nil, ErrBotForbidden
	}

	client := component.NewMCPClient(srv)

	ctx, cancel := context.WithTimeout(l.ctx, 30*time.Second)
	defer cancel()

	toolDefs, err := client.ListTools(ctx)
	if err != nil {
		l.Errorf("discover tools from mcp server %d (%s) failed: %v", in.McpServerId, srv.URL, err)
		return nil, errors.Wrap(errors.CodeInternal, "discover tools failed, please check if the MCP server is reachable", err)
	}

	tools := make([]model.McpTool, 0, len(toolDefs))
	for _, td := range toolDefs {
		schemaJSON := ""
		if td.InputSchema != nil {
			b, _ := json.Marshal(td.InputSchema)
			schemaJSON = string(b)
		}
		tools = append(tools, model.McpTool{
			McpServerID: in.McpServerId,
			Name:        td.Name,
			Description: td.Description,
			InputSchema: schemaJSON,
		})
	}

	if err := l.svcCtx.Repo.UpsertMcpTools(l.ctx, in.McpServerId, tools); err != nil {
		l.Errorf("save discovered tools for mcp server %d failed: %v", in.McpServerId, err)
		return nil, errors.Wrap(errors.CodeDBError, "save discovered tools failed", err)
	}

	l.Infof("mcp tools discovered: server_id=%d count=%d", in.McpServerId, len(tools))

	items := make([]*pb.McpToolInfo, 0, len(tools))
	for _, t := range tools {
		items = append(items, &pb.McpToolInfo{
			Id:          t.ID,
			McpServerId: t.McpServerID,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			UpdatedAt:   t.UpdatedAt.Unix(),
		})
	}

	return &pb.DiscoverMcpToolsResp{Tools: items}, nil
}

type ListMcpToolsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListMcpToolsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListMcpToolsLogic {
	return &ListMcpToolsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ListMcpToolsLogic) ListMcpTools(in *pb.ListMcpToolsReq) (*pb.ListMcpToolsResp, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}

	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.McpServerId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "mcp server not found", err)
	}

	if !mcpCanView(srv, in.UserId) {
		return nil, ErrBotForbidden
	}

	tools, err := l.svcCtx.Repo.ListMcpTools(l.ctx, in.McpServerId)
	if err != nil {
		l.Errorf("list mcp tools for server %d failed: %v", in.McpServerId, err)
		return nil, errors.Wrap(errors.CodeDBError, "list mcp tools failed", err)
	}

	items := make([]*pb.McpToolInfo, 0, len(tools))
	for _, t := range tools {
		items = append(items, &pb.McpToolInfo{
			Id:          t.ID,
			McpServerId: t.McpServerID,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			UpdatedAt:   t.UpdatedAt.Unix(),
		})
	}

	return &pb.ListMcpToolsResp{Tools: items}, nil
}

// AutoDiscoverMcpTools connects to an MCP server and stores the discovered tools.
// Used internally after Create/Update to auto-discover tools. Best-effort, silent on failure.
func AutoDiscoverMcpTools(ctx context.Context, srvCtx *svc.ServiceContext, serverID int64, srv *model.McpServer) {
	if srv.URL == "" || (srv.Transport != "" && srv.Transport != "sse" && srv.Transport != "http-streamable") {
		return
	}

	client := component.NewMCPClient(srv)

	discCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	toolDefs, err := client.ListTools(discCtx)
	if err != nil {
		return
	}

	tools := make([]model.McpTool, 0, len(toolDefs))
	for _, td := range toolDefs {
		schemaJSON := ""
		if td.InputSchema != nil {
			b, _ := json.Marshal(td.InputSchema)
			schemaJSON = string(b)
		}
		tools = append(tools, model.McpTool{
			McpServerID: serverID,
			Name:        td.Name,
			Description: td.Description,
			InputSchema: schemaJSON,
		})
	}

	_ = srvCtx.Repo.UpsertMcpTools(ctx, serverID, tools)
}
