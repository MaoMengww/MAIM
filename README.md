# MAIM

> **AI-Native Instant Messaging Platform** — 微服务架构 · LLM Bot 引擎 · RAG 知识库 · k3s 集群部署

---



## 项目概要

MAIM 是一个面向 AI 时代的即时通讯后端平台，将大语言模型深度融入实时通讯场景。系统由 **13 个 Go 微服务**构成，涵盖消息引擎、Bot 编排、知识库 RAG 检索、WebSocket 实时推送等完整业务域，通过 **Helmfile + k3s** 声明式部署。

- **定位**：IM 平台 + AI Bot 引擎 + 知识库 RAG，三者一体化
- **规模**：13 个 Go 微服务，gRPC + Kafka 通信
- **部署**：k3s (Kubernetes) 集群，Helmfile 统一编排 12 个基础设施 + 13 个应用服务

---

## 核心技术栈

| 层级       | 技术选型                                                     | 用途                                                     |
| ---------- | ------------------------------------------------------------ | -------------------------------------------------------- |
| **框架**   | go-zero · Gin · gRPC + Protobuf                              | 微服务 RPC、HTTP 网关、服务间通信                        |
| **数据**   | PostgreSQL  · Redis  · MinIO · Elasticsearch · Milvus · Neo4j | 关系存储、缓存、对象存储、全文搜索、向量检索、图谱可视化 |
| **消息**   | Kafka (KRaft) · WebSocket · APNs · FCM                       | 事件总线、实时推送、离线通知                             |
| **AI**     | CloudWeGo Eino · 多厂商 LLM                                  | AI 编排框架、模型路由、工具调用                          |
| **可观测** | OpenTelemetry · Jaeger · Prometheus · Grafana · EFK (Filebeat + ES + Kibana) | 分布式追踪、指标监控、日志聚合                           |
| **配置**   | etcd                                                         | 服务注册发现 + 配置中心热加载                            |
| **部署**   | k3s · Helm · Helmfile · Docker                               | 容器编排、声明式部署                                     |

### Wiki 知识库

基于karpathy提出的[llm-wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)构建的一种知识库的特殊模式，专为需要长期维护、持续更新的知识体系设计。核心理念是用 AI 自动化知识构建与维护的全流程。

#### Wiki 页面

- **页面类型**：entity（实体）、concept（概念）、summary（概述）、synthesis（综合论述）、comparison（对比）、index（索引）、log（变更日志）
- **Slug 路由**：`entity/transformer`、`concept/leader-election` 等层级标识，URL 直接访问
- **内部链接**：`[[entity/transformer]]` 语法创建页面间双向引用，`[[slug|显示名]]` 自定义显示
- **别名**：页面可设置同义词/别名，辅助检索和关联
- **版本追踪**：无论agent还是人工，每次编辑自动递增版本号,同时产生日志
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



### 后端架构与微服务

- **微服务治理** — go-zero 框架实现超时控制、限流、熔断、重试；etcd 服务发现与配置热加载（`MergeRemote`），无需重启即可更新配置
- **事件驱动架构** — Kafka 解耦消息生命周期：消息发送 → Inbox 写入 → 在线推送 / Bot 触发 / ES 索引 / 审计日志，消费组按功能拆分

### 实时通信

- **WebSocket 长连接网关** — 自研 ws-gateway 维护客户端长连接，支持在线状态广播、消息实时推送、多端同步
- **事件扇出与推送** — signaling-service 消费 Kafka 事件流，分流推送到在线用户（WebSocket）、Bot（Kafka/WebSocket/Webhook）、离线用户（APNs/FCM）；管理未读数缓存与已读回执广播
- **写扩散模式）** — 消息发送写入 Kafka，消费者写入各接收者 Inbox 表，解耦发送与递送，天然支持离线消息与多端同步

### AI 工程

- **Eino Graph 编排** — AI Bot 推理流程基于 CloudWeGo Eino 框架构建为可组合 Graph：回调节点 → 知识检索节点 → 上下文聚合节点 → LLM 调用 → 响应格式化，支持节点级指标采集
- **Bot 统一平台** — bot-platform 管理 Bot 生命周期（创建/更新/删除），配置系统提示词、模型参数、知识库绑定、回复策略；ai-bot-service 消费 Kafka 消息触发 Bot 回复生成
- **LLM 模型网关** — llm-gateway 统一代理多厂商 LLM，模型路由、请求转发、成本核算，对上层屏蔽接口差异
- **长记忆管理** — 跨会话上下文保持
- **RAG 知识库** — 文档摄入 → 分块 → Embedding 嵌入 → Milvus 向量存储；混合检索：向量 ANN + BM25 关键词匹配 + Reranker 重排序
- **Wiki 自动生成** — AI 分析上传文档自动生成结构化 Markdown 知识页面（摘要/实体/概念/索引/综合），支持内部交叉引用、Issue 追踪、版本管理；页面内容同步向量化到 Milvus 供检索
- **Neo4j 可视化** — 将 Wiki 页面节点与链接关系同步到 Neo4j 用于图谱可视化，仅作展示层，不影响检索与推理逻辑

### 可观测性与运维

- **分布式追踪** — OpenTelemetry gRPC 拦截器自动传播 Trace Context，Jaeger 可视化调用链
- **指标监控** — 每个服务暴露 `/metrics` 端点 (Prometheus)，Grafana 仪表盘监控 QPS、延迟、错误率、在线人数、Kafka 消费延迟
- **日志聚合** — Filebeat → Elasticsearch → Kibana，容器日志统一采集与检索
- **k3s 部署** — 通用 Helm Chart（`aim-service`）模板化 13 个服务，Helmfile 管理 staging/production 双环境；Docker 多阶段构建 → `ctr image import` 导入 containerd；启动时 `sed` 替换 localhost 为 K8s Service DNS



