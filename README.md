# MAIM

> **AI-Native Instant Messaging Platform** — 微服务架构 · LLM Bot 引擎 · RAG 知识库 · k3s 集群部署

---

## 项目概要

MAIM 是一个面向 AI 时代的即时通讯后端平台，将大语言模型深度融入实时通讯场景。当前由 **10 个 Go 服务** 构成；会话、消息与已读/未读模型同属 message-service，最终目标为 8 个部署单元，通过 **Helmfile + k3s** 声明式部署。

- **定位**：IM 平台 + AI Bot 引擎 + 知识库 RAG，三者一体化
- **规模**：10 个 Go 服务，gRPC + Kafka 通信；账号与好友关系同属 user-service，会话聚合与消息同属 message-service
- **部署**：Docker Compose 或 k3s，平台 DNS 发现，YAML 模板 + 环境变量配置

---

## 架构全景

```
 ┌──────────────────────────────────────────────────────────────┐
 │                       Web 前端 (React)                        │
 │                   ws:// / https://                            │
 └───────────┬──────────────────────────────┬───────────────────┘
             │                              │
    ┌────────▼────────┐              ┌──────▼──────┐
    │   ws-gateway    │              │   Gateway   │
    │  WebSocket 长连接 │              │  Gin BFF    │
    │  Port 8081      │              │  Port 8080  │
    └────────┬────────┘              └──────┬──────┘
             │ gRPC Push API                │ gRPC
             │                              │
    ┌────────▼──────────────────────────────▼──────────────┐
    │                  微服务层                              │
    │                                                       │
    │  ┌──────────┐ ┌──────────┐ ┌───────────┐            │
    │  │  user    │ │   conv   │ │  message  │  ...       │
    │  └──────────┘ └──────────┘ └───────────┘            │
    │                                                       │
    │  ┌──────────┐ ┌──────────┐ ┌───────────┐            │
    │  │ llm-gw   │ │   kb     │ │  ai-bot   │  ...       │
    │  └──────────┘ └──────────┘ └───────────┘            │
    └──────┬──────────────│──▲───────────┬────────────────┘
           │              │  │           │
    ┌──────▼──────┐ ┌─────▼──│──┐ ┌──────▼──────────────┐
    │ 平台 DNS    │ │   Kafka   │ │ signaling-service   │
    │ 服务发现    │ │  事件总线 │ │ 推送调度/在线状态     │
    └─────────────┘ └───────────┘ └─────────┬────────────┘
                                           │
    ┌───────────────────────────────────────▼────────────┐
    │                 中间件基础设施                        │
    │  PostgreSQL │ Redis │ MinIO │ Milvus │ ES │ Neo4j  │
    └────────────────────────────────────────────────────┘
```

### 消息流转全景

```
客户端发送消息
    │
    ▼
Gateway (REST) ──gRPC──▶ message-service
                              │
                    ┌─────────┼─────────────────────────┐
                    │         │                         │
               messages   nextSeq()          outbox_events
               写入      (PG UPSERT + RETURNING,  同事务写入
              (PostgreSQL)  同事务内)             (事务原子)
                    │         │                         │
                    └─────────┼─────────────────────────┘
                              │
                      ┌───────┴───────┐
                      │    DB 事务     │
                      │   CREATE /    │
                      │  ROLLBACK     │
                      └───────┬───────┘
                              │ 事务提交成功
                              ▼
                    OutboxDispatcher
                              │
                              ▼
                    Kafka "message.created"
                              │
                    ┌─────────┼──────────┬──────────────┐
                    │         │          │              │
                    ▼         ▼          ▼              ▼
                  inbox    search   conversation   signaling
                  writer   indexer  -service      -service
                    │         │          │              │
                    │         │     update last_msg   fanout
                    │         │     mark unread       │
                    │         │          │         ┌───┼───┐
                    │         │          │         │   │   │
                    ▼         ▼          ▼         ▼   ▼   ▼
                user_      ES      conv table  WS  FCM  Bot
                inbox     index              push APNS route

              messages 表  sequences 表      outbox_events 表
              (seq 有序)  (seq 持久化, PG)     (状态机: pending→sent/failed)
                                              pending ──▶ sent
                                                │
                                         指数退避重试
                                         1s→2s→...→60s(max)
                                                │
                                         超时(10次) → failed
```

