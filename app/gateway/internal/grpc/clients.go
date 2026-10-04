package grpc

import (
	"log"

	"github.com/maomeng/aim/app/gateway/internal/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Clients struct {
	User          zrpc.Client
	Friend        zrpc.Client
	Conversation  zrpc.Client
	Message       zrpc.Client
	File          zrpc.Client
	Notification  zrpc.Client
	BotPlatform   zrpc.Client
	KnowledgeBase zrpc.Client
	AIBot         zrpc.Client
	LLMGateway    zrpc.Client
}

func NewClients(cfg *config.Config) *Clients {
	newClient := func(name, target string) zrpc.Client {
		c, err := zrpc.NewClient(zrpc.RpcClientConf{
			Target:   target,
			NonBlock: true,
			Timeout:  30000,
			Middlewares: zrpc.ClientMiddlewaresConf{
				Breaker: true,
			},
		})
		if err != nil {
			log.Printf("grpc client: %s unavailable (%v), using lazy connect", name, err)
		}
		return c
	}

	return &Clients{
		User:          newClient("user-service", cfg.Services.UserServiceAddr),
		Friend:        newClient("friend-service", cfg.Services.FriendServiceAddr),
		Conversation:  newClient("conversation-service", cfg.Services.ConversationServiceAddr),
		Message:       newClient("message-service", cfg.Services.MessageServiceAddr),
		File:          newClient("file-service", cfg.Services.FileServiceAddr),
		Notification:  newClient("signaling-service", cfg.Services.NotificationServiceAddr),
		BotPlatform:   newClient("bot-platform", cfg.Services.BotPlatformServiceAddr),
		KnowledgeBase: newClient("knowledge-base", cfg.Services.KnowledgeServiceAddr),
		AIBot:         newClient("ai-bot-service", cfg.Services.AIBotServiceAddr),
		LLMGateway:    newClient("llm-gateway", cfg.Services.LLMGatewayServiceAddr),
	}
}

func (c *Clients) Close() {
	clients := []zrpc.Client{
		c.User, c.Friend, c.Conversation, c.Message, c.File, c.Notification, c.BotPlatform, c.KnowledgeBase, c.AIBot, c.LLMGateway,
	}
	for _, cli := range clients {
		if cli != nil {
			_ = cli.Conn().Close()
		}
	}
}
