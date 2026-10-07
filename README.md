# AIM

> **AI-Native Instant Messaging Platform** — IM 平台 · LLM Bot 引擎 · RAG 知识库 · k3s 集群部署

---

## 项目概要

AIM 是一个面向 AI 时代的即时通讯后端平台，将大语言模型深度融入实时通讯场景。当前由 **8 个 domain 服务、10 个业务工作负载** 构成；Bot 控制面与运行时同属 bot-service，在线检索与入库同属 knowledge-base，两者分别保留独立工作负载与副本。realtime-service 统一长连接、连接登记、定向投递与离线推送，通过 **Helmfile + k3s** 声明式部署。

- **定位**：IM 平台 + AI Bot 引擎 + 知识库 RAG，三者一体化
- **规模**：8 个 Go 服务，gRPC + Kafka 通信；账号与好友关系同属 user-service，会话聚合、消息与扇出同属 message-service
- **部署**：Docker Compose 或 k3s，平台 DNS 发现，单一 YAML 模板 + 环境变量配置
- **前端**：`web-client/`（React + Vite），只经 gateway REST 与 realtime WebSocket 访问后端

---

## 架构全景

```
Web 前端 ── REST ──▶ gateway ── gRPC ──▶ user / message / file
    │                                      bot / knowledge-base / llm-gateway
    │ WebSocket
    ▼
realtime-service ◀── Kafka delivery.requested ◀── message / bot / knowledge-base
    │
    ├── Redis 连接登记：用户 + 设备 → 实例 + generation（独立 TTL）
    ├── Redis rt:node:{instance} → 持有连接的实例 → WebSocket
    └── FCM / APNs 离线提醒

共享基础设施：PostgreSQL / Redis / Kafka / MinIO / Milvus / ES / Neo4j
```

### 消息流转全景

```
客户端 → gateway → message-service
    → [本地事务] 会话权限 + seq + 消息 + outbox + 最新消息
    → OutboxDispatcher → Kafka message.created
        ├── 收件箱写扩散 + 成员解析 + 权威未读 → delivery.requested
        │      → realtime 单消费组 → Redis 实例专用通道 → 用户各设备 WS
        ├── ES 搜索索引
        └── bot-runtime → Agent / MCP / Bot 回复（再次进入消息写入链路）

推送 best-effort；断线、实例故障或推送缺口通过按会话 seq 增量补拉恢复。
```

---

## 服务与端口

8 个 domain 服务对应 8 个可独立部署的进程；其中 bot 与 knowledge 各有一个同镜像的**第二角色工作负载**，合计 10 个工作负载。Compose 服务名、Helmfile release 名（`aim-<service>`）与工作负载一一对应。

| domain 服务 | Compose / Helm 工作负载 | gRPC | HTTP | metrics |
|------|------|------|------|------|
| user-service | user-service | `50051` | — | `9101` |
| message-service | message-service | `50053` | — | `9102` |
| file-service | file-service | `50054` | — | `9105` |
| llm-gateway | llm-gateway | `50056` | — | `9107` |
| knowledge-base | knowledge-base（`-role online`） | `50057` | — | `9108` |
| knowledge-base | knowledge-ingest（`-role ingest`） | — | `9118`（`/health` + `/metrics`） | `9118` |
| bot-service | bot-service（`-role control`） | `50058` | — | `9109` |
| bot-service | bot-runtime（`-role runtime`） | `50058` | — | `9119` |
| realtime-service | realtime-service | `50059` | `8081`（WebSocket + `/readiness`） | `9103` |
| gateway | gateway | — | `8080`（REST + `/metrics` + `/health`） | — |

**端口规则**（见 ADR-0008）：

- 业务域 gRPC 占 `5005x`，业务域 metrics 占 `91xx`；两者都取该族当前未占用的下一个值，**不复用**合并后释放的空号。
- 同一 domain 的第二角色 metrics = 基准 `+10`：knowledge-ingest `9118`（在线 `9108`）、bot-runtime `9119`（控制面 `9109`）。
- 面向客户端的 HTTP/WS 只有两个：gateway `8080`、realtime `8081`；其余工作负载不对外暴露业务 HTTP。
- 没有对外业务 HTTP 的工作负载把 `/health` 与 `/metrics` 放在**同一个监听**上，不再单开健康检查端口；knowledge-ingest 因此只有 `9118` 一个 HTTP 端口。