---

## 项目结构

```
AIM/
├── app/                              # 10 个 Go 服务 (go-zero 统一布局)
│   ├── gateway/                      # REST API 网关 (Gin BFF, JWT + 限流)
│   ├── ws-gateway/                   # WebSocket 实时网关 (长连接 + 在线状态)
│   ├── user-service/                 # 账号、资料、好友关系、分组与黑名单
│   ├── message-service/              # 消息域 (会话与群组、消息、收件箱、已读未读)
│   ├── file-service/                 # 文件管理 (MinIO Presigned URL)
│   ├── llm-gateway/                  # LLM 模型网关 (多厂商路由 + 计费)
│   ├── bot-platform/                 # Bot 管理平台 (创建/配置/MCP/Webhook)
│   ├── ai-bot-service/               # AI Bot 执行引擎 (Eino ReAct Agent + 记忆)
│   ├── knowledge-base/               # RAG 向量检索（解析/分块/Embedding/检索）
│   └── signaling-service/            # 事件扇出与推送 (Kafka → WS/APNs/FCM/Bot路由)
│
├── deploy/
│   ├── docker/                       # Dockerfile (多阶段构建) + 入口脚本
│   ├── k3s/                          # k3s 部署清单
│   │   ├── helmfile.yaml             # 集群级 Helmfile (基础设施 + 应用服务)
│   │   ├── charts/aim-service/       # 通用服务 Helm Chart
│   │   ├── infrastructure/           # 基础设施 Helm Values
│   │   ├── values/staging/           # 按环境的服务配置
│   │   └── scripts/                  # 一键构建/部署脚本
│   └── monitoring/                   # Grafana 仪表盘 + Prometheus 告警规则
│
├── idl/                              # Protobuf IDL 定义 (16 个 .proto 文件)
├── pkg/                              # 共享工具包 (20+ 子包)
├── migrations/postgres/              # 数据库 Schema 迁移 SQL
├── web-client/                       # React 前端
├── go.mod                            # Go 1.25.0, go-zero v1.10.1
└── docker-compose.yml                # 本地开发中间件编排
```

### go-zero RPC 服务统一布局

每个 go-zero 服务遵循统一目录模式：

```
app/<service>/
├── etc/<service>.yaml          # 配置文件
├── internal/
│   ├── config/config.go        # Config struct
│   ├── svc/servicecontext.go   # ServiceContext — 依赖注入（DB、Redis、Kafka、gRPC client）
│   ├── logic/                  # 业务逻辑，按 server 子目录分组
│   ├── server/                 # gRPC server 实现，调用 logic
│   ├── repo/                   # 数据访问层（GORM 查询封装）
│   ├── model/                  # GORM model 定义
│   └── consumer/               # Kafka 消费者（部分服务）
├── pb/<service>/               # Protobuf 生成的 Go 代码
└── main.go                     # 入口：加载配置 → 初始化 ServiceContext → 启动 gRPC server + Kafka consumers
```

### 配置与服务发现

每个服务只保留 `app/<service>/etc/` 的一份 YAML 模板；入口统一通过 `conf.MustLoad(..., conf.UseEnv())` 加载。Compose 的 `x-aim-env` 与 Helm Chart 的 `environment` 提供部署环境，模板不再由启动脚本或 `sed` 改写。

