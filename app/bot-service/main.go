package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/maomeng/aim/app/bot-service/internal/config"
	"github.com/maomeng/aim/app/bot-service/internal/consumer"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/run"
	"github.com/maomeng/aim/app/bot-service/internal/server"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
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

var role = flag.String("role", "all", "service role: all, control, runtime")

var configFile = flag.String("f", "etc/bot.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	// Runtime ingress contains credentials and work content, never request logs.
	c.Middlewares.StatConf.IgnoreContentMethods = append(c.Middlewares.StatConf.IgnoreContentMethods,
		botpb.BotService_ExecutorUpdate_FullMethodName,
		botpb.BotService_HandleIncomingWebhook_FullMethodName,
	)
	if *role != "all" && *role != "control" && *role != "runtime" {
		panic("invalid -role: " + *role)
	}
	c.Role = *role
	ctx := svc.NewServiceContext(c)
	logger := logx.DefaultLogger()
	defer ctx.DeliveryPublisher.Close()

	// gRPC server — also initializes OpenTelemetry tracing via go-zero's ServiceConf.SetUp()
	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		botpb.RegisterBotServiceServer(grpcServer, server.NewBotServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(
		interceptor.UnaryErrorInterceptor(),
		interceptor.UnaryRequestIDInterceptor(),
		interceptor.UnaryUserIDInterceptor(),
	)

	var eventConsumer *kafka.Consumer
	kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
	if c.Role != "control" {
		// Kafka consumer for message.created events
		var err error
		eventConsumer, err = kafka.NewConsumer(c.Kafka, []string{consts.KafkaTopicMessageCreated}, c.Kafka.ConsumerGroup, logger)
		if err != nil {
			panic(fmt.Sprintf("kafka consumer init failed: %v", err))
		}

		// Start Kafka consumer lag monitoring
		if kafkaClient := eventConsumer.GetClient(); kafkaClient != nil {
			kafka.CollectConsumerLag(kafkaClient, c.Kafka.ConsumerGroup, []string{consts.KafkaTopicMessageCreated})
		}

		runs := run.NewService(ctx)
		runs.Start(kafkaCtx)
		handler := consumer.NewHandler(ctx.BotRepo, repo.NewConvBotRepo(ctx.DB), runs)

		// Start Kafka consumer after tracing is initialized (by MustNewServer above)
		// so ConsumerClaim spans have a valid global TracerProvider.
		go func() {
			if err := eventConsumer.Consume(kafkaCtx, handler); err != nil && kafkaCtx.Err() == nil {
				logger.Errorf("kafka consumer stopped: %v", err)
			}
		}()

	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		logger.Info("shutting down...")
		kafkaCancel()
		if eventConsumer != nil {
			eventConsumer.Close()
		}
		s.Stop()
	}()

	fmt.Printf("Starting bot-service (%s) rpc server at %s...\n", c.Role, c.ListenOn)
	s.Start()
}
