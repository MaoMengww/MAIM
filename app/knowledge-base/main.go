package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/eventpush"
	"github.com/maomeng/aim/app/knowledge-base/internal/handler"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/embedder"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/milvus"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/reranker"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	"github.com/maomeng/aim/app/knowledge-base/internal/svc"
	pb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/pb/realtimeevent"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/knowledge-base.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	configcenter.InitConfigCenter(c.Name, c.Etcd.Hosts, &c)
	ctx := svc.NewServiceContext(c)
	logger := logx.DefaultLogger()

	var (
		embed    domain.Embedder
		vecStore domain.VectorStore
		rank     domain.Reranker
	)
	if c.LLMGateway.Etcd.Key != "" || c.LLMGateway.Target != "" {
		embed = embedder.NewLLMGatewayEmbedder(ctx.LLMGatewayClient)
		rank = reranker.NewLLMGatewayReranker(ctx.LLMGatewayClient)
	}

	if c.Milvus.Address != "" {
		vs, err := milvus.NewMilvusStore(c.Milvus)
		if err != nil {
			panic(fmt.Sprintf("init milvus failed: %v", err))
		}
		vecStore = vs
	}

	var progressPusher eventpush.Pusher = eventpush.NoopPusher{}
	if c.RealtimeEvent.Etcd.Key != "" || c.RealtimeEvent.Target != "" {
		realtimeClient := zrpc.MustNewClient(c.RealtimeEvent)
		progressPusher = eventpush.New(realtimeevent.NewRealtimeEventServiceClient(realtimeClient.Conn()), "knowledge-base")
	}

	// Wire pusher to scheduler for maintenance completion events
	if ctx.MaintenanceScheduler != nil {
		ctx.MaintenanceScheduler.WithPusher(progressPusher)
	}

	ingestPipe := &pipeline.IngestPipeline{
		Parser:           ctx.Parser,
		Chunker:          ctx.Chunker,
		Embedder:         embed,
		VectorStore:      vecStore,
		FileStore:        ctx.FileStore,
		DocRepo:          ctx.DocRepo,
		KBRepo:           ctx.KBRepo,
		Snowflake:        ctx.Snowflake,
		LLMGatewayClient: ctx.LLMGatewayClient,
		RetryLimit:       c.RetryLimit,
		MaxFileSize:      c.MaxFileSize,
		Logger:           logger,
	}

	ingestPipe.Progress = func(progressCtx context.Context, doc *domain.Document, evt event.RealtimeEvent) {
		kb, err := ctx.KBRepo.Get(progressCtx, doc.KBID)
		if err != nil {
			logger.Errorf("get kb for progress event failed: kb=%d err=%v", doc.KBID, err)
			return
		}
		evt.KBID = doc.KBID
		evt.DocID = doc.ID
		if err := progressPusher.PushToUser(progressCtx, kb.OwnerID, evt); err != nil {
			logger.Errorf("push knowledge progress event failed: doc=%d err=%v", doc.ID, err)
		}
	}

	// Wire wiki pipeline progress events
	ctx.WikiIngestPipe.Progress = func(progressCtx context.Context, docID int64, evt event.RealtimeEvent) {
		doc, err := ctx.DocRepo.Get(progressCtx, docID)
		if err != nil {
			logger.Errorf("get doc for wiki progress event failed: doc=%d err=%v", docID, err)
			return
		}
		kb, err := ctx.KBRepo.Get(progressCtx, doc.KBID)
		if err != nil {
			logger.Errorf("get kb for wiki progress event failed: kb=%d err=%v", doc.KBID, err)
			return
		}
		evt.KBID = doc.KBID
		evt.DocID = doc.ID
		if err := progressPusher.PushToUser(progressCtx, kb.OwnerID, evt); err != nil {
			logger.Errorf("push wiki progress event failed: doc=%d err=%v", docID, err)
		}
	}

	retrievePipe := &pipeline.RetrievePipeline{
		KBRepo:           ctx.KBRepo,
		Embedder:         embed,
		VectorStore:      vecStore,
		Reranker:         rank,
		Logger:           logger,
		EmbeddingModelID: 15,
	}

	wikiH := &handler.WikiHandler{
		WikiRepo:      ctx.WikiRepo,
		DocRepo:       ctx.DocRepo,
		FileStore:     ctx.FileStore,
		KBRepo:        ctx.KBRepo,
		Snowflake:     ctx.Snowflake,
		WikiPipe:      ctx.WikiIngestPipe,
		WikiSearch:    ctx.WikiSearchPipe,
		WikiLint:      ctx.WikiLintPipe,
		WikiMaintain:  ctx.WikiMaintain,
		WikiAgent:     ctx.WikiAgent,
		WikiEinoAgent: ctx.WikiEinoAgent,
		Neo4jStore:    ctx.Neo4jStore,
		Logger:        logger,
		LogWriter:     ctx.LogWriter,
		Pusher:        progressPusher,
	}

	h := &handler.KnowledgeBaseHandler{
		KBRepo:               ctx.KBRepo,
		DocRepo:              ctx.DocRepo,
		FileStore:            ctx.FileStore,
		VectorStore:          vecStore,
		Producer:             ctx.Producer,
		IngestPipeline:       ingestPipe,
		RetrievePipe:         retrievePipe,
		Snowflake:            ctx.Snowflake,
		Logger:               logger,
		WikiHandler:          wikiH,
		LLMGateway:           ctx.LLMGatewayClient,
		MaintenanceScheduler: ctx.MaintenanceScheduler,
	}

	// 启动自动维护调度器，加载所有启用了维护的 wiki 知识库
	if ctx.MaintenanceScheduler != nil {
		ctx.MaintenanceScheduler.Start()
		offset := 0
		limit := 100
		for {
			kbs, err := ctx.KBRepo.ListByMode(context.Background(), "wiki", offset, limit)
			if err != nil || len(kbs) == 0 {
				break
			}
			for i := range kbs {
				ctx.MaintenanceScheduler.ScheduleKB(&kbs[i])
			}
			if len(kbs) < limit {
				break
			}
			offset += limit
		}
		logger.Infof("maintenance scheduler started")
	}

	if len(c.Kafka.Brokers) > 0 {
		kafkaConsumer, err := kafka.NewConsumer(c.Kafka, []string{"document.uploaded"}, c.Kafka.ConsumerGroup, logger)
		if err != nil {
			panic(fmt.Sprintf("init kafka consumer failed: %v", err))
		}

		docHandler := &handler.DocumentUploadedHandler{
			DocRepo:            ctx.DocRepo,
			KBRepo:             ctx.KBRepo,
			IngestPipe:         ingestPipe,
			WikiIngestPipe:     ctx.WikiIngestPipe,
			Logger:             logger,
			DefaultEmbeddingID: 15,
		}

		// Signal-aware context so consumer exits on SIGINT/SIGTERM
		consumerCtx, consumerStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

		go func() {
			defer consumerStop()
			for {
				err := kafkaConsumer.Consume(consumerCtx, &kafka.MessageHandler{
					OnMessage: docHandler.Handle,
					Logger:    logger,
				})
				if err != nil {
					logger.Errorf("kafka consume failed: %v, restarting in 3s...", err)
					select {
					case <-consumerCtx.Done():
						return
					case <-time.After(3 * time.Second):
					}
				} else {
					// Consume returned nil (context cancelled), exit normally
					return
				}
			}
		}()
	}

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		pb.RegisterKnowledgeBaseServer(grpcServer, h)

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryUserIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()
	if ctx.MaintenanceScheduler != nil {
		defer ctx.MaintenanceScheduler.Stop()
	}

	fmt.Printf("Starting knowledge-base rpc server at %s...\n", c.ListenOn)
	s.Start()
}