- 本地直接启动：参考 `.env.example` 设置环境变量，再执行 `go run ./app/<service> -f app/<service>/etc/<filename>.yaml`；文件名以各服务 `etc/` 为准。
- Compose：准备 `.env` 中的 Postgres、MinIO、Neo4j、`JWT_SECRET` 与 `AIM_ENC_KEY`，执行 `docker compose up -d --build`。`AIM_ENC_KEY` 必须是 base64 编码的 32 字节密钥。改变环境变量后用 `docker compose up -d --force-recreate <service>` 重建容器，不支持热更新。
- k3s：在环境 values 中覆盖 Chart 的 `environment`，通过 Helmfile 更新；环境变量改变 Pod template 后触发滚动更新。后端 RPC Service 为 Headless，`dns:///aim-<service>:<port>` 返回各副本地址；gateway 保持普通 ClusterIP。
- `KAFKA_BROKERS` 与 `ELASTICSEARCH_ADDRESSES` 是 YAML 列表值，例如 `[kafka:9092]`。部署示例中的凭据只用于开发，生产部署必须覆盖。
- Milvus standalone 使用内嵌 etcd，将元数据保存在自身持久卷 `/var/lib/milvus/etcd`；没有共享 etcd 容器、初始化任务或 AIM 配置中心。它不是可以改用 DNS 替代的服务发现数据。

### Protobuf 生成

唯一入口是仓库根目录的 `make proto`。前置条件：GNU Make、`sed`，以及下列固定版本的工具都在 `PATH` 中（与现有生成代码保持一致）：