### 数据 schema

每个拥有持久化状态的 domain 对应一个同名 PostgreSQL schema（ADR-0002），通过各服务 DSN 的 `search_path` 指定；显式带 schema 前缀的模型（如 `messaging.conv_bots`、`realtime.notifications`）在代码中写全名。

| schema | 归属 |
|--------|------|
| `messaging` | message-service：会话与群组、消息、收件箱、seq、outbox |
| `realtime` | realtime-service：通知与设备令牌 |
| `user` | user-service：账号、资料、好友关系与黑名单 |
| `bot` | bot-service：Bot 配置、MCP、记忆与摘要 |
| `file` | file-service：文件元数据 |
| `knowledge` | knowledge-base：知识库、文档与分块 |
| `llm` | llm-gateway：模型注册与计费 |

---

## 项目结构

```
AIM/
├── app/                              # 8 个 domain 服务
│   ├── gateway/                      # HTTP 入口 (Gin, JWT + 限流 + 协议转换)
│   ├── user-service/                 # 账号、资料、好友关系、分组与黑名单
│   ├── message-service/              # 消息域 (会话与群组、消息、收件箱、seq、已读未读)
│   ├── file-service/                 # 文件管理 (MinIO Presigned URL)
│   ├── llm-gateway/                  # LLM 模型网关 (多厂商路由 + 计费 + 限流)
│   ├── knowledge-base/               # RAG 在线检索与入库，online/ingest 独立工作负载
│   ├── bot-service/                  # Bot 控制面 + Eino Agent/MCP/记忆，control/runtime 独立工作负载
│   └── realtime-service/             # 长连接、连接登记、定向投递、在线状态与离线推送
│
├── deploy/
│   ├── docker/                       # Dockerfile (多阶段构建) 与镜像构建脚本/配置
│   ├── k3s/                          # k3s 部署清单
│   │   ├── helmfile.yaml             # 集群级 Helmfile (基础设施 + 10 个工作负载 release)
│   │   ├── charts/aim-service/       # 通用服务 Helm Chart
│   │   ├── infrastructure/           # 基础设施 Helm Values
│   │   ├── values/staging/           # 每 domain 一份 values (共 8 份)
│   │   └── scripts/deploy.sh         # 一键构建/部署脚本
│   └── monitoring/                   # Grafana 仪表盘 + Prometheus 告警规则
│
├── idl/                              # Protobuf IDL 定义 (8 个 .proto)
├── pkg/                              # 共享工具包 (16 个子包)
├── migrations/postgres/              # PostgreSQL Schema 迁移 SQL
├── tests/e2e/                        # 端到端验收 harness (run.py + 验收客户端/外部 provider)
├── web-client/                       # React 前端 (Vite)
├── go.mod                            # Go 1.25.0, go-zero v1.10.1
└── docker-compose.yml                # 本地开发编排 (中间件 + 应用服务)
```

### go-zero RPC 服务统一布局

每个 go-zero RPC 服务遵循统一目录模式：

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

配置是「**单一 YAML 模板 + `${ENV}` 插值**」：每个服务只保留 `app/<service>/etc/` 的一份模板，入口统一通过 `conf.MustLoad(..., conf.UseEnv())` 加载；Compose 的 `x-aim-env` 与 Helm Chart 的 `environment` 提供部署环境，模板不再由启动脚本或 `sed` 改写，也没有远程配置中心（ADR-0003）。服务发现用平台原生 DNS，不使用 etcd。

- 本地直接启动：参考 `.env.example` 设置环境变量，再执行 `go run ./app/<service> -f app/<service>/etc/<filename>.yaml`；文件名以各服务 `etc/` 为准。
- Compose：准备 `.env` 中的 Postgres、MinIO、Neo4j、`JWT_SECRET`、`AIM_ENC_KEY` 与 `INGEST_EMBEDDING_TOKEN`，执行 `docker compose up -d --build`。`AIM_ENC_KEY` 必须是 base64 编码的 32 字节密钥。改变环境变量后用 `docker compose up -d --force-recreate <service>` 重建容器，不支持热更新。
- k3s：在环境 values（`deploy/k3s/values/staging/`，每 domain 一份）中覆盖 Chart 的 `environment`，通过 Helmfile 更新；环境变量改变 Pod template 后触发滚动更新。后端 RPC Service 为 Headless，`dns:///aim-<service>:<port>` 返回各副本地址；gateway 保持普通 ClusterIP。
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

