package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/handler"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/embedder"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/milvus"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/reranker"
	"github.com/maomeng/aim/app/knowledge-base/internal/metrics"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	"github.com/maomeng/aim/app/knowledge-base/internal/svc"
	pb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/knowledge-base.yaml", "the config file")
var role = flag.String("role", "online", "workload role: online or ingest")

func main() {
	flag.Parse()
	if *role != "online" && *role != "ingest" {
		panic("role must be online or ingest")
	}
	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	if *role == "ingest" {
		if c.Ingest.RequestsPerSecond <= 0 || c.Ingest.Concurrency <= 0 || c.Ingest.EmbeddingToken == "" {
			panic("ingest requires positive requestsPerSecond/concurrency and an embeddingToken")
		}
		c.Name = "knowledge-ingest"
		c.Telemetry.Name = c.Name
		c.Prometheus.Port = c.Ingest.MetricsPort
		c.ServiceConf.MustSetUp()
	}
	resources := svc.NewServiceContext(c, *role)
	defer resources.Close()
	logger := logx.DefaultLogger()
	vecStore, err := milvus.NewMilvusStore(c.Milvus)
	if err != nil {
		panic(fmt.Sprintf("init milvus failed: %v", err))
	}
	defer vecStore.Close(context.Background())
	embed := embedder.NewLLMGatewayEmbedder(resources.LLMGatewayClient)

	if *role == "ingest" {
		ingestPipe := &pipeline.IngestPipeline{
			Embedder: embed, VectorStore: vecStore, FileStore: resources.FileStore,
			DocRepo: resources.DocRepo, KBRepo: resources.KBRepo,
			NextChunkID: func(ctx context.Context) (int64, error) {
				var id int64
				err := resources.DB.WithContext(ctx).Raw("SELECT nextval('knowledge.ingest_chunk_ids')").Scan(&id).Error
				return id, err
			},
			LLMGatewayClient: resources.LLMGatewayClient, RetryLimit: c.RetryLimit,
			MaxFileSize: c.MaxFileSize, Logger: logger,
		}
		docHandler := handler.NewDocumentUploadedHandler(resources.DocRepo, resources.KBRepo, ingestPipe, logger, c.Ingest)
		if err := runIngest(c, docHandler, logger); err != nil {
			panic(err)
		}
		return
	}

	h := &handler.KnowledgeBaseHandler{
		KBRepo: resources.KBRepo, DocRepo: resources.DocRepo, FileStore: resources.FileStore,
		VectorStore: vecStore, Producer: resources.Producer,
		RetrievePipe: &pipeline.RetrievePipeline{
			KBRepo: resources.KBRepo, Embedder: embed, VectorStore: vecStore,
			Reranker: reranker.NewLLMGatewayReranker(resources.LLMGatewayClient), Logger: logger,
			EmbeddingModelID: 15,
		},
		Snowflake: resources.Snowflake, Logger: logger, LLMGateway: resources.LLMGatewayClient,
	}
	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		pb.RegisterKnowledgeBaseServer(grpcServer, h)
		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryUserIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()
	fmt.Printf("Starting knowledge-base online rpc server at %s...\n", c.ListenOn)
	s.Start()
}

func runIngest(c config.Config, h *handler.DocumentUploadedHandler, logger logx.Logger) error {
	consumer, err := kafka.NewConsumer(c.Kafka, []string{"document.uploaded"}, c.Kafka.ConsumerGroup, logger)
	if err != nil {
		return fmt.Errorf("init ingest consumer: %w", err)
	}
	defer consumer.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if ctx.Err() != nil || !h.Ready.Load() {
			http.Error(w, "ingest consumer not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", c.Ingest.HealthPort))
	if err != nil {
		return fmt.Errorf("listen ingest health: %w", err)
	}
	health := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	healthDone := make(chan error, 1)
	go func() {
		err := health.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
		healthDone <- err
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = health.Shutdown(shutdownCtx)
	}()
	logger.Infof("starting knowledge-ingest consumer group=%s health_port=%d metrics_port=%d concurrency=%d requests_per_second=%d", c.Kafka.ConsumerGroup, c.Ingest.HealthPort, c.Ingest.MetricsPort, c.Ingest.Concurrency, c.Ingest.RequestsPerSecond)
	for ctx.Err() == nil {
		err := consumer.Consume(ctx, h)
		h.Ready.Store(false)
		metrics.KbIngestConsumerReady.Set(0)
		if ctx.Err() != nil {
			break
		}
		metrics.KbIngestConsumerErrors.Inc("consume")
		logger.Errorf("ingest consumer failed: %v", err)
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
	select {
	case err := <-healthDone:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	default:
	}
	return nil
}
