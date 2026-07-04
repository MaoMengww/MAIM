# MAIM

> **AI-Native Instant Messaging Platform** — 微服务架构 · LLM Bot 引擎 · RAG 知识库 · k3s 集群部署

---

## 项目概要

MAIM 是一个面向 AI 时代的即时通讯后端平台，将大语言模型深度融入实时通讯场景。系统由 **13 个 Go 微服务** 构成，涵盖消息引擎、Bot 编排、知识库 RAG 检索、WebSocket 实时推送等完整业务域，通过 **Helmfile + k3s** 声明式部署。

- **定位**：IM 平台 + AI Bot 引擎 + 知识库 RAG，三者一体化
- **规模**：13 个 Go 微服务，gRPC + Kafka 通信
- **部署**：docker或者k3s集群，Helmfile 统一编排 12 个基础设施 + 13 个应用服务

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
    │  │  user    │ │  friend  │ │  message  │  ...       │
    │  └──────────┘ └──────────┘ └───────────┘            │
    │                                                       │
    │  ┌──────────┐ ┌──────────┐ ┌───────────┐            │
    │  │ llm-gw   │ │   kb     │ │  ai-bot   │  ...       │
    │  └──────────┘ └──────────┘ └───────────┘            │
    └──────┬──────────────│──▲───────────┬────────────────┘
           │              │  │           │
    ┌──────▼──────┐ ┌─────▼──│──┐ ┌──────▼──────────────┐
    │    etcd     │ │   Kafka   │ │  signaling-service  │
    │ 服务发现/配置 │ │  事件总线  │ │  推送调度/在线状态    │
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
├── app/                              # 13 个 Go 微服务 (go-zero 统一布局)
│   ├── gateway/                      # REST API 网关 (Gin BFF, JWT + 限流)
│   ├── ws-gateway/                   # WebSocket 实时网关 (长连接 + 在线状态)
│   ├── user-service/                 # 用户注册/登录/资料管理
│   ├── friend-service/               # 好友关系管理 (添加/删除/黑名单)
│   ├── conversation-service/         # 会话与群组管理 (Bot 绑定)
│   ├── message-service/              # 消息引擎 (收发 + Kafka Consumer)
│   ├── file-service/                 # 文件管理 (MinIO Presigned URL)
│   ├── llm-gateway/                  # LLM 模型网关 (多厂商路由 + 计费)
│   ├── bot-platform/                 # Bot 管理平台 (创建/配置/MCP/Webhook)
│   ├── ai-bot-service/               # AI Bot 执行引擎 (Eino ReAct Agent + 记忆)
│   ├── knowledge-base/               # RAG 向量检索 + Wiki 自动生成
│   ├── signaling-service/            # 事件扇出与推送 (Kafka → WS/APNs/FCM/Bot路由)
│   └── audit-service/                # 审计日志服务
│
├── deploy/
│   ├── docker/                       # Dockerfile (多阶段构建) + 入口脚本
│   ├── k3s/                          # k3s 部署清单
│   │   ├── helmfile.yaml             # 集群级 Helmfile (基础设施 + 应用服务)
│   │   ├── charts/aim-service/       # 通用服务 Helm Chart
│   │   ├── infrastructure/           # 基础设施 Helm Values
│   │   ├── jobs/                     # 一次性 Job (etcd 配置初始化)
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
2. **知识库内容**：通过 `KnowledgeResolver` 检索绑定的知识库（RAG 或 Wiki 模式）
3. **用户记忆**：通过 `MemoryStore` 检索用户相关记忆
4. **历史消息**：从 message-service 获取最近 N 条消息

#### 知识检索双模式

| 模式 | 实现方式 | 适用场景 |
|------|---------|---------|
| **RAG 模式** | `kbClient.Retrieve()` 向量检索，返回 top-5 文档片段 | 精确文档片段检索 |
| **Wiki 模式** | `kbClient.WikiQuery()` ReAct Agent 查询，返回结构化答案 + 引用 | 需要综合推理的知识查询 |

两种模式的结果都生成 `KnowledgeSource`（包含 type/kb_name/kb_id/title/content），最终注入到 LLM 上下文中。

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

