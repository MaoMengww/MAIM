package bot

import (
	"context"
	"encoding/json"
	stderrors "errors"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

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
	if err := validateCaller(l.ctx, in.UserId); err != nil {
		return nil, err
	}
	if err := validateUserOwnership(l.ctx, in.OwnerType, in.OwnerId); err != nil {
		return nil, err
	}
	if in.Name == "" {
		return nil, ErrBotInvalid
	}
	for _, data := range []string{in.Env, in.AuthConfig, in.AdvancedConfig} {
		if data != "" && model.ValidateEntityJSON([]byte(data)) != nil {
			return nil, ErrBotInvalid
		}
	}
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	transport := in.Transport
	if transport == "" {
		transport = "sse"
	}
	srv := &model.McpServer{
		ID: id, OwnerType: in.OwnerType, OwnerID: in.OwnerId, CreatedBy: &in.UserId,
		Name: in.Name, Description: in.Description, Transport: transport, URL: in.Url,
		Command: in.Command, Args: in.Args, Enabled: true,
	}
	if in.Env != "" {
		srv.Env = []byte(in.Env)
	}
	if in.AuthConfig != "" {
		var auth model.MCPAuthConfig
		if err := json.Unmarshal([]byte(in.AuthConfig), &auth); err != nil {
			return nil, ErrBotInvalid
		}
		srv.AuthConfig = &auth
	}
	srv.AdvancedConfig = &model.MCPAdvancedConfig{Timeout: 30, RetryCount: 3, RetryDelay: 1}
	if in.AdvancedConfig != "" {
		if err := json.Unmarshal([]byte(in.AdvancedConfig), srv.AdvancedConfig); err != nil {
			return nil, ErrBotInvalid
		}
		if srv.AdvancedConfig.Timeout <= 0 {
			srv.AdvancedConfig.Timeout = 30
		}
		if srv.AdvancedConfig.RetryCount <= 0 {
			srv.AdvancedConfig.RetryCount = 3
		}
		if srv.AdvancedConfig.RetryDelay <= 0 {
			srv.AdvancedConfig.RetryDelay = 1
		}
	}
	if err := l.svcCtx.Repo.CreateMcpServer(l.ctx, srv); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "create mcp server failed", err)
	}
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
	if err := validateCaller(l.ctx, in.UserId, in.Id); err != nil {
		return nil, err
	}
	for _, data := range []string{in.Env, in.AuthConfig, in.AdvancedConfig} {
		if data != "" && model.ValidateEntityJSON([]byte(data)) != nil {
			return nil, ErrBotInvalid
		}
	}
	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	if err != nil {
		return nil, mcpGetError(err)
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
		var auth model.MCPAuthConfig
		if err := json.Unmarshal([]byte(in.AuthConfig), &auth); err != nil {
			return nil, ErrBotInvalid
		}
		updates["auth_config"] = &auth
	}
	if in.AdvancedConfig != "" {
		var advanced model.MCPAdvancedConfig
		if err := json.Unmarshal([]byte(in.AdvancedConfig), &advanced); err != nil {
			return nil, ErrBotInvalid
		}
		updates["advanced_config"] = &advanced
	}
	if in.Enabled || in.Status == "active" {
		updates["enabled"] = true
	}
	if in.Status == "disabled" {
		updates["enabled"] = false
	}
	if len(updates) > 0 {
		if err := l.svcCtx.Repo.UpdateMcpServer(l.ctx, in.Id, updates); err != nil {
			return nil, errors.Wrap(errors.CodeDBError, "update mcp server failed", err)
		}
	}
	srv, err = l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	if err != nil {
		return nil, mcpGetError(err)
	}
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
	if err := validateCaller(l.ctx, in.UserId, in.Id); err != nil {
		return nil, err
	}
	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	if err != nil {
		return nil, mcpGetError(err)
	}
	if !mcpCanModify(srv, in.UserId) {
		return nil, ErrBotForbidden
	}
	if err := l.svcCtx.Repo.DeleteMcpServer(l.ctx, in.Id); err != nil {
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
	if err := validateCaller(l.ctx, in.UserId, in.Id); err != nil {
		return nil, err
	}
	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.Id)
	if err != nil {
		return nil, mcpGetError(err)
	}
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
	if err := validateCaller(l.ctx, in.UserId); err != nil {
		return nil, err
	}
	page, pageSize := int32(1), int32(20)
	if in.Pagination != nil {
		if in.Pagination.Page > 0 {
			page = in.Pagination.Page
		}
		if in.Pagination.PageSize > 0 {
			pageSize = min(in.Pagination.PageSize, 100)
		}
	}
	servers, total, err := l.svcCtx.Repo.ListUserMcpServers(l.ctx, in.UserId, in.Status, int((page-1)*pageSize), int(pageSize))
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list mcp servers failed", err)
	}
	items := make([]*pb.McpServerInfo, 0, len(servers))
	for i := range servers {
		items = append(items, mcpServerToProto(&servers[i]))
	}
	totalPages := int32((total + int64(pageSize) - 1) / int64(pageSize))
	return &pb.ListMcpServersResp{Servers: items, Pagination: &common.PaginationResp{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}}, nil
}

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
	if err := validateCaller(l.ctx, in.UserId, in.BotId, in.McpServerId); err != nil {
		return nil, err
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		return nil, ErrBotNotFound
	}
	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}
	srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, in.McpServerId)
	if err != nil {
		return nil, mcpGetError(err)
	}
	if !mcpCanAssign(srv, *bot.OwnerID) {
		return nil, ErrBotForbidden
	}
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	assoc := &model.BotMcpServer{ID: id, BotID: in.BotId, McpServerID: in.McpServerId, Enabled: true}
	if err := l.svcCtx.Repo.AssignMcpToBot(l.ctx, assoc); err != nil {
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
	if err := validateCaller(l.ctx, in.UserId, in.BotId, in.McpServerId); err != nil {
		return nil, err
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		return nil, ErrBotNotFound
	}
	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}
	if err := l.svcCtx.Repo.UnassignMcpFromBot(l.ctx, in.BotId, in.McpServerId); err != nil {
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
	caller, _ := l.ctx.Value(interceptor.ContextKeyUserID).(string)
	if err := validateCaller(l.ctx, caller, in.BotId); err != nil {
		return nil, err
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		return nil, ErrBotNotFound
	}
	if !canWrite(bot, caller) && bot.OwnerType != "platform" {
		return nil, ErrBotForbidden
	}
	assocs, err := l.svcCtx.Repo.ListBotMcpServers(l.ctx, in.BotId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list bot mcp servers failed", err)
	}
	items := make([]*pb.BotMcpServerInfo, 0, len(assocs))
	for _, assoc := range assocs {
		srv, err := l.svcCtx.Repo.GetMcpServer(l.ctx, assoc.McpServerID)
		if err != nil {
			return nil, mcpGetError(err)
		}
		if !mcpCanView(srv, caller) {
			return nil, ErrBotForbidden
		}
		items = append(items, &pb.BotMcpServerInfo{Id: assoc.ID, McpServerId: srv.ID, Name: srv.Name, Description: srv.Description, Transport: srv.Transport, Url: srv.URL, TimeoutMs: timeoutFromAdvanced(srv.AdvancedConfig), Status: statusFromEnabled(srv.Enabled), Enabled: assoc.Enabled})
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
	if err := validateCaller(l.ctx, in.UserId, in.BotId, in.McpServerId); err != nil {
		return nil, err
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		return nil, ErrBotNotFound
	}
	if !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}
	if err := l.svcCtx.Repo.UpdateBotMcpServer(l.ctx, in.BotId, in.McpServerId, in.Enabled); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "update bot mcp server failed", err)
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}

