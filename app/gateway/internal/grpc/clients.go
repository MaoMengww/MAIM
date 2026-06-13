package grpc

import (
	"log"

	"github.com/maomeng/aim/app/gateway/internal/config"
	"github.com/zeromicro/go-zero/core/discov"
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
	etcdHosts := cfg.Etcd.Hosts
	if len(etcdHosts) == 0 {
		etcdHosts = []string{"localhost:2379"}
	}

	newClient := func(serviceKey string) zrpc.Client {
		conf := zrpc.RpcClientConf{
			Etcd: discov.EtcdConf{
				Hosts: etcdHosts,
				Key:   serviceKey,
			},
			NonBlock: true,
			Timeout:  30000,
			Middlewares: zrpc.ClientMiddlewaresConf{
				Breaker: true,
			},
		}
		c, err := zrpc.NewClient(conf)
		if err != nil {
			log.Printf("grpc client: %s unavailable (%v), using lazy connect", serviceKey, err)
		}
		return c
	}

	return &Clients{
		User:          newClient(cfg.Services.UserServiceKey),
		Friend:        newClient(cfg.Services.FriendServiceKey),
		Conversation:  newClient(cfg.Services.ConversationServiceKey),
		Message:       newClient(cfg.Services.MessageServiceKey),
		File:          newClient(cfg.Services.FileServiceKey),
		Notification:  newClient(cfg.Services.NotificationServiceKey),
		BotPlatform:   newClient(cfg.Services.BotPlatformServiceKey),
		KnowledgeBase: newClient(cfg.Services.KnowledgeServiceKey),
		AIBot:         newClient(cfg.Services.AIBotServiceKey),
		LLMGateway:    newClient(cfg.Services.LLMGatewayServiceKey),
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
