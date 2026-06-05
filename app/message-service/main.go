package main

import (
	"context"
	"flag"
	"fmt"

	"time"
	"github.com/IBM/sarama"
	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/consumer"
	"github.com/maomeng/aim/app/message-service/internal/model"
	messageserviceServer "github.com/maomeng/aim/app/message-service/internal/server/messageservice"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/kafka"

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
	conf.MustLoad(*configFile, &c)
	configcenter.InitConfigCenter(c.Name, c.Etcd.Hosts, &c)
	ctx := svc.NewServiceContext(c)

	kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
	defer kafkaCancel()

	saramaCfg := sarama.NewConfig()
	saramaCfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	saramaCfg.Consumer.IsolationLevel = sarama.ReadCommitted

	inboxGroup, err := sarama.NewConsumerGroup(c.Kafka.Brokers, c.Kafka.ConsumerGroup+"-inbox", saramaCfg)
	if err != nil {
		panic(fmt.Sprintf("kafka inbox consumer group: %v", err))
	}
	defer inboxGroup.Close()

	searchGroup, err := sarama.NewConsumerGroup(c.Kafka.Brokers, c.Kafka.ConsumerGroup+"-search", saramaCfg)
	if err != nil {
		panic(fmt.Sprintf("kafka search consumer group: %v", err))
	}
	defer searchGroup.Close()

	dlqProducer, err := kafka.NewProducer(c.Kafka, consts.KafkaTopicMessageCreatedDLQ, ctx.Logger)
	if err != nil {
		ctx.Logger.Errorf("kafka dlq producer init failed: %v", err)
	}

	inboxWriter := consumer.NewInboxWriter(ctx.InboxRepo, ctx.ConvClient, ctx.Logger, c.Kafka.MaxRetry, dlqProducer)
	go func() {
		for {
			select {
			case <-kafkaCtx.Done():
				return
			default:
				if err := inboxGroup.Consume(kafkaCtx, []string{consts.KafkaTopicMessageCreated}, inboxWriter); err != nil {
					ctx.Logger.WithContext(kafkaCtx).Errorf("kafka inbox consume error: %v", err)
				}
				if kafkaCtx.Err() != nil {
					return
				}
			}
		}
	}()

	searchIndexer := consumer.NewSearchIndexer(ctx.ESClient, ctx.Logger, c.Kafka.MaxRetry)
	go func() {
		for {
			select {
			case <-kafkaCtx.Done():
				return
			default:
				if err := searchGroup.Consume(kafkaCtx, []string{
					consts.KafkaTopicMessageCreated,
					consts.KafkaTopicMessageEdited,
					consts.KafkaTopicMessageRecalled,
					consts.KafkaTopicMessageDeleted,
				}, searchIndexer); err != nil {
					ctx.Logger.WithContext(kafkaCtx).Errorf("kafka search consume error: %v", err)
				}
				if kafkaCtx.Err() != nil {
					return
				}
			}
		}
	}()

	// 后台重试 failed_events 中的 Kafka 发送失败事件
	go func() {
		for {
			select {
			case <-kafkaCtx.Done():
				return
			case <-time.After(30 * time.Second):
				var events []model.FailedEvent
				if err := ctx.DB.WithContext(context.Background()).Where("retry_count < 3").Order("created_at ASC").Limit(100).Find(&events).Error; err != nil {
					continue
				}
				for _, e := range events {
					if err := ctx.MessageCreatedProducer.Send(context.Background(), e.Key, e.Payload); err != nil {
						ctx.DB.Model(&e).Update("retry_count", e.RetryCount+1).Update("last_error", err.Error())
						continue
					}
					ctx.DB.Delete(&e)
				}
			}
		}
	}()

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		message.RegisterMessageServiceServer(grpcServer, messageserviceServer.NewMessageServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
