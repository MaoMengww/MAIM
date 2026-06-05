# MAIM — AI 即时通讯系统

# MAIM - AI 驱动的即时通讯系统

> AIM 是一个面向多人在线的即时通讯系统，内置可自部署的 AI 助手，将大模型能力深度集成到聊天场景中，实现“通讯 + AI”的深度融合。

## 功能清单

### 基础要求

- [x] 基于 TCP/WebSocket 的实时消息收发，支持单聊、群聊、广播消息；消息类型支持文本、图片、文件、语音。
- [x] 消息已读回执、输入状态提示、在线状态管理；消息本地存储与云端漫游，支持按关键词、时间范围搜索历史消息。
- [x] 好友关系管理：添加、删除、分组、备注。
- [x] 群组管理：创建、邀请、踢出、禁言、转让群主、群公告。
- [x] 内置聊天 Bot，接入多家厂商模型接口，用户可直接 @Bot 对话；支持用户通过 OpenAPI 自行部署机器人，也可使用 AIM 平台内置 Bot，这里有两个选择，一个是自己提供 API Key，也可以使用平台的模型，要求需要做好计费管理。
- [x] 一键总结群聊/单聊历史消息，生成要点摘要与待办提取；根据上下文生成回复候选，用户可一键选用。
- [x] 分布式架构，不限制框架，需要合理划分模块，要求至少需要使用 docker 打包部署。

### 进阶要求

- [x] RAG 知识库 Bot：用户可上传多种格式的文档（pdf、md、doc、ppt）构建私有知识库，Bot 可基于知识库回答问题。
- [x] MCP 工具集成：Bot 可调用外部工具（天气查询、代码执行、Web 搜索等），扩展能力边界。
- [x] 多 Bot 协作：支持配置多个不同人设/能力的 Bot，按场景自动路由或由用户指定。
- [x] 记忆能力：Bot 记住用户偏好与历史交互，跨会话保持上下文。
- [x] 消息引用与回复、限时撤回与编辑、离线推送与上线同步。
- [x] 多端消息同步：同一账号多端登录，消息实时同步，已读状态一致。
- [ ] 实时多语言翻译；接入模型对消息内容进行实时审核与过滤。
- [x] 集成主流可观测性组件，包括分布式日志、链路追踪；集成 Prometheus + Grafana 监控仪表盘：在线人数、消息吞吐量、Bot 响应延迟。
- [x] 提供 CLI 客户端（TUI 界面），支持 Markdown 渲染与流式输出；可选提供 Web 前端或移动端客户端。
- [x] 服务治理，包括超时，限流，熔断，重试，降级等常见治理
- [ ] 部署，最好能部署到服务器进行展示

## 技术栈

### 后端

| 技术                   | 用途                                                  |
| ---------------------- | ----------------------------------------------------- |
| go-zero v1.10          | 微服务框架（RPC + 服务治理: 限流/熔断/降级/超时控制） |
| Gin                    | HTTP 框架                                             |
| gRPC + Protobuf        | 服务间通信                                            |
| PostgreSQL 16          | 数据库                                                |
| Redis                  | 缓存                                                  |
| etcd                   | 服务发现 + 配置中心                                   |
| Kafka                  | 消息队列                                              |
| MinIO                  | 对象存储（文件 / 图片 / 头像）                        |
| Milvus                 | 向量数据库（RAG Embedding 存储与检索）                |
| Elasticsearch          | 消息全文搜索 / 应用日志 (EFK)                         |
| Neo4j                  | 图数据库（Wiki）                                      |
| gorilla/websocket      | WebSocket 实时推送                                    |
| APNs、FCM              | 离线推送                                              |
| Eino (CloudWeGo)       | AI 编排框架（LLM 调用链、Agent、MCP 工具）            |
| OpenTelemetry + Jaeger | 分布式追踪                                            |
| Prometheus + Grafana   | 指标监控                                              |
| GORM                   | ORM 数据访问                                          |
| zap                    | 结构化日志                                            |



## 系统架构

### 架构概览

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
   │                  微服务                              │
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

### 微服务列表