### 实体身份与业务序号

共享合同见 [ADR-0010](docs/adr/0010-entity-identity-and-ordering.md) 和 [ID 系统规格](.scratch/id-system/spec.md)。实体由所属 domain 调用 `pkg/identity.New()` 生成 UUIDv7，PostgreSQL 使用原生 `uuid`；HTTP/WS、RPC 和事件引用使用标准小写、带连字符的 UUID 字符串。公开边界用 `identity.Validate` 拒绝十进制身份、空字符串和全零 UUID；`Normalize` 仅供显式规范化，不是兼容协议入口。客户端发送动作另用 UUIDv4 提交键，重试复用；设备、请求、连接 generation、JWT jti、stream 和第三方标识保持专用职责。

会话 `seq`、已读序号、收件箱 `position` 和发布序号不是实体身份。内部使用 `int64`/`BIGINT`，范围为 `0..9007199254740991`，真实追加从 1 开始。`pkg/sequence` 提供边界校验与不环绕的下一位置计算；所属 domain 必须在业务事务内分配和持久化。HTTP/WS 的 protobuf 载荷统一经过 `pkg/protocol.Marshal/Unmarshal`：只将明确标注 `common.safe_sequence` 的字段序列化为 JSON number，不全局转换时间、数量等 `int64`。`common.entity_id` 和 `common.submission_key` 明确字段语义，不依赖 `_id` 后缀猜测。

可选实体引用使用 protobuf presence 和数据库 `NULL`。更新省略引用表示保持，`clear_<字段名>=true` 表示解除，同时设置与清除必须拒绝。对象所有权通过 `owner_type=platform|user` 表达：平台没有 `owner_id`，用户所有必须关联有效用户 UUID；该合同不授予平台管理权限。

第 02 票的用户、关系和通知链路采用上述合同。JWT 分别携带用户 UUID、稳定专用 `device_id`、设备记录 UUID `session_id` 和专用 `jti`；gateway 与 realtime 经 user 的真实 `ValidateToken` 校验记录/撤销状态。撤销后同设备重新登录创建新 session，旧 token 不复活。好友解除分组发送 `clear_group_id=true`，省略保持；通知引用同时提供 `reference_type` 与 UUID `reference_id`，无引用时二者缺失。presence 使用 `presence.subscribe` / `presence.unsubscribe` 与 `presence.state` 的 `online` 布尔值。

新环境应用 `migrations/postgres/000_uuid_identity.sql` 及其后续 UUID 合同迁移，服务通过 `database.RunMigrations` 消费同一嵌入迁移集；旧迁移的有效表、索引和约束已合并。`001_inbox_checkpoint_provenance.sql` 保留已分配同步位点的来源，使已读记录合并后仍可续读；不推测此前已经丢失的位点。旧整数结构/旧迁移记录会显式失败，不自动映射或清空。第 01 票只交付共享宽改型：业务调用链迁移归第 02–07 票，全仓绿色与协调重建归第 08 票；本阶段不能启动完整新系统，也未清理现有开发数据。


---

## 部署

### Docker Compose（本地开发/联调）

`docker-compose.yml` 同时编排基础设施与应用服务：Postgres、Redis、MinIO、Kafka（含 `init-kafka-topics` 幂等建 topic）、Milvus、Elasticsearch、Neo4j、otel-collector、Jaeger、Prometheus、Kibana、Grafana，以及 10 个工作负载（`user-service`、`message-service`、`file-service`、`llm-gateway`、`knowledge-base`、`knowledge-ingest`、`bot-service`、`bot-runtime`、`realtime-service`、`gateway`）。