func mcpGetError(err error) error {
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New(errors.CodeNotFound, "mcp server not found")
	}
	return errors.Wrap(errors.CodeDBError, "get mcp server failed", err)
}
func mcpCanView(srv *model.McpServer, userID string) bool {
	return srv != nil && ((srv.OwnerType == "platform" && srv.OwnerID == nil) ||
		(srv.OwnerType == "user" && srv.OwnerID != nil && *srv.OwnerID == userID && identity.Validate(userID) == nil))
}
func mcpCanModify(srv *model.McpServer, userID string) bool {
	return srv != nil && srv.OwnerType == "user" && srv.OwnerID != nil && *srv.OwnerID == userID && identity.Validate(userID) == nil
}
func mcpCanAssign(srv *model.McpServer, botOwnerID string) bool { return mcpCanView(srv, botOwnerID) }
func mcpServerToProto(srv *model.McpServer) *pb.McpServerInfo {
	env, auth, advanced := string(srv.Env), "", ""
	if srv.AuthConfig != nil {
		data, _ := json.Marshal(srv.AuthConfig)
		auth = string(data)
	}
	if srv.AdvancedConfig != nil {
		data, _ := json.Marshal(srv.AdvancedConfig)
		advanced = string(data)
	}
	return &pb.McpServerInfo{
		Id: srv.ID, OwnerType: srv.OwnerType, OwnerId: srv.OwnerID, CreatedBy: srv.CreatedBy,
		Name: srv.Name, Description: srv.Description, Transport: srv.Transport, Url: srv.URL,
		Command: srv.Command, Args: srv.Args, Env: env, AuthConfig: auth, AdvancedConfig: advanced,
		TimeoutMs: timeoutFromAdvanced(srv.AdvancedConfig), Status: statusFromEnabled(srv.Enabled), Enabled: srv.Enabled,
		CreatedAt: srv.CreatedAt.Unix(), UpdatedAt: srv.UpdatedAt.Unix(),
	}
}
func timeoutFromAdvanced(cfg *model.MCPAdvancedConfig) int32 {
	if cfg != nil && cfg.Timeout > 0 {
		return int32(cfg.Timeout * 1000)
	}
	return 30000
}
func statusFromEnabled(enabled bool) string {
	if enabled {
		return "active"
	}
	return "disabled"
}