| 服务                     | 端口                    | 角色                                                         | DB Schema   |
| ------------------------ | ----------------------- | ------------------------------------------------------------ | ----------- |
| **gateway**              | HTTP 8080               | API 网关 / BFF                                               | 无          |
| **user-service**         | 50051                   | 用户认证、资料、多端会话管理                                 | `user`      |
| **friend-service**       | 50052                   | 好友关系、分组、黑名单                                       | `friend`    |
| **message-service**      | 50053                   | 消息收发、ES 索引、搜索、收件箱                              | `msg`       |
| **file-service**         | 50054                   | 文件存储（MinIO 预签名 URL）                                 | `file`      |
| **conversation-service** | 50055                   | 会话、成员、权限、禁言                                       | `conv`      |
| **llm-gateway**          | 50056                   | LLM 代理、模型注册与路由、计费                               | `llm`       |
| **knowledge-base**       | 50057                   | RAG 知识库、文档解析流水线、Wiki                             | `knowledge` |
| **audit-service**        | 50059                   | 审计日志 、LLM 内容审核                                      | `audit`     |
| **signaling-service**    | 50061 (gRPC)            | 推送调度（在线 WS + 离线 FCM/APNs）、在线状态、消息扇出、未读计数、通知管理 | `notify`    |
| **ws-gateway**           | 50060 (gRPC), 8081 (WS) | WebSocket 长连接、Session/设备管理、Presence 订阅广播、跨设备同步 | 无          |
| **ai-bot-service**       | 50062                   | AI Bot 对话、MCP 工具调用、记忆、总结/翻译/回复候选          | `bot`       |
| **bot-platform**         | 8085 (HTTP)             | Bot CRUD、Webhook、MCP 服务器管理                            | `bot`       |

### 实时推送链路

```
Kafka 事件 → signaling-service (消费者)
                     ├── 在线用户 → ws-gateway (gRPC Push API) → WebSocket → 实时推送
                     └── 离线用户 → FCM (Android/Web) / APNs (iOS) → 移动推送通知
```

### Wiki 知识库

Wiki 是知识库的特殊模式，专为需要长期维护、持续更新的知识体系设计。核心理念是用 AI 自动化知识构建与维护的全流程。

#### Wiki 页面

- **页面类型**：entity（实体）、concept（概念）、summary（概述）、synthesis（综合论述）、comparison（对比）、index（索引）、log（变更日志）
- **Slug 路由**：`entity/transformer`、`concept/leader-election` 等层级标识，URL 直接访问
- **内部链接**：`[[entity/transformer]]` 语法创建页面间双向引用，`[[slug|显示名]]` 自定义显示
- **别名**：页面可设置同义词/别名，辅助检索和关联
- **版本追踪**：每次编辑自动递增版本号,同时产生日志
- **来源引用**：关联原始文档，可在页面中追溯信息来源

#### AI 文档导入流水线（IngestPipeline）

上传文档后自动触发：

```
文档上传 → 解析文档内容 → LLM 分析生成知识页面
  → 写入 Neo4j  → 更新 index 索引页
```

LLM 会根据文档内容自动创建实体、概念、概述、对比等多种类型的页面，并建立页面间的引用关系。

#### AI 与人工共同维护（Maintenance）

通过定时任务或手动触发执行ai自动维护：

1. **新文档导入**：检测未覆盖的文档，自动创建对应的 Wiki 页面
2. **ReAct Agent 巡检**：基于 Eino 框架构建的 ReAct Agent
3. **Issue 追踪**：Agent 发现的问题写入 `wiki_page_issues` 表，前端可查看、解决、忽略
4. **变更日志**：每次维护生成结构化日志，以 `log` 类型页面存储，时间线展示

同时允许用户手动编辑wiki页面实现人工ai共同长期维护

#### Wiki 智能搜索

基于 LLM 的语义搜索：读取 index 索引页内容，由 LLM 多步查询，返回相关内容。不需要向量 Embedding。

谈论时完善知识库: 但在谈论中涉及新知识，新论述，LLM可以自动更新创建或删除wiki页面