**Go · go-zero · Kafka · Milvus · Neo4j · k3s**

- 设计并实现 **13 个微服务**的分布式 IM 系统，涵盖用户、好友、消息、会话、文件、AI Bot、知识库、审计等完整业务域，服务间通过 gRPC + Kafka 事件总线通信
- 自研 **WebSocket 实时推送网关**，基于 Gin + gorilla/websocket，配合 signaling-service 实现消息扇出推送、在线状态广播、多端同步、离线 APNs/FCM 通知
- 构建 **AI Bot 执行引擎**，集成 CloudWeGo Eino Graph 编排框架，实现 LLM 推理流程的可组合节点图（回调解构 → 知识检索 → 上下文聚合 → 模型调用），支持流式输出与节点级指标采集
- 落地 **RAG 知识库 + Wiki 自动生成**双模式知识管理：文档解析管道（PDF/Markdown/Word/PPT等）→ 分块 → 向量化 → Milvus ANN 检索 + BM25 混合检索；AI 自动分析文档生成结构化 Markdown 知识页面（摘要/实体/概念/综合），支持交叉引用、Issue 追踪、版本管理
- 搭建完整可观测性体系：Jaeger 分布式追踪（OpenTelemetry）、Prometheus + Grafana 仪表盘（QPS / Bot 响应延迟 / Kafka 消费延迟 / 错误率）、EFK 日志聚合
- 实现 **k3s 一键部署**：通用 Helm Chart 模板化 13 个服务（Deployment + Service + Ingress），Helmfile 声明式编排 12 个基础设施（PostgreSQL / Redis / Kafka / MinIO / Milvus / Neo4j / Elasticsearch / Jaeger / Prometheus / Grafana / Kibana / Filebeat）

---

## 系统能力矩阵

| 能力域    | 实现                                                         |
| --------- | ------------------------------------------------------------ |
| 即时消息  | 文本/图片/文件/语音消息，单聊与群聊，消息状态追踪            |
| 消息管理  | 发送/接收/撤回/编辑/引用回复，全文搜索 (Elasticsearch)       |
| 社交关系  | 好友增删/分组/备注/黑名单，群创建/邀请/踢出/转让/公告        |
| AI Bot    | @Bot 对话，多厂商模型路由，多 Bot 会话协作，流式输出         |
| 知识库    | 多格式文档上传，RAG 向量检索问答，混合检索 + Reranker 重排序 |
| Wiki 生成 | AI 自动分析文档生成结构化 Markdown 知识页面，交叉引用 + Issue 追踪 + 版本管理 |
| 多端同步  | 同账号多设备消息同步，已读状态一致，离线消息补推             |
| 文件存储  | MinIO ，文件/头像/附件统一管理                               |
| 离线推送  | 在线 WebSocket 实时推送 + 离线 FCM/APNs 通知                 |
| 服务治理  | 超时控制、限流、熔断、重试，etcd 配置热更新                  |
| 可观测性  | Jaeger 分布式追踪、Prometheus 监控、Grafana 仪表盘、EFK 日志 |
| 部署运维  | k3s 集群，Helmfile 声明式                                    |

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
│   ├── llm-gateway/                  # LLM 模型网关 (多厂商路由)
│   ├── bot-platform/                 # Bot 管理平台 (创建/配置/删除)
│   ├── ai-bot-service/               # AI Bot 执行引擎 (Eino Graph + 记忆)
│   ├── knowledge-base/               # RAG 向量检索 + Wiki 自动生成 (Milvus + Neo4j 可视化)
│   ├── signaling-service/            # 事件扇出与推送 (Kafka → WS/APNs/FCM/Bot路由)
│   └── audit-service/                # 审计日志服务
│
├── deploy/
│   ├── docker/                       # Dockerfile (多阶段构建) + 入口脚本
│   ├── k3s/                          # k3s 部署清单
│   │   ├── helmfile.yaml             # 集群级 Helmfile (基础设施 + 应用服务)
│   │   ├── charts/aim-service/       # 通用服务 Helm Chart (Deployment + Service + Ingress)
│   │   ├── infrastructure/           # 基础设施 Helm Values (PG/Redis/Kafka/MinIO/...)
│   │   ├── jobs/                     # 一次性 Job (etcd 配置初始化)
│   │   ├── values/staging/           # 按环境的服务配置
│   │   └── scripts/                  # 一键构建/部署脚本
│   └── monitoring/                   # Grafana 仪表盘 + Prometheus 告警规则
│
├── idl/                              # Protobuf IDL 定义 (14 个服务)
├── pkg/                              # 共享工具包
│   ├── errors/                       # 业务错误码 (CodeNotFound/Unauthorized/Forbidden/...)
│   ├── jwt/                          # JWT 令牌签发与验证
│   ├── kafka/                        # Sarama Kafka 生产/消费者封装
│   ├── configcenter/                 # etcd 配置中心客户端 (MergeRemote)
│   ├── interceptor/                  # gRPC 拦截器 (Trace/Metrics/Auth/Logging)
│   ├── trace/                        # OpenTelemetry 初始化
│   ├── snowflake/                    # 分布式 Snowflake ID 生成
│   ├── metrics/                      # Prometheus 指标注册
│   └── ...
│
├── migrations/postgres/              # 数据库 Schema 迁移 SQL
├── go.mod                            # Go 1.25.0, go-zero v1.10.1
└── docker-compose.yml                # 本地开发中间件编排
```

## 可能开展活动

1.为wiki增加向量层(当文件变多，索引太大时)

2.强大bot记忆层

3.实现bot市场与知识库市场