知识库支持 **RAG 检索** 和 **Wiki 自动生成** 双模式，满足不同场景的知识管理需求。上传文档后经两条独立管线并行处理。

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
  └─ 完成 → DocStatusReady → 触发 Wiki 管线
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

#### Wiki 模式

基于 [karpathy/llm-wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f) 理念构建，从解析后的文档中自动提取结构化知识，生成可维护的 Wiki 页面并构建知识图谱。

##### Wiki 页面类型

| 类型 | Slug 前缀 | 说明 | 生成方式 |
|------|-----------|------|---------|
| `summary` | `summary/` | 文档标题 + 概览 | 每文档 LLM 摘要 |
| `entity` | `entity/` | 命名实体（人/系统/协议/产品） | LLM 从文档中提取 |
| `concept` | `concept/` | 抽象概念（理论/方法/机制） | LLM 从文档中提取 |
| `synthesis` | `synthesis/` | 跨文档综合论述 | LLM 分析所有文档后生成 |
| `comparison` | `comparison/` | 实体/概念对比分析 | LLM 发现可比对象后生成 |
| `index` | `index` | 知识库目录 | 自动维护 |
| `log` | `log` / `log-YYYY-MM-DD` | 变更日志 | 自动记录 |

##### Wiki Ingest Pipeline

```
文档上传
  │
  ├─ Pass 0: Per-doc Candidate Extraction（并发, semaphore=5）
  │   ├─ LLM 调用 candidateExtractionPrompt
  │   ├─ 返回 entities[] + concepts[]
  │   │   └─ 含 slug / name / description / details / aliases
  │   └─ 传入 previousSlugs 确保 slug 连续性
  │
  ├─ 4.5 Dedup: 跨文档去重管道
  │   ├─ Layer 1: SQL — FindSimilarPages (pg_trgm)
  │   │   ├─ similarity(lower(title), lower($query)) > 0.1
  │   │   ├─ 每 item 返回 Top-20 候选
  │   │   └─ GIN 索引加速 (idx_wiki_pages_title_trgm)
  │   │
  │   ├─ Layer 2: 构建 LLM 输入
  │   │   └─ 将 <new_items> + <existing_pages> 打包为 LLM 输入
  │   │
  │   ├─ Layer 3: LLM 裁决 (WikiDeduplicationPrompt)
  │   │   ├─ 判断同义/变体/缩写/翻译关系
  │   │   ├─ 输出 {"merges": {"entity/paxos算法": "entity/paxos"}}
  │   │   └─ 原则: 同义可合并，同类不同物不合并，父子范畴不合并
  │   │
  │   └─ Layer 4: validMerge 校验
  │       ├─ 目标 slug 必须在 SQL 返回的候选集中（防 LLM 幻觉）
  │       └─ 类型前缀必须一致 (entity→entity, concept→concept)
  │
  ├─ 创建/合并 Wiki 页面（串行）
  │   ├─ 已存在 → reduceMerge（LLM 合并: SUMMARY + 内容融合）
  │   ├─ 新页面 → 创建 WikiPage (Snowflake ID)
  │   └─ in-batch dedup: createdSlugs map 避免同批次重复
  │
  ├─ 文档摘要 (summary/doc-{slug})
  │
  ├─ 综合论述 (synthesis/) — LLM 分析所有文档生成 0-5 篇
  │
  ├─ 对比分析 (comparison/) — LLM 发现可比实体/概念对
  │
  ├─ 交叉链接注入 (CrossLinks)
  │   ├─ 扫描所有页面 title/aliases → 在其他页面中查找匹配
  │   ├─ 替换为 [[slug]] 双向链接
  │   └─ 保护区域: 代码块/已有链接/内联代码不替换
  │
  ├─ Neo4j 图谱同步
  │   ├─ 节点: (slug, title, page_type)
  │   └─ 关系: out_links → 有向边
  │
  ├─ 索引页更新 (index) — 按 type 分组列出所有页面
  │
  └─ 变更日志 (log) — 满 100 条自动归档
```

##### 去重管线详解

