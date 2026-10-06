package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/consumer"
	messageservicelogic "github.com/maomeng/aim/app/message-service/internal/logic/messageservice"
	messageserviceServer "github.com/maomeng/aim/app/message-service/internal/server/messageservice"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/kafka"

	"github.com/maomeng/aim/pkg/logx"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/message.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	ctx := svc.NewServiceContext(c)
	ctx.SendSystemMessage = func(callCtx context.Context, req *message.SendSystemMessageReq) error {
		_, err := messageservicelogic.NewSendSystemMessageLogic(callCtx, ctx).SendSystemMessage(req)
		return err
	}
	defer ctx.Close()

	// Stop collection before ServiceContext closes its database.
	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	retentionDone := make(chan struct{})
	go func() {
		defer close(retentionDone)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			cutoff := time.Now().AddDate(0, 0, -ctx.Config.Message.InboxRetentionDays)
			if removed, err := ctx.InboxRepo.Prune(retentionCtx, cutoff); err != nil {
				if retentionCtx.Err() == nil {
					ctx.Logger.WithContext(retentionCtx).Errorw("inbox_retention_failed", logx.Err(err))
				}
			} else if removed > 0 {
				ctx.Logger.WithContext(retentionCtx).Infow("inbox_retention_completed", logx.Field("removed", removed))
			}
			select {
			case <-retentionCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { retentionCancel(); <-retentionDone }()

	dlqProducer, err := kafka.NewProducer(c.Kafka, consts.KafkaTopicMessageCreatedDLQ, ctx.Logger)
	if err != nil {
		ctx.Logger.Errorf("kafka dlq producer init failed: %v", err)
	}
	if dlqProducer != nil {
		defer dlqProducer.Close()
	}

	fanout := consumer.NewFanout(ctx.ConversationRepo, ctx.DeliveryPublisher)
	inboxWriter := consumer.NewInboxWriter(ctx.InboxRepo, fanout, ctx.Logger, c.Kafka.MaxRetry, dlqProducer)
	inboxConsumer, err := kafka.NewConsumer(c.Kafka, []string{consts.KafkaTopicMessageCreated}, c.Kafka.ConsumerGroup+"-inbox", ctx.Logger)
	if err != nil {
		panic(fmt.Sprintf("kafka inbox consumer: %v", err))
	}
	defer inboxConsumer.Close()

	searchIndexer := consumer.NewSearchIndexer(ctx.ESClient, ctx.Logger, c.Kafka.MaxRetry)
	searchConsumer, err := kafka.NewConsumer(c.Kafka, []string{consts.KafkaTopicMessageCreated}, c.Kafka.ConsumerGroup+"-search", ctx.Logger)
	if err != nil {
		panic(fmt.Sprintf("kafka search consumer: %v", err))
	}
	defer searchConsumer.Close()

	inboxCtx, inboxCancel := context.WithCancel(context.Background())
	defer inboxCancel()
	go func() {
		if err := inboxConsumer.Consume(inboxCtx, inboxWriter); err != nil {
			ctx.Logger.WithContext(inboxCtx).Errorf("kafka inbox consume error: %v", err)
		}
	}()

	searchCtx, searchCancel := context.WithCancel(context.Background())
	defer searchCancel()
	go func() {
		if err := searchConsumer.Consume(searchCtx, searchIndexer); err != nil {
			ctx.Logger.WithContext(searchCtx).Errorf("kafka search consume error: %v", err)
		}
	}()

	// Start OutboxDispatcher for reliable Kafka event delivery
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	defer outboxCancel()
	go ctx.OutboxDispatcher.Run(outboxCtx)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		message.RegisterMessageServiceServer(grpcServer, messageserviceServer.NewMessageServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryUserIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