- MinIO 社区版已改为[仅发布源码](https://github.com/minio/minio#source-only-distribution)，Compose 从固定版本源码构建 `aim-minio`，不再拉取不可用的 `minio/minio:latest`；首次构建需要访问 Go module proxy。
- 消费者服务等待 `init-kafka-topics` 成功后再启动，避免空环境首次启动时因 topic 不存在退出。
- 应用配置全部来自 `.env` 与环境变量；变更后需要 `--force-recreate`，不支持热更新。

```bash
cp .env.example .env   # 填写 Postgres/MinIO/Neo4j 凭据、JWT_SECRET、AIM_ENC_KEY、INGEST_EMBEDDING_TOKEN
docker compose up -d --build
```

### k3s（集群部署）

声明式部署基于根目录 `Makefile` 之外的 `deploy/k3s/helmfile.yaml` 与 `deploy/k3s/scripts/deploy.sh`：

```bash
./deploy/k3s/scripts/deploy.sh all      # build 镜像 + import 到 containerd + 部署基础设施与应用
```

- `helmfile.yaml` 一次声明基础设施、监控、日志与全部 10 个工作负载 release（`aim-<service>`）。
- `deploy/k3s/values/staging/` 按环境保存 values，**每个 domain 一份共 8 份**：`user-service`、`message-service`、`file-service`、`llm-gateway`、`knowledge-base`、`bot-service`、`realtime-service`、`gateway`。
- `bot-runtime` 与 `knowledge-ingest` **不再各自持有 values 文件**（原 `bot-runtime.yaml`、`knowledge-ingest.yaml` 已删除）：helmfile 用同域那份 values + 显式覆盖实例化——`aim-bot-runtime` 基于 `bot-service.yaml` 覆盖 `role=runtime`、`metricsPort=9119`、`config.name`；`aim-knowledge-ingest` 基于 `knowledge-base.yaml` 覆盖 `role=ingest`、`httpPort`/`metricsPort`/`probes`=`9118` 与 `secretEnv`。两者仍可独立设置 `replicaCount`，独立伸缩。
- 后端 RPC Service 为 Headless（`clusterIP: None`），配合 `dns:///aim-<service>:<port>` 做发现；gateway 保持普通 ClusterIP。
- 生产部署需创建 Helm 引用的 `aim-ingest-embedding` Secret（`token` key），并设置独立随机令牌。

---

## 端到端验收

前置条件：Python 3、Docker Engine、支持 `--wait` 的 Docker Compose 插件及 Buildx。验收通过 gateway REST 与 WebSocket 驱动真实服务；没有公开通知创建入口时直接调用现有 domain gRPC，不新增测试 API。不替换内部 RPC、Kafka 或数据库。

```bash
python3 tests/e2e/run.py --scenario all --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario user-identity --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario messaging --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario attachments --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --cross-instance --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario stage-p3 --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario stage-p5 --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario stage-p6 --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario user-sync --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario broadcasts --artifacts /tmp/aim-e2e-artifacts
```

每次使用独立 Compose project、网络与数据卷，默认无宿主端口映射。所选场景全部依赖就绪后才施加流量；realtime 的两个实例分别可寻址，但共用一个 Kafka 消费组。成功或失败后均清理该 project 的容器、数据卷与本次构建的镜像，不清理共享 BuildKit 缓存；`--artifacts` 保留诊断日志。显式 `--keep-environment DIR` 保留本轮私有环境，`environment.json` 记录清理命令；配合 `--browser-access` 或 `--database-access` 只发布随机回环端口。浏览器验收须将保留环境的 realtime 心跳/登记 TTL 恢复生产配置（30/90 秒），不能让 Web 的 25 秒心跳运行于故障场景的 1/6 秒配置。

默认 `all` 检查关系链、同实例与 A/B 跨实例双向投递；`--scenario` 可选择单个场景。P6 已用连接登记取代旧的单实例 gRPC 推送目标，同实例与跨实例走同一条 Redis 定向投递路径。

`user-identity` 只启动 user、gateway、双 realtime 及其真实中间件，复用 relationships 并验证注册/登录/刷新/资料/设置、跨账号拒绝、通知持久化/跨实例 WS/离线列表、设备稳定重连和撤销后旧 token 不复活。它不证明尚未迁移的其它 domain 可用；全仓和完整栈验收由第 08 票完成。

`messaging` 只启动 user、message、gateway、双 realtime 与 PostgreSQL/Redis/Kafka/Elasticsearch 等真实依赖，复用 conversation-unread、broadcasts、user-sync、同实例 A/B 和跨实例场景。覆盖丢确认后的原结果重试、并发唯一提交、异义冲突、发送者/会话隔离、权限恢复、历史序号分页、个人删除与搜索；不替换尚未迁移的 Bot、文件或知识业务。

`attachments` 启动 user、message、file、gateway、双 realtime 与真实 MinIO/PostgreSQL/Redis/Kafka/Elasticsearch。验证真实 PUT/确认/下载的字节一致性、四种附件的 UUID 在确认/WS/历史/离线收件箱中一致、同键重试与换文件冲突（包括文件删除后的原提交回放）、回复与文本搜索引用、私有对象匿名访问拒绝，以及消息删除、单个/批量文件删除的对象隔离。没有 Bot 或知识 service 替身。普通聊天上传显式使用既有公开访问级别；私有附件仅上传者读取，未新增会话成员授权策略。文件实体身份与对象键、临时签名 URL 各司其职；消息 domain 分配提交结果，gateway 不拥有附件提交判定。

使用 `attachments --keep-environment DIR --browser-access` 时，清单额外记录真实 MinIO 的随机回环端口。容器验收默认使用 `minio:9000`；浏览器验证前将该隔离环境 file-service 的 `MINIO_PUBLIC_ENDPOINT` 设置为清单的 MinIO 地址并重建该容器。SDK 对浏览器可见 host 本身签名，不在签名后替换 host。公开对象只允许 `public/*` 匿名读取，私有对象通过鉴权后的有效期下载 URL 访问。

`user-sync` 检查空流重建、单个位点跨会话分页、消息正文与账号隔离、新设备最近历史及置顶/免打扰设置、未知位点重建和续增量；同账号两设备个人删除他人消息后同步隐藏，原发送者仍可读取，搜索/历史/回复摘要/会话预览及新设备重建不泄露正文。过期回收后不复活与并发未提交写入窗口由真实 PostgreSQL 的集成回归覆盖。

`broadcasts` 检查 `user/group/all` 范围、并发首次广播只创建一个用户系统会话、后续复用与递增 `seq`、两副本上的普通 `message.new` 投递，以及离线账号经用户收件箱增量/重建和会话历史读取广播。广播无需客户端专用事件分支。

`stage-p5` 在 P4 场景上追加 Bot 配置/令牌、真实网络 MCP 工具发现与调用、Kafka 回复及 WS/REST 精确内容核对，以及四个大文档并发入库期间的检索和失败隔离。OpenAI/MCP 外部协议由 `e2e-provider` 提供；AIM 内部 RPC、Kafka、PostgreSQL、Milvus 不替换。在线查询每次硬截止 5 秒；入库总截止 10 分钟，容纳默认 20 RPM 预算，不提高配额或缩小文档负载。

`stage-p6` 覆盖 P3/P4 主链路、跨实例 Bot 回复和流式输出、发送方其它设备回显、非成员隔离、撤回/编辑/删除与未读同步。验收通过 `presence.query` 查询设备所在实例，并真实执行强制终止、TTL 自然失效、TTL 内投递失败负反馈、重启恢复，以及 readiness 摘流量后的多连接平滑排空；故障期间遗漏的消息按用户同步位点补拉。`--artifacts` 保留检查点与服务日志。

Bot 使用同一镜像：`bot-service -role control` 提供外部入口，运行时调用转发到 `BOT_RUNTIME_ADDR`；`bot-runtime -role runtime` 消费消息并执行 Agent，直接读取同域配置，不依赖控制面 RPC。两个工作负载分别暴露 9109/9119 指标，可独立调整副本。Knowledge 使用同一镜像：`-role online` 仅提供查询/管理 RPC，`-role ingest` 仅消费上传事件，只监听一个 `9118` HTTP 端口同时服务 `/health` 与 `/metrics`。Compose 与 Helm 都把两者作为独立工作负载配置资源，可单独设置 `replicaCount`；Helm 侧不另建 values 文件，由同域 values 覆盖生成。

Embedding 的 online/ingest RPM 与并发预算通过 Redis 跨副本共享且相互隔离；仅入库 worker 持有 `INGEST_EMBEDDING_TOKEN`，llm-gateway 校验凭据后才允许使用入库池。在线请求超额立即拒绝，入库任务在 RPC 截止时间内等待可用配额；Redis 故障不放行。生产部署需创建 Helm 引用的 `aim-ingest-embedding` Secret（`token` key），并设置独立随机令牌；不要把令牌注入在线检索进程。

---

## 服务详解

### 消息引擎 — 不重复·不丢失·不乱序

#### SendMessage 完整链路

```
客户端 → Gateway REST → message-service gRPC
  → [事务] messages + outbox_events + seq (PostgreSQL)
  → OutboxDispatcher (后台轮询)
  → Kafka message.created → 消息域收件箱与扇出 → delivery.requested
  → realtime → Redis 实例专用通道 → WebSocket（best-effort；用户同步位点补拉兜底）
```

#### 不重复（持久化 effectively-once 语义）

**三层防重机制**：

| 层级 | 机制 | 实现方式 |
|------|------|---------|
| 发送端 | UUIDv4 提交键 + 数据库唯一约束 | `(sender_id, conv_id, client_msg_id)` 唯一，权限检查与提交同事务；相同原始语义返回原消息 UUID、seq 与创建时间，不同语义显式冲突。原始 `submission_content` 不随编辑变化，JSON 对象键序及精确数值语义规范化；无 Redis TTL 占位。Web 在请求前持久保存提交键及原内容，刷新后重试复用 |
| Inbox 写入 | 用户流事务 + 事件幂等账本 | `BatchInsert` 按用户 ID 顺序锁定 `inbox_streams`，以 `(user_id, change_id)` 去重后分配位置；账本与记录同事务提交，已读合并或记录过期后重放也不增加位置 |
| Kafka 消费 | 先持久化再投递 | InboxWriter 对同一批人类收件人先写收件箱再发布投递意图；重放不增加同步位置，但仍允许 best-effort 推送重放 |

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
5. **下游持久化幂等**：InboxWriter 在用户流锁内检查每名收件人的引用，避免重复分配位置；收件箱已存在不跳过投递意图，允许 best-effort 推送重放
6. **客户端 SyncMessages 最终兜底**：客户端可通过 `GET /api/v1/messages/sync?position=lastKnownPosition&limit=50` 用一个用户位点拉取所有会话的缺失变化；按响应 `next_position` 继续，直到 `has_more=false`。

#### 不乱序（严格有序保证）

**Seq 序号机制**：

| 环节 | 机制 | 保证 |
|------|------|------|
| Seq 生成 | PostgreSQL UPSERT + RETURNING | 同一会话内 seq 严格递增（`INSERT ... ON CONFLICT DO UPDATE SET current_seq = current_seq + 1 RETURNING current_seq`），与消息写入同事务 |
| 消息存储 | `messaging.messages` 表 `idx_conv_seq (conv_id, seq)` 索引 | 所有查询 `ORDER BY seq`，天然有序 |
| Inbox 存储 | `messaging.inbox_entries` 主键 `(user_id, position)` | 每个用户一条跨会话流；分配器 `inbox_streams.position` 是已提交末端，与记录同事务提交，较小位置不会晚于较大位置出现 |
| 变化发布 | 会话锁 + 独立发布序号 + 单一 Kafka 通道 | 消息、会话与自己的已读变化同业务事务分配 `publication_sequence` 并写 UUID Outbox；dispatcher 持锁发送，同一会话按发布序号的未发送前驱阻断后继（含重试和终态失败），不依赖 UUID/创建时间排序，不承诺全局顺序或恰好一次 |
| 增量同步 | `SyncMessages: WHERE position > $request_position ORDER BY position ASC` | 跨会话变化按用户 position 顺序分页；有后续页时 `next_position` 只到本页末位置，末页可到同一快照中的已提交末端；已删除或失去成员资格的引用不会泄露正文 |
| 游标分页 | `GetMessages: WHERE seq < cursor ORDER BY seq DESC` | 基于 seq，不会跨页乱序 |

**关键设计**：每个会话拥有独立的 seq 空间（`messaging.sequences` 表每 conv 一行），不同会话的 seq 互不影响。PostgreSQL UPSERT 的原子性保证了同一事务内 seq 的严格递增。

#### Inbox 写扩散模型

新消息（含系统消息与 Bot 回复）、编辑、撤回、全员删除、会话与成员变化、个人会话设置、自己的已读位点均经同一用户流重放。记录仅保存引用与变更种类，不复制正文；UUID `change_id` 与独立发布序号在持会话锁的业务事务中分配。消费前冻结的收件人集合与当前成员资格求交，移除标识仍投给原成员。UUID 基线不为变更引用添加会话外键，以保留删除标识；`inbox_applied_changes` 同事务记录实际分配的位置与时间，已读合并后旧有效位点继续可用，真正未知的位置不被误认。

- **用户同步位置**：由 `messaging.inbox_streams` 独立分配，跨会话且不由会话 `seq` 推导；位置分配与收件箱写入要么一起提交，要么一起回滚。
- **同步协议**：`SyncMessages` 请求包含 `user_id`、`position` 与 `limit`；HTTP 用户 ID 仅从鉴权上下文读取。每条 `changes` 包含 `position`、`conversation_id`、`kind`：`message.new/edited/recalled` 返回完整当前 `message`，`message.deleted` 返回 `message_id`；消息变化同时附当前完整 `conversation`。`conversation.upsert` 返回当前会话快照，`conversation.removed` 移除会话与缓存。`read.updated` 返回自己的 `last_read_seq` 与当前会话，按用户/会话只保留最新一条并移动到新位点。同一页可多次引用同一消息，客户端严格按位点顺序应用。旧会话级同步入口已移除；历史与 around-seq 接口不变。
- **重建协议**：省略位点或 `position=0` 返回 `rebuild_required=true`、`rebuild_reason=new_device`；范围内未分配或超过已提交末端的未知位点返回 `unknown_position`；超出收件箱保留期返回 `expired_position`。已读合并后的已分配位点可正常续读。重建包含完整当前会话列表（含个人设置）与每会话最近 `limit` 条历史，并返回可继续增量的正 `next_position`，即使用户流为空也不静默从最新开始。
- **参数边界**：`position`、会话 `seq`、已读位点和历史 `cursor` 必须是 `0..9007199254740991` 的整数；负数、分数、溢出或旧 JSON 字符串显式拒绝，HTTP/WS 嵌套响应及 Web 快照均使用 JSON number。`limit` 为非负有符号 32 位整数，省略或 `0` 默认使用既有页大小并受 `Message.MaxPageSize` 限制。历史由 `GET /api/v1/convs/:id/messages?cursor=<seq>&limit=50` 读取，按 `pagination.next_cursor` 续页，不以 UUID 排序或分页。
- **收件箱保留期**：配置 `Message.inboxRetentionDays`（正整数，默认 30 天），启动时及随后每小时回收过期前缀；`inbox_streams.retained_position` 与删除同事务更新，已提交末端不回退。历史读取不受影响；同步时直接按记录年龄判断过期位点，不依赖回收 worker 是否已运行。
- **重建可观测性**：Prometheus `aim_service_inbox_sync_rebuild_total{reason="new_device|unknown_position|expired_position"}` 统计成功重建次数；标签只有三种固定原因，不包含用户或设备 ID。
- **个人删除**：持久状态存于 `messaging.personal_message_deletions`，键为 `(user_id, conv_id, message_id)`，不受收件箱保留期、退群重入或重建影响。当前成员可个人删除任意可见消息；覆盖与仅投给本人的删除变更 outbox 同事务提交，经 `message.created` 统一通道重放 `message.deleted` 标识。历史分页、单条/批量、搜索及其计数/高亮、回复摘要、同步/重建、会话预览与未读数均按该账号过滤；不改消息本体，不影响其他成员。全员删除仍仅允许发送者并物理删除本体，撤回保留撤回状态实体。

`messaging.conv_read_seqs` 是每个用户在会话内已读位点的唯一真相源；标记已读与 outbox 同事务提交，位置不能回退且截断到会话最新 `seq`。列表、详情和回执读取同一值，未读数按「消息 `seq` 大于已读位点、且发送者不是该用户」计算。他人的回执与未读计数仅实时投递，不写收件箱。WebSocket `inbox.changed` 只唤起用户同步；在线与离线变化共用整页校验、按序应用、快照与位点原子落盘路径，新消息同时保留原 `message.new` 实时载荷。

### AI Bot 执行引擎

bot-service 的 runtime 角色是 AI 能力的核心引擎，基于 **CloudWeGo Eino** 框架构建 ReAct Agent，实现 LLM 推理、工具调用、知识检索、记忆管理的完整闭环；控制面与其共享领域、数据 schema 和 BotService 协议。

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
| **WebSocket** | Bot 解析会话收件人后发布 `delivery.requested`，realtime 按连接登记投递流式 chunk，不向无关在线用户广播 |

#### MCP 工具集成

MCP (Model Context Protocol) 工具集成流程：

1. 从 bot 域仓储读取 Bot 绑定的 MCP Server 配置，不调用控制面 RPC
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

知识库支持 **RAG 检索**，满足不同场景的知识管理需求。上传文档后经解析、分块、Embedding 管线处理。在线检索（`-role online`）与入库（`-role ingest`）是两个独立工作负载，同镜像不同角色。

知识库、文档、父/子片段和绑定由 knowledge domain 分配 UUIDv7，关系库使用 `uuid`。子片段在 Milvus `kb_chunks_uuid_v1` 的主键与 `document_chunks.id` 相同，`id`、`kb_id`、`doc_id` 均为 `VARCHAR(36)`；不再拼接第二套向量身份。原对象键为 `knowledge/<kb UUID>/<document UUID>/original`，图片对象限定在该文档的 `images/` 目录。

同文档摄取、重试和删除通过 PostgreSQL session advisory lock 串行；阶段重试复用已分配片段 UUID，未确认的处理中任务重投在锁内恢复。替换前清理旧关系和向量，检索仅接受 `ready` 文档、`active` 知识库的当前关系片段；失败片段不可检索，禁用后的摄取失败仍可在重新启用后重试。向量写入成功依赖 Milvus mutation 确认，立即可见性由强一致性读取保障，不逐文档执行手动 Flush。

---

#### RAG Ingest Pipeline（含图片处理）

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
       metadata: {block_type: "child", parent_index: 0, parent_chunk_id: "父片段 UUID"}
```

- Parent 和 Child 使用完全独立的 `ChunkingConfig`（size/overlap/separators）
- Child 通过 `document_chunks.parent_chunk_id` 关联到 Parent
- 检索时先验证命中 Child 的当前关系 UUID，再按 `parent_chunk_id` 恢复 Parent 正文并去重；返回来源保留 Child 的 `chunk_id` 与 `matched_content`。

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
  ├─ 当前 ready 片段验证 + 父片段关系恢复与去重
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

### Realtime 定向投递

`realtime-service` 不拥有会话成员、消息业务判断或未读数；消息域产出的投递意图已包含收件人和客户端信封。

1. **连接登记**：每设备独立 TTL；建立、心跳与断开维护，`generation` 条件更新防止旧连接删除新登记。在线状态查询直接复用登记。
2. **在线投递**：Kafka 单消费组读取 `delivery.requested`；查询目标设备实例，发布到 `rt:node:{instance}`。同实例与跨实例使用同一路径，发送方自己的其它设备也收到回显。
3. **负反馈**：节点无订阅、连接缺失或写失败时清理对应 generation 的登记，记录降级并触发配置启用的 FCM/APNs。没有 ACK、重投或未确认队列。
4. **正确性**：WebSocket 是 best-effort；客户端按会话 `seq` 补拉恢复缺口，未读由消息域持有。
5. **平滑退出**：SIGTERM 先使 readiness 失败、拒绝新接入，再等待摘流量窗口并分散关闭连接。Compose 与 Helm 预留退出宽限期。

消息域负责新消息、Bot 回复、撤回、编辑、按用户删除、已读回执和未读同步的收件人解析；typing 也由消息域校验成员再产出投递意图。

| Bot 类型 | 路由方式 |
|---------|---------|
| Official / Self-Deployed | Kafka `message.created` → bot-runtime |
| Third-Party (conn_mode=ws) | `delivery.requested` 的 Bot 收件人 → realtime WebSocket |
| Third-Party (conn_mode=webhook) | bot 域读取配置并发送签名 HTTP Webhook |

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
