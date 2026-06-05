package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/maomeng/aim/app/conversation-service/internal/consumer"
	"github.com/maomeng/aim/app/conversation-service/internal/config"
	conversationserviceServer "github.com/maomeng/aim/app/conversation-service/internal/server"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/kafka"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/conversation.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	configcenter.InitConfigCenter(c.Name, c.Etcd.Hosts, &c)
	ctx := svc.NewServiceContext(c)

	// Start Kafka consumer for message.created events
	msgConsumer, err := kafka.NewConsumer(c.Kafka, []string{"message.created"}, c.Kafka.ConsumerGroup, ctx.Logger)
	if err != nil {
		ctx.Logger.Errorf("init msg consumer failed: %v", err)
	} else {
		kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
		go func() {
			handler := &kafka.MessageHandler{
				OnMessage: consumer.NewMessageHandler(ctx),
				Logger:    ctx.Logger,
			}
			if err := msgConsumer.Consume(kafkaCtx, handler); err != nil {
				ctx.Logger.Errorf("msg consumer stopped: %v", err)
			}
		}()
		defer kafkaCancel()
	}

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		conversation.RegisterConversationServiceServer(grpcServer, conversationserviceServer.NewConversationServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryUserIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
