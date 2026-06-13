package svc

import (
	"fmt"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/eventpush"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/chunker"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/llmgateway"
	infraMinio "github.com/maomeng/aim/app/knowledge-base/internal/infra/minio"
	infraNeo4j "github.com/maomeng/aim/app/knowledge-base/internal/infra/neo4j"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/parser"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/repo"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/scheduler"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	llmgatewaypb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/database"
	pkgkafka "github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	pkgminio "github.com/maomeng/aim/pkg/minio"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config           config.Config
	DB               *database.DB
	KBRepo           domain.KBRepo
	DocRepo          domain.DocumentRepo
	FileStore        domain.FileStore
	Parser           domain.Parser
	Chunker          domain.Chunker
	Producer         *pkgkafka.Producer
	LLMGatewayClient zrpc.Client
	Snowflake        *snowflake.Node
	// Wiki 相关组件
	WikiRepo             domain.WikiPageRepo
	WikiIngestPipe       *pipeline.WikiIngestPipeline
	WikiSearchPipe       *pipeline.WikiSearchPipeline
	WikiLintPipe         *pipeline.WikiLintPipeline
	WikiMaintain         *pipeline.WikiMaintenanceAgent
	WikiAgent            *pipeline.WikiReActAgent
	WikiEinoAgent        *pipeline.WikiEinoAgent
	WikiLLMGateway       pipeline.LLMGateway
	LogWriter            *pipeline.LogWriter
	Neo4jStore           infraNeo4j.GraphStore
	MaintenanceScheduler *scheduler.MaintenanceScheduler
	Pusher               eventpush.Pusher
}

func NewServiceContext(c config.Config) *ServiceContext {
	log := logx.DefaultLogger()
	log.Infof("Initializing knowledge-base service...")

	db, err := database.NewDB(c.Database, log)
	if err != nil {
		panic(fmt.Sprintf("init database failed: %v", err))
	}
	if err := db.AutoMigrate(&domain.KnowledgeBase{}, &domain.Document{}, &domain.ChunkRecord{}, &domain.KnowledgeBinding{},
		&domain.WikiPage{}, &domain.WikiPageIssue{}); err != nil {
		panic(fmt.Sprintf("auto migrate failed: %v", err))
	}
	log.Infof("Database connected")

	kafkaProducer, err := pkgkafka.NewProducer(c.Kafka, "document.uploaded", log)
	if err != nil {
		panic(fmt.Sprintf("init kafka producer failed: %v", err))
	}

	minioClient, err := pkgminio.NewClient(c.Minio)
	if err != nil {
		panic(fmt.Sprintf("init minio client failed: %v", err))
	}
	fileStore := infraMinio.NewFileStore(minioClient)

	var neo4jStore infraNeo4j.GraphStore = infraNeo4j.NewNoopGraphStore()
	if c.Neo4j.Enabled && c.Neo4j.URI != "" {
		store, err := infraNeo4j.NewGraphStore(c.Neo4j)
		if err != nil {
			log.Errorf("neo4j init failed, graph features disabled: %v", err)
		} else {
			neo4jStore = store
		}
	}

	snowNode, err := snowflake.NewNode(c.Snowflake.WorkerID)
	if err != nil {
		snowNode, err = snowflake.NewNode(0)
		if err != nil {
			panic(fmt.Sprintf("init snowflake failed: %v", err))
		}
	}

	kbRepo := repo.NewKBRepo(db).WithSnow(snowNode)
	docRepo := repo.NewDocumentRepo(db).WithSnow(snowNode)
	llmGatewayClient := zrpc.MustNewClient(c.LLMGateway)
	defaultParser := parser.NewParserChain(parser.NewBuiltinParser("txt"))
	defaultChunker := chunker.NewChunker()

	// Wiki 组件
	wikiRepo := repo.NewWikiPageRepo(db, snowNode)
	wikiLLMGW := llmgateway.NewGatewayAdapter(llmGatewayClient)
	logWriter := &pipeline.LogWriter{WikiRepo: wikiRepo, Snowflake: snowNode}

	wikiIngestPipe := pipeline.NewWikiIngestPipeline(
		neo4jStore, wikiRepo, fileStore, docRepo, kbRepo, wikiLLMGW, snowNode, log,
	)
	wikiIngestPipe.LogWriter = logWriter

	wikiSearchPipe := pipeline.NewWikiSearchPipeline(wikiRepo, wikiLLMGW, log)
	wikiLintPipe := pipeline.NewWikiLintPipeline(wikiRepo, kbRepo, wikiLLMGW, snowNode, log)
	wikiMaintainReAct := pipeline.NewMaintenanceReActAgent(
		wikiRepo, docRepo, kbRepo, fileStore,
		llmgatewaypb.NewLLMGatewayClient(llmGatewayClient.Conn()),
		snowNode, log,
	)
	wikiMaintainReAct.LogWriter = logWriter

	wikiMaintain := pipeline.NewWikiMaintenanceAgent(
		neo4jStore, wikiRepo, docRepo, kbRepo, fileStore, wikiLLMGW, snowNode, log,
	)
	wikiMaintain.LogWriter = logWriter
	wikiMaintain.ReActAgent = wikiMaintainReAct

	wikiAgent := pipeline.NewWikiReActAgent(wikiRepo, kbRepo, wikiLLMGW, snowNode, log)
	wikiEinoAgent := pipeline.NewWikiEinoAgent(
		wikiRepo, docRepo, fileStore,
		llmgatewaypb.NewLLMGatewayClient(llmGatewayClient.Conn()),
		snowNode,
	)
	maintenanceScheduler := scheduler.New(kbRepo, wikiMaintain, logWriter, log)

	return &ServiceContext{
		Config:               c,
		DB:                   db,
		KBRepo:               kbRepo,
		DocRepo:              docRepo,
		FileStore:            fileStore,
		Parser:               defaultParser,
		Chunker:              defaultChunker,
		Producer:             kafkaProducer,
		LLMGatewayClient:     llmGatewayClient,
		Snowflake:            snowNode,
		WikiRepo:             wikiRepo,
		WikiIngestPipe:       wikiIngestPipe,
		WikiSearchPipe:       wikiSearchPipe,
		WikiLintPipe:         wikiLintPipe,
		WikiMaintain:         wikiMaintain,
		WikiAgent:            wikiAgent,
		WikiEinoAgent:        wikiEinoAgent,
		WikiLLMGateway:       wikiLLMGW,
		LogWriter:            logWriter,
		Neo4jStore:           neo4jStore,
		MaintenanceScheduler: maintenanceScheduler,
	}
}