四层去重管道解决上传多文档后的同名变体重复问题（如"Paxos算法" vs "Paxos 算法" vs "Multi-Paxos"）：

```
新提取的 items (entities + concepts)
     │
     ├─ Layer 1: SQL — FindSimilarPages()
     │      pg_trgm 三元组相似度搜索
     │      similarity(lower(title), lower($query))
     │      阈值 0.1，每 item 返回 Top-20
     │      使用 GIN 索引加速 (idx_wiki_pages_title_trgm)
     │
     ├─ Layer 2: XML 打包
     │      <new_items> 含所有新提取 entities + concepts
     │      <existing_pages> 含所有候选页面
     │
     ├─ Layer 3: LLM 裁决 (WikiDeduplicationPrompt)
     │      合并原则:
     │      合并: 同义变体、缩写↔全称、汉译↔英文、空格差异
     │      不合并: 同类但不同物、父子范畴、版本变体
     │      输出: {"merges": {"entity/paxos算法": "entity/paxos", …}}
     │
     └─ Layer 4: validMerge() 校验
            类型前缀一致 (entity/entity, concept/concept)
            目标 slug 必须在候选集中（防幻觉）
```

##### reduceMerge（LLM 合并更新）

当新提取 item 命中已有页面时，调用 reduceMerge 增量合并：

```
LLM 接收:
  <page_metadata> (slug/title/type)
  <existing_summary>
  <existing_page_content>
  <new_extracted_information>

LLM 输出:
  SUMMARY: {一句话摘要}
  {合并后的完整 Markdown 内容}
```

##### Wiki ReAct Agent

基于 Eino `react.Agent` 的智能 Wiki 查询，配备 12 个 Wiki 工具（read_index, read_page, search, write_page, replace_text, rename_page, delete_page, flag_issue 等）。系统提示词要求：中文回答、必须先搜索再回答、必须引用来源（`[[slug|display name]]`）、发现新知识时自动写入。

##### Wiki Lint

自动检查引用缺失

##### Wiki Maintenance Agent

三步维护 — Ingest 新文档 → ReAct Agent 巡检修复 → 更新 Index 页面。支持定时调度（cron 表达式）。

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
3. **Bot 路由**：将消息推送给官方 Bot（Kafka `bot.event.ai`）、自建 Bot（WS）、第三方 Bot（Webhook）
4. **未读计数**：Redis 缓存递增，推送给在线用户
5. **已读回执**：消费 `conversation.read.updated` 事件，推送给其他成员

#### Fanout 扇出逻辑

**PushMessageNewWithUnread**（核心方法）：

1. 获取会话成员列表
2. 为所有接收者递增 Redis 未读计数
3. 区分在线/离线用户
4. 为每个在线用户推送 `message.new` 事件（附带个人未读数）
5. 为离线用户通过 FCM/APNS 发送推送通知
6. 推送给会话中的 Bot

**Bot 路由策略**：

| Bot 类型 | 路由方式 |
|---------|---------|
| Official / Self-Deployed | Kafka `bot.event.ai` topic → ai-bot-service |
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
| Wiki 生成 | AI 自动分析文档生成结构化 Markdown 知识页面，交叉引用 + Issue 追踪 + 版本管理 |
| MCP 集成 | MCP Server 管理 + 工具发现 + Bot 绑定 + 运行时工具调用 |
| 多端同步 | 同账号多设备消息同步，已读状态一致，离线消息补推 |
| 文件存储 | MinIO Presigned URL，文件/头像/附件统一管理 |
| 离线推送 | 在线 WebSocket 实时推送 + 离线 FCM/APNs 通知 |
| 服务治理 | 超时控制、限流、熔断、重试，etcd 配置热更新 |
| 可观测性 | Jaeger 分布式追踪、Prometheus 监控、Grafana 仪表盘、EFK 日志 |
| 部署运维 | k3s 集群，Helmfile 声明式，一键部署脚本 |

---

## 可能开展的活动

1. 为 Wiki融入向量层（当文件变多，索引太大时）
2. 强大 Bot 记忆层
3. 实现 Bot 市场与知识库市场

