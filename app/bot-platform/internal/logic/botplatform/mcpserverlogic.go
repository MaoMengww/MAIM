package botplatform

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/errors"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// ========== Global MCP Server Management ==========

type CreateMcpServerLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewCreateMcpServerLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateMcpServerLogic {
	return &CreateMcpServerLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *CreateMcpServerLogic) CreateMcpServer(in *pb.CreateMcpServerReq) (*pb.McpServerInfo, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}
	if in.Name == "" {
		return nil, errors.New(errors.CodeInvalidParam, "mcp server name is required")
	}
	if in.Transport == "" {
		in.Transport = "sse"
	}

	srv := &model.McpServer{
		Name:        in.Name,
		Description: in.Description,
		Transport:   in.Transport,
		URL:         in.Url,
		Command:     in.Command,
		Args:        in.Args,
		Enabled:     true,
		CreatedBy:   in.UserId,
	}
	if in.Env != "" {
		srv.Env = []byte(in.Env)
	}
	// Parse auth_config JSON
	if in.AuthConfig != "" {
		var authCfg model.MCPAuthConfig
		if err := json.Unmarshal([]byte(in.AuthConfig), &authCfg); err == nil {
			srv.AuthConfig = &authCfg
		}
	}
	// Parse advanced_config JSON
	if in.AdvancedConfig != "" {
		var advCfg model.MCPAdvancedConfig
		if err := json.Unmarshal([]byte(in.AdvancedConfig), &advCfg); err == nil {
			if advCfg.Timeout <= 0 {
				advCfg.Timeout = 30
			}
			if advCfg.RetryCount <= 0 {
				advCfg.RetryCount = 3
			}
			if advCfg.RetryDelay <= 0 {
				advCfg.RetryDelay = 1
			}
			srv.AdvancedConfig = &advCfg
		}
	} else {
		srv.AdvancedConfig = &model.MCPAdvancedConfig{Timeout: 30, RetryCount: 3, RetryDelay: 1}
	}

	if err := l.svcCtx.Repo.CreateMcpServer(l.ctx, srv); err != nil {
		l.Errorf("create mcp server failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "create mcp server failed", err)
	}

	l.Infof("mcp server created: name=%s", srv.Name)
	return mcpServerToProto(srv), nil
}

type UpdateMcpServerLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateMcpServerLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateMcpServerLogic {
	return &UpdateMcpServerLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateMcpServerLogic) UpdateMcpServer(in *pb.UpdateMcpServerReq) (*pb.McpServerInfo, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}
	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New(errors.CodeNotFound, "mcp server not found")
		}
		l.Errorf("get mcp server failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "get mcp server failed", err)
	}

	if !mcpCanModify(srv, in.UserId) {
		return nil, ErrBotForbidden
	}

	updates := make(map[string]any)
	if in.Name != "" {
		updates["name"] = in.Name
	}
	if in.Description != "" {
		updates["description"] = in.Description
	}
	if in.Transport != "" {
		updates["transport"] = in.Transport
	}
	if in.Url != "" {
		updates["url"] = in.Url
	}
	if in.Command != "" {
		updates["command"] = in.Command
	}
	if in.Args != nil {
		updates["args"] = in.Args
	}
	if in.Env != "" {
		updates["env"] = []byte(in.Env)
	}
	if in.AuthConfig != "" {
		var authCfg model.MCPAuthConfig
		if err := json.Unmarshal([]byte(in.AuthConfig), &authCfg); err == nil {
			updates["auth_config"] = &authCfg
		}
	}
	if in.AdvancedConfig != "" {
		var advCfg model.MCPAdvancedConfig
		if err := json.Unmarshal([]byte(in.AdvancedConfig), &advCfg); err == nil {
			updates["advanced_config"] = &advCfg
		}
	}
	if in.Enabled {
		updates["enabled"] = true
	}
	if in.Status == "disabled" {
		updates["enabled"] = false
	}

	if len(updates) > 0 {
		if err := l.svcCtx.Repo.UpdateMcpServer(l.ctx, in.Id, updates); err != nil {
			l.Errorf("update mcp server failed: %v", err)
			return nil, errors.Wrap(errors.CodeDBError, "update mcp server failed", err)
		}
	}

	srv, _ = l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	return mcpServerToProto(srv), nil
}

type DeleteMcpServerLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteMcpServerLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteMcpServerLogic {
	return &DeleteMcpServerLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DeleteMcpServerLogic) DeleteMcpServer(in *pb.DeleteMcpServerReq) (*common.BaseResponse, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}
	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New(errors.CodeNotFound, "mcp server not found")
		}
		return nil, errors.Wrap(errors.CodeDBError, "get mcp server failed", err)
	}

	if !mcpCanModify(srv, in.UserId) {
		return nil, ErrBotForbidden
	}

	if err := l.svcCtx.Repo.DeleteMcpServer(l.ctx, in.Id); err != nil {
		l.Errorf("delete mcp server failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "delete mcp server failed", err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}

type GetMcpServerLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetMcpServerLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetMcpServerLogic {
	return &GetMcpServerLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetMcpServerLogic) GetMcpServer(in *pb.GetMcpServerReq) (*pb.McpServerInfo, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}
	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New(errors.CodeNotFound, "mcp server not found")
		}
		return nil, errors.Wrap(errors.CodeDBError, "get mcp server failed", err)
	}

	// only owner or platform-public can view
	if !mcpCanView(srv, in.UserId) {
		return nil, ErrBotForbidden
	}
	return mcpServerToProto(srv), nil
}

type ListMcpServersLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListMcpServersLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListMcpServersLogic {
	return &ListMcpServersLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ListMcpServersLogic) ListMcpServers(in *pb.ListMcpServersReq) (*pb.ListMcpServersResp, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}
	page := int32(1)
	pageSize := int32(20)
	if in.Pagination != nil {
		if in.Pagination.Page > 0 {
			page = in.Pagination.Page
		}
		if in.Pagination.PageSize > 0 && in.Pagination.PageSize <= 100 {
			pageSize = in.Pagination.PageSize
		}
	}

	offset := int((page - 1) * pageSize)
	// user-scoped: only user's own + platform public (created_by=0)
	servers, total, err := l.svcCtx.Repo.ListUserMcpServers(l.ctx, in.UserId, in.Status, offset, int(pageSize))
	if err != nil {
		l.Errorf("list mcp servers failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "list mcp servers failed", err)
	}

	items := make([]*pb.McpServerInfo, 0, len(servers))
	for i := range servers {
		items = append(items, mcpServerToProto(&servers[i]))
	}

	totalPages := int32(0)
	if total > 0 {
		totalPages = int32((total + int64(pageSize) - 1) / int64(pageSize))
	}

	l.Infof("mcp servers listed: user_id=%d count=%d", in.UserId, len(items))
	return &pb.ListMcpServersResp{
		Servers: items,
		Pagination: &common.PaginationResp{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}

// ========== Bot MCP Assignment ==========

type AssignMcpToBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewAssignMcpToBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AssignMcpToBotLogic {
	return &AssignMcpToBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *AssignMcpToBotLogic) AssignMcpToBot(in *pb.AssignMcpToBotReq) (*common.BaseResponse, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrBotNotFound
		}
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}

	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}

	// verify mcp server exists and belongs to bot's owner (or platform public)
	mcp, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.McpServerId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New(errors.CodeNotFound, "mcp server not found")
		}
		return nil, errors.Wrap(errors.CodeDBError, "get mcp server failed", err)
	}
	if !mcpCanAssign(mcp, bot.OwnerID) {
		return nil, ErrBotForbidden
	}

	assoc := &model.BotMcpServer{
		BotID:       in.BotId,
		McpServerID: in.McpServerId,
		Enabled:     true,
	}

	if err := l.svcCtx.Repo.AssignMcpToBot(l.ctx, assoc); err != nil {
		l.Errorf("assign mcp to bot failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "assign mcp to bot failed", err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}

type UnassignMcpFromBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUnassignMcpFromBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UnassignMcpFromBotLogic {
	return &UnassignMcpFromBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UnassignMcpFromBotLogic) UnassignMcpFromBot(in *pb.UnassignMcpFromBotReq) (*common.BaseResponse, error) {
	if l.svcCtx.Repo == nil {
		return nil, errors.New(errors.CodeInternal, "repo not initialized")
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrBotNotFound
		}
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}

	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}

	if err := l.svcCtx.Repo.UnassignMcpFromBot(l.ctx, in.BotId, in.McpServerId); err != nil {
		l.Errorf("unassign mcp from bot failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "unassign mcp failed", err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}

type ListBotMcpServersLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListBotMcpServersLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListBotMcpServersLogic {
	return &ListBotMcpServersLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ListBotMcpServersLogic) ListBotMcpServers(in *pb.ListBotMcpServersReq) (*pb.ListBotMcpServersResp, error) {
	assocs, err := l.svcCtx.Repo.ListBotMcpServers(l.ctx, in.BotId)
	if err != nil {
		l.Errorf("list bot mcp servers failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "list bot mcp servers failed", err)
	}

	items := make([]*pb.BotMcpServerInfo, 0, len(assocs))
	for _, a := range assocs {
		srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, a.McpServerID)
		if err != nil {
			l.Errorf("get mcp server %d failed: %v", a.McpServerID, err)
			continue
		}
		items = append(items, &pb.BotMcpServerInfo{
			Id:          a.ID,
			McpServerId: srv.ID,
			Name:        srv.Name,
			Description: srv.Description,
			Transport:   srv.Transport,
			Url:         srv.URL,
			TimeoutMs:   timeoutFromAdvanced(srv.AdvancedConfig),
			Status:      statusFromEnabled(srv.Enabled),
			Enabled:     a.Enabled,
		})
	}

	return &pb.ListBotMcpServersResp{Servers: items}, nil
}

type UpdateBotMcpServerLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateBotMcpServerLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateBotMcpServerLogic {
	return &UpdateBotMcpServerLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateBotMcpServerLogic) UpdateBotMcpServer(in *pb.UpdateBotMcpServerReq) (*common.BaseResponse, error) {
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrBotNotFound
		}
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}

	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}

	if err := l.svcCtx.Repo.UpdateBotMcpServer(l.ctx, in.BotId, in.McpServerId, in.Enabled); err != nil {
		l.Errorf("update bot mcp server failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "update bot mcp server failed", err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}

// ========== Helpers ==========

func mcpCanView(srv *model.McpServer, userID int64) bool {
	return srv.CreatedBy == 0 || srv.CreatedBy == userID
}

func mcpCanModify(srv *model.McpServer, userID int64) bool {
	return srv.CreatedBy > 0 && srv.CreatedBy == userID
}

func mcpCanAssign(srv *model.McpServer, botOwnerID int64) bool {
	return srv.CreatedBy == 0 || srv.CreatedBy == botOwnerID
}

func mcpServerToProto(srv *model.McpServer) *pb.McpServerInfo {
	envStr := ""
	if srv.Env != nil {
		envStr = string(srv.Env)
	}
	authConfigStr := ""
	if srv.AuthConfig != nil {
		if b, err := json.Marshal(srv.AuthConfig); err == nil {
			authConfigStr = string(b)
		}
	}
	advancedConfigStr := ""
	if srv.AdvancedConfig != nil {
		if b, err := json.Marshal(srv.AdvancedConfig); err == nil {
			advancedConfigStr = string(b)
		}
	}
	status := "active"
	if !srv.Enabled {
		status = "disabled"
	}
	return &pb.McpServerInfo{
		Id:             srv.ID,
		Name:           srv.Name,
		Description:    srv.Description,
		Transport:      srv.Transport,
		Url:            srv.URL,
		Command:        srv.Command,
		Args:           srv.Args,
		Env:            envStr,
		TimeoutMs:      timeoutFromAdvanced(srv.AdvancedConfig),
		Status:         status,
		AuthConfig:     authConfigStr,
		AdvancedConfig: advancedConfigStr,
		Enabled:        srv.Enabled,
		CreatedBy:      srv.CreatedBy,
		CreatedAt:      srv.CreatedAt.Unix(),
		UpdatedAt:      srv.UpdatedAt.Unix(),
	}
}

func timeoutFromAdvanced(cfg *model.MCPAdvancedConfig) int32 {
	if cfg != nil && cfg.Timeout > 0 {
		return int32(cfg.Timeout * 1000)
	}
	return 30000 // default 30s
}

func statusFromEnabled(enabled bool) string {
	if enabled {
		return "active"
	}
	return "disabled"
}