- `protoc` **29.4**：从 [Protobuf v29.4 release](https://github.com/protocolbuffers/protobuf/releases/tag/v29.4) 安装对应平台的发行包，保留其 `include/google/protobuf/` 标准协议文件。
- `protoc-gen-go` **v1.36.11**。
- `protoc-gen-go-grpc` **v1.6.1**。

安装 Go 插件（需要仓库要求的 Go 工具链）：

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
export PATH="$(go env GOPATH)/bin:$PATH"
make proto
```

入口在生成前检查工具版本，一次生成 `idl/` 及其服务子目录下的全部 `.proto`。输出只由各文件的 `go_package` 与 `go.mod` 的 module 前缀决定：服务协议写入所属 `app/<service>/pb/<package>/`，共享协议写入 `pkg/pb/<package>/`。不要使用 `source_relative` 拼接输出目录，也不要手改 `.pb.go` 或用 `goctl rpc protoc` 重新覆盖现有服务实现、client、配置与业务代码。新增或修改协议后，重复运行同一命令即可。

### 客户端可见面验收

前置条件：Python 3、Docker Engine、支持 `--wait` 的 Docker Compose 插件及 Buildx。验收只通过 gateway REST 与 WebSocket 驱动真实服务，不替换内部 RPC、Kafka 或数据库。

MinIO 社区版已改为[仅发布源码](https://github.com/minio/minio#source-only-distribution)，Compose 从固定版本源码构建 `aim-minio`，不再拉取不可用的 `minio/minio:latest`。首次构建需要访问 Go module proxy；保留原有 S3 接口、启动参数与真实 `mc ready` 健康检查。

Compose 的 `init-kafka-topics` 在 broker 就绪后幂等创建活跃 topic，消费者服务等待它成功后启动，避免空环境首次启动时消费者因 topic 不存在退出。验收失败时按服务分别保留有界日志，避免 Kafka 等高日志量服务截掉投递诊断。

```bash
python3 tests/e2e/run.py --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --cross-instance --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario stage-p3 --artifacts /tmp/aim-e2e-artifacts
```

每次使用独立 Compose project、网络与数据卷，无宿主端口映射。启动顺序按真实依赖编排（`message-service` 先于它的热路径调用方 `signaling-service`），全部中间件与应用就绪后才施加流量。成功或失败后均清理该 project 的容器与数据卷；`--artifacts` 保留诊断日志。

默认检查关系链、A/A 与 B/B，`--cross-instance` 追加 A/B 双向投递；`--scenario` 可选择单个场景，`stage-p3` 检查完整关系链与 A/A，`stage-p4` 追加会话与成员管理，但仍启动含两个长连接实例的全栈。P1 之后两者的期望不同：Compose 的推送目标 `WS_GATEWAY_ADDR` 只解析到主 `ws-gateway` 实例，因此 A/A（两端都连在推送可达的实例上）是确定性通过的；B/B 与 A/B 需要「按连接定向投递」，属 P6 范围，当前仍然失败——默认验收不会跳过或伪装该失败。

---

## 微服务详解

### 消息引擎 — 不重复·不丢失·不乱序

#### SendMessage 完整链路

```
客户端 → Gateway REST → message-service gRPC
  → [事务] messages + outbox_events + seq (PostgreSQL)
  → OutboxDispatcher (后台轮询)
  → Kafka message.created → signaling-service fanout → ws-gateway push → 客户端
```

#### 不重复（Exactly-Once 语义）

**三层防重机制**：

| 层级 | 机制 | 实现方式 |
|------|------|---------|
| 发送端 | 客户端幂等 key | `client_msg_id` + Redis `SetNX("msg:idempotent:{client_msg_id}", TTL=2小时)`，重复请求直接返回 `ErrDuplicateMessage` |
| Inbox 写入 | 数据库幂等 | `BatchInsert` 使用 `ON CONFLICT DO NOTHING`，联合主键 `(user_id, conv_id, seq)` 保证即使 Kafka 消息重复消费也不会产生重复 inbox 记录 |
| Kafka 消费 | 业务层幂等 | InboxWriter 消费前先 `ExistsByMessageID(messageID, convID)` 检查，已存在则跳过。配合 at-least-once 语义 + OutboxDispatcher 最多一次投递，实现 effectively-once |

#### 不丢失（零消息丢失）

采用 **Transactional Outbox Pattern** 替代旧的"先写 DB 再异步发 Kafka"方案，从根本上消除 DB 与 Kafka 之间的双写不一致。

**全链路防丢失机制**：

```
┌────────────────────────────────────────────────────────────┐
│  SendMessage 事务                                           │
│  BEGIN TXN                                                 │
│    INSERT INTO messages (...)                               │
│    seq = INSERT ... ON CONFLICT DO UPDATE RETURNING          │
│    INSERT INTO outbox_events (topic, key, payload, ...)     │
│    UPDATE conversations SET max_seq = seq                   │
│  COMMIT  ──── 原子提交，要么全部成功，要么全部回滚              │
└────────────────────────────────────────────────────────────┘
                           │ 
                           ▼
┌────────────────────────────────────────────────────────────┐
│  OutboxDispatcher (后台 goroutine)                          │
│                            │                               │
│  轮询 ──▶ SELECT ... FOR UPDATE SKIP LOCKED       │
│                     WHERE status=0                         │
│                     ORDER BY created_at ASC                 │
│                     LIMIT 100                              │
│                            │                               │
│                    ┌───────┴───────┐                       │
│                    │               │                       │
│                 Kafka.Send      失败                        │
│                    │               │                       │
│               status=1      retry_count+1                  │
│              dispatched_at   指数退避 next_retry_at         │
│                    │               │                       │
│                    │         retry > 10?                    │
│                    │         ├── 否 ──▶ 继续等待轮询         │
│                    │         └── 是 ──▶ status=2 (failed)   │
└────────────────────────────────────────────────────────────┘
```

1. **事务内双写**：`message` + `outbox_events` 在同一次 PostgreSQL 事务中原子写入，要么都成功要么都回滚，不存在"消息已存但 Kafka 未发"的问题
2. **OutboxDispatcher 轮询**：后台 goroutine 轮询 pending 事件，使用 `SELECT ... FOR UPDATE SKIP LOCKED` 保证多实例安全
3. **指数退避重试**：发送失败按 `1s → 2s → 4s → 8s → 16s → 32s → 60s` 退避，最多 10 次
4. **死信队列**：超过最大重试次数后标记 `status=2 (failed)`，人工介入或后续补偿
5. **下游消费者幂等**：InboxWriter、SearchIndexer 等在消费前做 `ExistsByMessageID` 幂等检查，不惧重复投递
6. **客户端 SyncMessages 最终兜底**：客户端可随时通过 `SyncMessages(from_seq=lastKnownSeq)` 拉取缺失消息

#### 不乱序（严格有序保证）

**Seq 序号机制**：

| 环节 | 机制 | 保证 |
|------|------|------|
| Seq 生成 | PostgreSQL UPSERT + RETURNING | 同一会话内 seq 严格递增（`INSERT ... ON CONFLICT DO UPDATE SET current_seq = current_seq + 1 RETURNING current_seq`），与消息写入同事务 |
| 消息存储 | `messages` 表 `idx_conv_seq (conv_id, seq)` 索引 | 所有查询 `ORDER BY seq`，天然有序 |
| Inbox 存储 | `user_inbox` 包含 `seq` | `GetByUserAndConv` 查询 `ORDER BY seq ASC` |
| 增量同步 | `SyncMessages: WHERE seq > from_seq ORDER BY seq ASC` | 客户端按 seq 顺序接收，不会乱序 |
| 游标分页 | `GetMessages: WHERE seq < cursor ORDER BY seq DESC` | 基于 seq，不会跨页乱序 |

**关键设计**：每个会话拥有独立的 seq 空间（表 `msg.sequences` 中每 conv 一行），不同会话的 seq 互不影响。PostgreSQL UPSERT 的原子性保证了同一事务内 seq 的严格递增。

#### Inbox 写扩散模型

每条消息为每个成员生成一条 `user_inbox` 记录，支持：

- **每用户独立的已读位置** (`last_read_seq`)
- **每用户独立的删除状态** (`is_deleted`) — 删除仅对当前用户生效
- **按 seq 排序的增量同步** — 客户端只需记录上次同步的 seq 即可拉取增量

### AI Bot 执行引擎

ai-bot-service 是 AI 能力的核心引擎，基于 **CloudWeGo Eino** 框架构建 ReAct Agent，实现 LLM 推理、工具调用、知识检索、记忆管理的完整闭环。

#### 整体处理流程

```
Kafka message.created 事件
  → 去重(Redis SETNX, 2小时TTL)
  → 查找 Bot + ConvBot 配置
  → 判断是否应响应(shouldRespond)
  → 构建 MCP 工具列表
  → 构建 Context（BuildContextNode）
  → 创建 ReAct Agent
  → 流式/非流式推理
  → 发送回复 + 触发记忆提取
```

#### ReAct Agent 架构

基于 Eino 的 `react.Agent` 实现 ReAct (Reasoning + Acting) 推理循环：

```go
agent, err := react.NewAgent(ctx, &react.AgentConfig{
    ToolCallingModel: einoChatModel,    // LLM 模型（通过 llm-gateway）
    ToolsConfig: compose.ToolsNodeConfig{
        Tools:               mcpTools,  // MCP 工具列表
        ToolCallMiddlewares: []compose.ToolMiddleware{toolMiddleware},
    },
    MessageModifier: func(c context.Context, msgs []*schema.Message) []*schema.Message {
        // 每次推理前注入：系统提示词 + 知识上下文 + 记忆 + 历史消息
    },
    MaxStep: bot.MaxStep,  // 限制推理循环轮数（默认5）
})
```

**MessageModifier** 在每次推理前注入四类上下文：

1. **系统提示词**：模板渲染 `{botname}`, `{username}`, `{user_language}`, `{message}` 等变量
2. **知识库内容**：通过 `KnowledgeResolver` 检索绑定的知识库（RAG 模式）
3. **用户记忆**：通过 `MemoryStore` 检索用户相关记忆
4. **历史消息**：从 message-service 获取最近 N 条消息

#### 知识检索

`kbClient.Retrieve()` 向量检索，返回 top-5 文档片段，用于精确文档片段检索。

检索结果生成 `KnowledgeSource`（包含 type/kb_name/kb_id/title/content），最终注入到 LLM 上下文中。

#### 记忆管理系统

记忆系统实现 AI Bot 的**长期记忆**能力，采用 **Neo4j（图谱/时序）+ Milvus（向量语义 + BM25）** 双存储。LLM 只参与事实提取和用户画像生成，在线读取阶段走确定性检索与排序。

##### 整体流程

```
用户消息
  │
  ├─ 写入路径（异步，不阻塞对话）:
  │   manager.RememberAsync()
  │     ├── 1. 保存 Episode（原始消息，Neo4j）
  │     ├── 2. LLM 抽取结构化事实（SPO 三元组）
  │     ├── 3. 过滤低质事实（置信度<0.6 / 重要性<0.3 / 寒暄语）
  │     ├── 4. 谓词路由: HAS_FACT + 可选 Entity→Entity 边
  │     ├── 5. 向量化 → 写入 Milvus（dense vector + sparse BM25）
  │     └── 6. 条件触发画像更新（新事实 ≥3 条 & 距上次 ≥5分钟）
  │
  └─ 读取路径:
      BuildContextNode.loadMemories()
        ├── GetProfile() → O(1) 读取用户画像缓存
        └── Retrieve() → manager.Search()
              ├── 1. Milvus HybridSearch
              │      dense(HNSW/COSINE) + sparse(BM25) → RRF → fact_id
              ├── 2. Neo4j SearchByIDs
              │      按 fact_id 回查直接事实，应用 current / historical 时间过滤
              ├── 3. Neo4j Entity BFS 候选
              │      fulltext 命中 entry Entity 后，只走显式 Entity→Entity 边（最多3跳）
              ├── 4. Rank-Based Fusion
              └── 5. 兜底: direct+graph 都为空时，Neo4j 全文索引 → 全量质量排序
```

##### 用户画像生成

画像采用**增量更新**策略，避免每次全量重建：

```
触发条件: 新事实 ≥3 条 AND 距上次更新 ≥5 分钟
  │
  ├─ 首次生成: 最近 20 条事实 → LLM 生成画像 (≤300字)
  │
  └─ 增量更新:
        ├── 读取当前画像文本
        ├── 获取增量新事实 + 已失效事实
        └── LLM 融合更新（合并新事实 + 移除失效信息）
```

#### 流式输出

| 通道 | 实现 |
|------|------|
| **WebSocket** | 通过 ws-gateway 的 `PushToConv` gRPC 推送流式 chunk |

#### MCP 工具集成

MCP (Model Context Protocol) 工具集成流程：

1. 从 bot-platform 获取 Bot 绑定的 MCP Server 配置
2. 创建 `mcp-go` 客户端（支持 SSE 和 StreamableHTTP 两种传输）
3. 发送 MCP `Initialize` 请求（协议版本协商）
4. 通过 `einoMCP.GetTools()` 获取所有工具定义

内置工具：`web_search`

#### 会话工具服务

`ConversationToolService` 提供三个 AI 增强工具：

| RPC | 功能 |
|-----|------|
| `SummarizeConversation` | 异步总结对话（提取要点 + 待办事项），结果通过 WebSocket 推送 |
| `Translate` | 多语言翻译 |
| `GenerateReplyCandidates` | 生成 3 条快捷回复建议（每条不超过 15 字） |

---

### LLM 模型网关

llm-gateway 是所有 LLM 调用的统一入口，提供多 Provider 抽象、计费、限流。

#### 多 Provider 抽象

**chatModel** 实现 Eino 的 `model.BaseChatModel` 接口：

- **Generate**: 同步调用 `/chat/completions`
- **Stream**: SSE 流式调用，支持增量 Tool Call 累积（按 index 聚合 delta）
- 自动根据 provider 设置默认 BaseURL
- Eino callback 集成

---

### 知识库服务

知识库支持 **RAG 检索**，满足不同场景的知识管理需求。上传文档后经解析、分块、Embedding 管线处理。

---

#### RAG Ingest Pipeline（五阶段）

```
文档上传
  │
  ├─ Stage 1: Parse（解析）
  │     ├─ 本地解析器 (builtin): PDF/Markdown/HTML → RawText
  │     └─ MinerU 解析器: PDF → 高精度 Markdown + 图片
  │
  ├─ Stage 1.5: Image Processing（图片处理）
  │     ├─ 下载 Markdown 内嵌图片 → MinIO
  │     └─ VLM 图片描述 → 替换原图引用为 <figure> 标签
  │
  ├─ Stage 2: Chunk（分块）
  │     ├─ 策略选择（3 Tier）→ 见下方「分块策略详解」
  │     └─ Parent-Child 模式 → DB 写入
  │
  ├─ Stage 3: Embed + Store（向量化 + 存储）
  │     ├─ Embedder: llm-gateway RPC (批量, batchSize=10)
  │     ├─ 稠密向量: HNSW(COSINE) 索引
  │     └─ 稀疏向量: BM25 → SparseInvertedIndex
  │
  └─ 完成 → DocStatusReady
```

##### 分块策略详解

整个分块在 `infra/chunker/chunker.go` 实现，入口 `Chunk()` 有三个策略分支：

```
Chunk()
  │
  ├─ 检测 RawText 是否包含 Markdown 标题 (\n# 或开头 #)
  │
  ├─ 是 → Tier 1: headingAwareChunk（按标题结构分块）
  │   ├─ 扫描 RawText 逐行解析 # ~ ###### 标题层级
  │   ├─ 维护 heading breadcrumb（标题面包屑路径）
  │   │   └─ 例: ["Introduction", "Architecture", "Storage Layer"]
  │   ├─ 在 heading 边界处断块 → 每块携带完整 breadcrumb
  │   └─ 递归分隔符拆分（\n\n → \n → 。）回退到 chunkFlat
  │
  ├─ 否 & ≥5 个启发式边界 → Tier 2: heuristicChunk（边界标记分块）
  │   ├─ 检测 8 种边界类型，优先级从高到低:
  │   │   100 ─ \\f (表单换页)
  │   │    90 ─ 编号章节 ("1. ", "1.1 ", "第X章")
  │   │    85 ─ Chapter 关键词 ("Chapter ", "CHAPTER ")
  │   │    70 ─ 全大写标题行 ("INTRODUCTION\\n")
  │   │    60 ─ 视觉分隔符 ("---", "***", "===")
  │   │    50 ─ 页脚标记 ("Page ", "Copyright ")
  │   │    40 ─ 过量空行 (≥3)
  │   ├─ dedupWindow=80: 80 字节内低优 → 高优抑制
  │   └─ 保护区域（code fence/LaTeX/表格/图片/链接）不分割
  │
  └─ 否 → Tier 3: chunkFlat（递归分隔符拆分）
      ├─ 分隔符优先级: ["\n\n", "\n", "。"]
      ├─ 保护区域: 同上述 7 种 Pattern
      │   ├─ code fence: /```(?:\\w+)?[\r\n].*?```/s
      │   ├─ LaTeX math: (?s)\\$\\$.*?\\$\\$
      │   ├─ 表格: Markdown table rows (header + separator + rows)
      │   ├─ 图片: ![alt](url)
      │   ├─ 链接: [text](url)
      │   └─ VLM: <figure>...</figure>
      ├─ buildUnits() → 按分隔符拆分 + 保护区域冻结（rune 偏移量）
      ├─ mergeUnits() → 合并为 ~512 字符块（chunkSize）
      │   ├─ 粘连 separator 到前一块
      │   ├─ headerTracker: 表格表头自动注入后续分块
      │   ├─ overlap 50 字符（从尾部取，剔除纯分隔符）
      │   └─ maxProtectedSize 7500 → 超大保护块强制拆分
      └─ estimateTokens() = runeCount × 4/3
```

##### Parent-Child 二级分块

当 `cfg.ParentChild.Enabled = true` 时，三种策略各自产生两级分块：

```
Parent 块（~4096 字符）  ─────── 提供 LLM 上下文窗口
  │    metadata: {block_type: "parent"}
  ├─ Child 块1（~384 字符）     ─── 用于向量检索
  ├─ Child 块2（~384 字符）
  └─ Child 块3（~384 字符）
       metadata: {block_type: "child", parent_index: 0}
```

- Parent 和 Child 使用完全独立的 `ChunkingConfig`（size/overlap/separators）
- Child 通过 `document_chunks.parent_chunk_id` 关联到 Parent
- 检索时先匹配 Child，可 `expandParentChunks` 回填 Parent 上下文

#### RAG Retrieve Pipeline

```
用户查询
  │
  ├─ Embedder: 查询向量化 (1536维)
  │
  ├─ 检索模式:
  │   ├─ vector ──── dense vector → HNSW(COSINE) ANN (CandidateTopK)
  │   ├─ fulltext ── BM25 → SparseInvertedIndex (CandidateTopK)
  │   └─ hybrid ──── dense + sparse → RRF (Reciprocal Rank Fusion) 融合
  │                  (DenseWeight / SparseWeight 加权)
  │
  ├─ Rerank（可选: Reranker RPC）
  │   └─ reranker 对候选重排序 → TopK
  │
  ├─ ScoreThreshold 过滤 → 低于阈值剔除
  │
  ├─ expandParentChunks（当前为占位，直接返回子块）
  │
  └─ 返回 RetrieveItem[] → LLM 上下文注入
```

---

### Bot 管理平台

#### Bot 类型体系

| 类型 | 说明 | 模型来源 | 连接方式 |
|------|------|---------|---------|
| **Official** | 官方 Bot，基于模板（qa/knowledge） | 平台模型 | 内部 Kafka |
| **Self-Deployed** | 用户自有 API Key + BaseURL | 用户自提供 | 内部 Kafka |
| **Third-Party** | 第三方 Bot | 外部 | WebSocket 或 Webhook |

---

### 信令推送服务

signaling-service 是消息扇出(fanout)的核心枢纽。

#### 核心职责

1. **在线推送**：通过 gRPC 调用 ws-gateway 的 `InternalPushService`
2. **离线推送**：通过 FCM/APNS 发送移动端通知
3. **Bot 路由**：将消息推送给第三方 Bot（WS 或 Webhook）；官方与自部署 Bot 由 ai-bot-service 直接消费 `message.created`
4. **未读计数**：消息域按 `max_seq - last_read_seq` 批量计算权威未读数；推送层不维护计数器
5. **已读回执**：消费 `conversation.read.updated` 事件，推送给其他成员

#### Fanout 扇出逻辑

**消息扇出**：

1. 获取会话成员列表
2. 从消息域批量读取接收者的权威未读数（缺失成员计为 0）
3. 区分在线/离线用户
4. 为每个在线用户推送 `message.new` 事件（附带个人未读数）
5. 为离线用户通过 FCM/APNS 发送推送通知
6. 推送给会话中的 Bot

**Bot 路由策略**：

| Bot 类型 | 路由方式 |
|---------|---------|
| Official / Self-Deployed | Kafka `message.created` topic → ai-bot-service |
| Third-Party (conn_mode=ws) | ws-gateway WebSocket 推送 |
| Third-Party (conn_mode=webhook) | HTTP Webhook 回调 |

## 系统能力矩阵

| 能力域 | 实现 |
|--------|------|
| 即时消息 | 文本/图片/文件/语音消息，单聊与群聊，消息状态追踪 |
| 消息可靠性 | 不重复（幂等key+DB幂等+Kafka幂等）、不丢失（Transactional Outbox：DB+outbox_events状态机+指数退避重试+SyncMessages兜底）、不乱序（PostgreSQL UPSERT seq+有序存储+有序查询） |
| 消息管理 | 发送/接收/撤回/编辑/引用回复，全文搜索 (ES + IK中文分词) |
| 社交关系 | 好友增删/分组/备注/黑名单，群创建/邀请/踢出/转让/公告 |
| AI Bot | @Bot 对话，ReAct Agent 推理，MCP 工具调用，多 Bot 会话协作，流式输出 |
| 记忆系统 | LLM SPO 提取 + 谓词配置表路由 + Entity 间图谱边 + BFS 多跳遍历（距离衰减）+ 增量画像 |
| 知识库 | 多格式文档上传，RAG 向量检索问答，混合检索 + Reranker 重排序 |
| MCP 集成 | MCP Server 管理 + 工具发现 + Bot 绑定 + 运行时工具调用 |
| 多端同步 | 同账号多设备消息同步，已读状态一致，离线消息补推 |
| 文件存储 | MinIO Presigned URL，文件/头像/附件统一管理 |
| 离线推送 | 在线 WebSocket 实时推送 + 离线 FCM/APNs 通知 |
| 服务治理 | 超时控制、限流、熔断、重试，YAML + 环境变量配置 |
| 可观测性 | Jaeger 分布式追踪、Prometheus 监控、Grafana 仪表盘、EFK 日志 |
| 部署运维 | k3s 集群，Helmfile 声明式，一键部署脚本 |

---

## 可能开展的活动

1. 强大 Bot 记忆层
2. 实现 Bot 市场与知识库市场