#### 架构位置

```
知识库（KB）可选择两种模式：
├── RAG 模式 → 向量检索 + 标准文档问答
└── Wiki 模式 → AI 文档导入 → 图谱 → 自动维护 → Issue 追踪
```

### 事件驱动架构

关键 Kafka 事件及消费者：

| 事件                                                         | 消费者                                                       |
| ------------------------------------------------------------ | ------------------------------------------------------------ |
| `message.created` / `.edited` / `.recalled` / `.deleted`     | signaling（推送）、ai-bot（@Bot 回复）、audit（审核）、message（收件箱写入 + ES 索引） |
| `conversation.*`                                             | signaling（推送成员变更）                                    |
| `knowledge.parsing` / `.chunking` / `.embedding` / `.ready` / `.failed` | 知识库文档处理流水线                                         |
| `ai.thinking` / `.processing`                                | 前端实时事件推送                                             |
| `bot.event.*`                                                | Bot 流式输出事件                                             |
| `broadcast.created`                                          | 全量推送                                                     |
| `wiki.ingested` / `.issue.flagged` / `.maintained`           | Wiki 文档管理与维护                                          |

### 项目结构

```
AIM/
├── app/                    # 13 个微服务
│   ├── gateway/            # API 网关 (Gin BFF)
│   ├── user-service/       # 用户服务
│   ├── friend-service/     # 好友关系
│   ├── conversation-service/ # 会话管理
│   ├── message-service/    # 消息引擎
│   ├── file-service/       # 文件存储
│   ├── llm-gateway/        # LLM 代理
│   ├── knowledge-base/     # 知识库 + Wiki
│   ├── ai-bot-service/     # AI Bot
│   ├── bot-platform/       # Bot 管理平台
│   ├── ws-gateway/         # WebSocket 网关
│   ├── signaling-service/  # 推送调度
│   └── audit-service/      # 审计
├── web-client/             # Web 前端 (React)
├── idl/                    # Protobuf 接口定义
├── pkg/                    # 共享工具包
├── migrations/postgres/    # 数据库初始化 SQL
├── deploy/                 # 部署配置
```

### 各服务内部结构

所有 go-zero RPC 服务遵循统一布局(除knowledgebase和ai)：

```
app/<service>/
├── etc/<service>.yaml              # 本地配置
├── internal/
│   ├── config/config.go            # 配置结构体
│   ├── svc/servicecontext.go       # 依赖注入（DB / Redis / Kafka / gRPC client）
│   ├── logic/<server>/xxxLogic.go  # 业务逻辑
│   ├── server/<server>/            # gRPC server 实现
│   ├── repo/                       # 数据访问层
│   └── model/                      # GORM 模型
├── pb/<service>/                   # Protobuf 生成代码
├── client/                         # 作为 RPC client 的辅助包装
└── main.go
```

Gateway 、

```
app/gateway/
├── etc/gateway.yaml
├── internal/
│   ├── config/          # 配置
│   ├── router/          # Gin 路由注册
│   ├── handler/         # HTTP handler（解析请求 → 调用 gRPC → 返回响应）
│   ├── middleware/      # 认证 / 限流 / 日志 / 追踪 中间件
│   └── grpc/            # gRPC 客户端连接管理
└── main.go
```



### 统一Bot平台，允许多类型bot

#### Bot 类型

| 类型           | 说明                                 | 适用场景        |
| -------------- | ------------------------------------ | --------------- |
| **官方 Bot**   | 平台内置模板（智能问答、知识库回答） | 开箱即用        |
| **自部署 Bot** | 自定义模型和提示词                   | 个性化自定义bot |
| **第三方 Bot** | 外部服务通过 Webhook/WebSocket 接入  | 完全自定义逻辑  |

#### 第三方bot接入模式

##### Webhook 模式 — 异步回调

AIM 向 Bot 的 `callback_url` 发送 HTTP POST 推送事件，Bot 处理完通过统一端点回复。

##### WebSocket 模式 — 实时双向

Bot 主动连接 AIM 的 ws-gateway，通过长连接接收实时推送并回复消息。
