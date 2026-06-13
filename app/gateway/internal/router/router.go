package router

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/config"
	"github.com/maomeng/aim/app/gateway/internal/grpc"
	"github.com/maomeng/aim/app/gateway/internal/handler"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/metrics"
	"github.com/redis/go-redis/v9"
)

func New(
	cfg *config.Config,
	clients *grpc.Clients,
	jwtMgr *jwt.Manager,
	rdb *redis.Client,
	logger logx.Logger,

) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.ContextWithFallback = true

	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())
	r.Use(middleware.Tracing(cfg.Name))
	r.Use(middleware.Logger(logger))
	r.Use(middleware.IDStringToNumber())
	r.Use(middleware.PrometheusMetrics())

	authH := handler.NewAuthHandler(clients.User.Conn())
	userH := handler.NewUserHandler(clients.User.Conn())
	friendH := handler.NewFriendHandler(clients.Friend.Conn())
	convH := handler.NewConversationHandler(clients.Conversation, clients.File.Conn())
	msgH := handler.NewMessageHandler(clients.Message, clients.File.Conn())
	fileH := handler.NewFileHandler(clients.File.Conn(), clients.User.Conn())
	broadcastH := handler.NewBroadcastHandler(clients.Message)
	notifH := handler.NewNotificationHandler(clients.Notification.Conn())
	botH := handler.NewBotHandler(clients.BotPlatform.Conn(), clients.Conversation.Conn(), clients.AIBot.Conn(), clients.File.Conn())
	kbH := handler.NewKnowledgeHandler(clients.KnowledgeBase.Conn())
	convToolH := handler.NewConversationToolHandler(clients.AIBot.Conn())
	var modelH *handler.ModelHandler
	if clients.LLMGateway != nil {
		modelH = handler.NewModelHandler(clients.LLMGateway.Conn())
	}

	auth := middleware.AuthRequired(jwtMgr)
	rateLimit := middleware.RateLimit(rdb, cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.MessagePerSecond)

	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/metrics", gin.WrapH(metrics.Handler()))

	api := r.Group("/api/v1")

	// Public routes
	api.POST("/auth/register", authH.Register)
	api.POST("/auth/login", authH.Login)
	api.POST("/auth/refresh", authH.Refresh)
	api.POST("/auth/oauth/:provider", authH.OAuthLogin)

	// Protected routes
	protected := api.Group("")
	protected.Use(auth)
	protected.Use(rateLimit)

	// Auth
	protected.POST("/auth/logout", authH.Logout)
	protected.GET("/auth/sessions", authH.GetSessions)
	protected.DELETE("/auth/sessions/:id", authH.RevokeSession)

	// User
	protected.GET("/users/me", userH.GetProfile)
	protected.PUT("/users/me", userH.UpdateProfile)
	protected.PUT("/users/me/password", userH.UpdatePassword)
	protected.PUT("/users/me/phone", userH.BindPhone)
	protected.PUT("/users/me/email", userH.BindEmail)
	protected.POST("/users/me/avatar", fileH.UploadAvatar)
	protected.GET("/users/:id", userH.GetUserInfo)
	protected.POST("/users/batch", userH.BatchGetUserInfo)
	protected.POST("/users/search", userH.SearchUsers)
	protected.POST("/users/batch_status", userH.BatchGetStatus)
	protected.GET("/users/me/settings", userH.GetSettings)
	protected.PUT("/users/me/settings", userH.UpdateSettings)
	protected.POST("/users/me/recharge", userH.Recharge)

	// Friend
	friends := protected.Group("/friends")
	friends.POST("/requests", friendH.SendRequest)
	friends.POST("/requests/:id/accept", friendH.AcceptRequest)
	friends.POST("/requests/:id/reject", friendH.RejectRequest)
	friends.DELETE("/requests/:id", friendH.CancelRequest)
	friends.GET("/requests/pending", friendH.ListPendingRequests)
	friends.GET("/requests/sent", friendH.ListSentRequests)
	friends.GET("", friendH.ListFriends)
	friends.DELETE("/:user_id", friendH.DeleteFriend)
	friends.PUT("/:user_id/remark", friendH.SetRemark)
	friends.PUT("/:user_id/group", friendH.SetGroup)
	friends.POST("/groups", friendH.CreateGroup)
	friends.PUT("/groups/:id", friendH.RenameGroup)
	friends.DELETE("/groups/:id", friendH.DeleteGroup)
	friends.GET("/groups", friendH.ListGroups)
	friends.POST("/:user_id/block", friendH.BlockUser)
	friends.DELETE("/:user_id/block", friendH.UnblockUser)
	friends.GET("/blacklist", friendH.ListBlacklist)

	// Conversation
	convs := protected.Group("/convs")
	convs.POST("", convH.CreateConversation)
	convs.GET("/:id", convH.GetConversation)
	convs.DELETE("/:id", convH.DeleteConversation)
	convs.PUT("/:id/info", convH.UpdateConversation)
	convs.POST("/:id/avatar", convH.UploadConvAvatar)
	convs.GET("/:id/members", convH.GetMembers)
	convs.POST("/:id/members/invite", convH.AddMembers)
	convs.POST("/:id/members/kick", convH.RemoveMembers)
	convs.PUT("/:id/members/:uid/role", convH.UpdateMember)
	convs.PUT("/:id/members/:uid/mute", convH.MuteMember)
	convs.DELETE("/:id/members/:uid/mute", convH.UnmuteMember)
	convs.POST("/:id/mute_all", convH.MuteAll)
	convs.DELETE("/:id/mute_all", convH.UnmuteAll)
	convs.PUT("/:id/announcement", convH.SetAnnouncement)
	convs.DELETE("/:id/announcement", convH.DeleteAnnouncement)
	convs.POST("/:id/transfer", convH.TransferOwner)
	convs.PUT("/:id/settings", convH.UpdateSettings)
	convs.GET("/:id/settings", convH.GetSettings)
	convs.GET("", convH.ListConversations)
	convs.PUT("/:id/read", convH.MarkAsRead)
	convs.GET("/:id/read_status/:message_id", convH.GetReadStatus)

	// Message
	msgs := protected.Group("/messages")
	msgs.POST("/send", msgH.SendMessage)
	msgs.GET("/:id/sync", msgH.SyncMessages)
	msgs.GET("/:id", msgH.GetMessageByID)
	msgs.POST("/:id/recall", msgH.RecallMessage)
	msgs.PUT("/:id", msgH.EditMessage)
	msgs.DELETE("/:id", msgH.DeleteMessage)
	msgs.POST("/:id/reply", msgH.ReplyMessage)
	msgs.GET("/search", msgH.SearchMessages)
	msgs.GET("/:id/around/:seq", msgH.GetAroundSeq)

	// File
	files := protected.Group("/files")
	files.POST("/upload_url", fileH.GetUploadURL)
	files.POST("/confirm", fileH.ConfirmUpload)
	files.GET("/:id/download", fileH.GetDownloadURL)
	files.DELETE("/:id", fileH.DeleteFile)
	files.GET("/:id/info", fileH.GetFileInfo)

	// Broadcast
	broadcasts := protected.Group("/broadcasts")
	broadcasts.POST("", broadcastH.CreateBroadcast)
	broadcasts.GET("", broadcastH.ListBroadcasts)
	broadcasts.GET("/:id", broadcastH.GetBroadcast)
	broadcasts.GET("/my", broadcastH.GetMyBroadcasts)

	// Notification
	notifs := protected.Group("/notifications")
	notifs.GET("", notifH.ListNotifications)

	notifs.POST("/:id/read", notifH.MarkRead)
	notifs.POST("/read_all", notifH.MarkAllRead)
	notifs.DELETE("/:id", notifH.DeleteNotification)

	// Bot Management
	bots := protected.Group("/bots")
	bots.POST("", botH.CreateBot)
	bots.GET("", botH.ListBots)
	bots.GET("/:id", botH.GetBot)
	bots.PUT("/:id", botH.UpdateBot)
	bots.POST("/:id/avatar", botH.UploadBotAvatar)
	bots.DELETE("/:id", botH.DeleteBot)
	bots.POST("/:id/secret/rotate", botH.RotateSecret)
	bots.POST("/:id/token", botH.IssueToken)
	bots.POST("/token/validate", botH.ValidateToken)

	// AI Bot Streaming Chat & Memory
	bots.GET("/:id/chat/stream", botH.StreamChat)
	bots.GET("/:id/memory", botH.GetUserMemories)
	bots.DELETE("/:id/memory", botH.ClearUserMemories)
	bots.DELETE("/:id/memory/:memory_id", botH.ForgetMemory)

	// Global MCP Server Management
	mcp := protected.Group("/mcp-servers")
	mcp.POST("", botH.CreateMcpServer)
	mcp.GET("", botH.ListMcpServers)
	mcp.GET("/:id", botH.GetMcpServer)
	mcp.PUT("/:id", botH.UpdateMcpServer)
	mcp.DELETE("/:id", botH.DeleteMcpServer)
	mcp.POST("/:id/discover", botH.DiscoverMcpTools)
	mcp.GET("/:id/tools", botH.ListMcpTools)

	// Bot MCP Assignment
	bots.POST("/:id/mcp-servers", botH.AssignMcpToBot)
	bots.DELETE("/:id/mcp-servers/:mcp_id", botH.UnassignMcpFromBot)
	bots.GET("/:id/mcp-servers", botH.ListBotMcpServers)
	bots.PUT("/:id/mcp-servers/:mcp_id", botH.UpdateBotMcpServer)

	// Bot in Conversation
	convs.POST("/:id/bots", botH.AddBotToConv)
	convs.DELETE("/:id/bots/:bot_id", botH.RemoveBotFromConv)
	convs.PUT("/:id/bots/:bot_id", botH.UpdateBotInConv)
	convs.GET("/:id/bots", botH.ListConvBots)

	// Knowledge Base
	kb := protected.Group("/knowledge")
	kb.POST("/bases", kbH.CreateKB)
	kb.GET("/bases", kbH.ListKBs)
	kb.GET("/bases/:id", kbH.GetKB)
	kb.PUT("/bases/:id", kbH.UpdateKB)
	kb.DELETE("/bases/:id", kbH.DeleteKB)
	kb.POST("/bases/:id/documents", kbH.UploadDocument)
	kb.GET("/bases/:id/documents", kbH.ListDocuments)
	kb.GET("/documents/:id", kbH.GetDocument)
	kb.DELETE("/documents/:id", kbH.DeleteDocument)
	kb.POST("/documents/:id/retry", kbH.RetryDocument)
	kb.GET("/documents/:id/content", kbH.GetDocumentContent)
	kb.GET("/documents/:id/chunks", kbH.ListChunks)
	kb.POST("/bases/:id/search", kbH.Search)
	kb.GET("/bases/:id/bindings", kbH.ListKBBindings)

	// Wiki Pages
	kb.GET("/bases/:id/wiki/index", kbH.WikiReadIndex)
	kb.GET("/bases/:id/wiki/pages", kbH.WikiListPages)
	kb.GET("/bases/:id/wiki/pages/*slug", kbH.WikiReadPage)
	kb.GET("/bases/:id/wiki/search", kbH.WikiSearch)
	kb.PUT("/bases/:id/wiki/pages/*slug", kbH.WikiUpdatePage)
	kb.DELETE("/bases/:id/wiki/pages/*slug", kbH.WikiDeletePage)
	kb.GET("/bases/:id/wiki/issues", kbH.WikiListIssues)
	kb.POST("/bases/:id/wiki/refresh", kbH.WikiRefresh)
	kb.POST("/bases/:id/wiki/maintenance", kbH.WikiRunMaintenance)
	kb.GET("/bases/:id/wiki/graph", kbH.WikiGraph)

	// Wiki — New tools
	kb.GET("/bases/:id/wiki/source/:docId", kbH.WikiReadSourceDoc)
	kb.PUT("/bases/:id/wiki/replace-text/:slug", kbH.WikiReplaceText)
	kb.PUT("/bases/:id/wiki/rename-page/:slug", kbH.WikiRenamePage)
	kb.POST("/bases/:id/wiki/issues", kbH.WikiFlagIssue)
	kb.PUT("/bases/:id/wiki/issues/:issueId", kbH.WikiUpdateIssue)
	kb.POST("/bases/:id/wiki/batch-upload", kbH.WikiBatchUploadDocuments)

	protected.POST("/bots/:id/knowledge", kbH.BindToBot)
	protected.DELETE("/bots/:id/knowledge/:kid", kbH.UnbindFromBot)
	protected.GET("/bots/:id/knowledge", kbH.ListBotBindings)

	protected.POST("/convs/:id/knowledge", kbH.BindToConv)
	protected.DELETE("/convs/:id/knowledge/:kid", kbH.UnbindFromConv)
	protected.GET("/convs/:id/knowledge", kbH.ListConvBindings)

	// Conversation Tools (summarize, todos, reply candidates)
	convs.POST("/:id/summarize", convToolH.Summarize)
	convs.GET("/:id/summaries", convToolH.GetSummaries)
	convs.POST("/:id/todos", convToolH.CreateTodo)
	convs.PUT("/:id/todos/:todoId", convToolH.UpdateTodo)
	convs.DELETE("/:id/todos/:todoId", convToolH.DeleteTodo)
	convs.POST("/:id/reply-candidates", convToolH.ReplyCandidates)

	// Message translation
	msgs.POST("/:id/translate", convToolH.Translate)

	// Model Management
	if modelH != nil {
		protected.GET("/models", modelH.ListModels)
		protected.POST("/models", modelH.CreateModel)
		protected.PUT("/models/:id", modelH.UpdateModel)
		protected.DELETE("/models/:id", modelH.DeleteModel)
		protected.GET("/models/billing/stats", modelH.GetBillingStats)
		protected.GET("/models/billing/records", modelH.ListBillingRecords)
	}
	// Webhook (no auth, HMAC signature — bot resolved by secret)
	r.POST("/api/v1/webhook", botH.Webhook)

	return r
}
