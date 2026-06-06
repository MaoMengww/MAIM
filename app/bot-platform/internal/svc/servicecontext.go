package svc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/bot-platform/internal/config"
	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/repo"
	"github.com/maomeng/aim/pkg/database"
	pkgjwt "github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"

	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
)

type ServiceContext struct {
	Config        config.Config
	Repo          repo.BotRepoInterface
	EncryptionKey []byte
	BotJWT        *pkgjwt.Manager
	MessageClient msgpb.MessageServiceClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	log := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, log)
	if err != nil {
		panic(fmt.Sprintf("database init failed: %v", err))
	}
	if err := db.AutoMigrate(&model.Bot{}, &model.McpServer{}, &model.BotMcpServer{}, &model.McpTool{}); err != nil {
		panic(fmt.Sprintf("auto migrate failed: %v", err))
	}

	sf, err := snowflake.NewNode(c.Snowflake.WorkerID)
	if err != nil {
		panic(fmt.Sprintf("snowflake init failed: %v", err))
	}

	r := repo.NewBotRepo(db, sf)

	// Ensure seed templates exist on startup
	ensureSeedTemplates(context.Background(), r, log)

	var encKey []byte
	if c.EncryptionKey != "" {
		encKey = []byte(c.EncryptionKey)
	}

	botJWT := pkgjwt.NewManager(c.JWT.Secret, c.JWT.ExpireSec, c.JWT.RefreshSec)

	var msgClient msgpb.MessageServiceClient
	if len(c.MessageService.Etcd.Hosts) > 0 || c.MessageService.Target != "" {
		msgClient = msgpb.NewMessageServiceClient(zrpc.MustNewClient(c.MessageService).Conn())
	}

	return &ServiceContext{
		Config:        c,
		Repo:          r,
		EncryptionKey: encKey,
		BotJWT:        botJWT,
		MessageClient: msgClient,
	}
}

func ensureSeedTemplates(ctx context.Context, r repo.BotRepoInterface, log logx.Logger) {
	bots, err := r.ListOfficialTemplates(ctx)
	if err != nil {
		log.Errorf("failed to list official templates: %v", err)
		return
	}

	hasQA := false
	hasKnowledge := false
	for _, b := range bots {
		switch b.TemplateID {
		case "qa":
			hasQA = true
		case "knowledge":
			hasKnowledge = true
		}
	}

	if !hasQA {
		createTemplateBot(ctx, r, log, "qa", "智能问答助手",
			"你是一个智能问答助手，请用中文回答用户的问题。", 0.7, 10, false)
		log.Infof("created qa template bot")
	}

	if !hasKnowledge {
		createTemplateBot(ctx, r, log, "knowledge", "知识库问答助手",
			"你是一个知识库问答助手。基于提供的知识库内容回答用户问题。如果知识库中没有相关信息，请如实告知。", 0.3, 15, true)
		log.Infof("created knowledge template bot")
	}
}

func createTemplateBot(ctx context.Context, r repo.BotRepoInterface, log logx.Logger, templateID, name, systemPrompt string, temperature float64, maxContextMessages int, enableKnowledge bool) {
	id, err := r.NextID(ctx)
	if err != nil {
		log.Errorf("create template bot %s: next id failed: %v", templateID, err)
		return
	}
	settings, _ := json.Marshal(map[string]any{
		"prompt_locale": "zh-CN",
	})

	bot := &model.Bot{
		ID:                 id,
		OwnerID:            0,
		Name:               name,
		Type:               "official",
		TemplateID:         templateID,
		Status:             "active",
		UsePlatformModel:   true,
		SystemPrompt:       systemPrompt,
		EnableKnowledge:    enableKnowledge,
		Temperature:        temperature,
		MaxContextMessages: maxContextMessages,
		ConnMode:           "ws",
		Settings:           settings,
	}

	if err := r.CreateBot(ctx, bot); err != nil {
		log.Errorf("create template bot %s: create failed: %v", templateID, err)
	}
}
