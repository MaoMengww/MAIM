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

共享基础设施：PostgreSQL / Redis / Kafka / MinIO / Milvus / ES
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

推送 best-effort；断线、实例故障或推送缺口通过用户级收件箱的增量补齐与重建恢复聊天变化；Run 另由 bot 的权威查询与独立版本恢复。
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
| `bot` | bot-service：Bot 配置、MCP、交互、Run 与有来源的工作摘要 |
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
│   ├── bot-service/                  # Bot 控制面 + Eino Agent/MCP/交互工作，control/runtime 独立工作负载
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
- Compose：准备 `.env` 中的 Postgres、MinIO、`JWT_SECRET`、`AIM_ENC_KEY` 与 `INGEST_EMBEDDING_TOKEN`，执行 `docker compose up -d --build`。`AIM_ENC_KEY` 必须是 base64 编码的 32 字节密钥。改变环境变量后用 `docker compose up -d --force-recreate <service>` 重建容器，不支持热更新。
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

用户、关系和通知链路采用上述合同。JWT 分别携带用户 UUID、稳定专用 `device_id`、设备记录 UUID `session_id` 和专用 `jti`；gateway 与 realtime 经 user 的真实 `ValidateToken` 校验记录/撤销状态。撤销后同设备重新登录创建新 session，旧 token 不复活。好友解除分组发送 `clear_group_id=true`，省略保持；通知引用同时提供 `reference_type` 与 UUID `reference_id`，无引用时二者缺失。presence 使用 `presence.subscribe` / `presence.unsubscribe` 与 `presence.state` 的 `online` 布尔值。

新环境应用 `migrations/postgres/000_uuid_identity.sql` 及其后续 UUID 合同迁移，服务通过 `database.RunMigrations` 消费同一嵌入迁移集；旧迁移的有效表、索引和约束已合并。`001_inbox_checkpoint_provenance.sql` 保留已分配同步位点的来源，使已读记录合并后仍可续读；不推测此前已经丢失的位点。旧整数结构/旧迁移记录会显式失败，不自动映射或清空。第 01–07 票的共享合同和业务调用链已切换；第 08 票协调存储重建、全部角色初始化、缓存拒绝、部署合同和全系统验收，具体运行证据及环境边界见 [实施票](.scratch/id-system/issues/08-storage-rebuild-final-cutover.md)。

#### 开发存储协调重建

重建不是 UUID 迁移自动行为。只允许对已核实属于本仓库的开发资源执行：先保存 Compose 的解析模型、容器 `com.docker.compose.project.working_dir/config_files/project` 标签、卷标签及实际挂载；仅凭 `aim-*` 名称不能证明归属。生产、外部共享资源、归属不明的匿名卷一律排除，不运行全局 `flush`、`prune` 或批量猜名删除。

1. 停止 gateway 和 realtime 的新流量，再停止 Bot runtime、knowledge ingest 及所有其它应用写入角色；确认它们已退出。离线旧容器也必须移除，不能在重建后重启旧镜像。
2. 核实所有连接目标。独占且归属已确认、且另获数据清理授权的 Compose 开发 project 按其原始 `--env-file`、`--project-directory`、`--project-name`、`-f` 执行 `down --volumes`，同步移除 PostgreSQL、Redis、Kafka（事件及消费位点）、Milvus（含内嵌 etcd）、Elasticsearch、MinIO 的项目卷与引用。共享后端不能用此整卷方法，只能对有所有权证据的 AIM schema/collection/database/index/topic/group/bucket/键空间实施同范围重建。退役的 Neo4j 卷、PVC、图数据与长期记忆向量集合不由仓库部署变更自动清理。
3. 先启动真实中间件并等待 readiness，再执行 `init-kafka-topics`，最后启动全部 10 个工作负载。PostgreSQL 初始化与服务启动共用迁移集；Milvus 使用 `default` database 下的 `kb_chunks_uuid_v1`，Bot 不初始化或访问长期记忆集合。平台模板在事务锁下幂等创建，重启不重复生成。
4. 运行隔离 runner `--scenario all`、真实数据库事务回归和 Web 同步回归，再在真实浏览器验收。旧 JWT、旧实体缓存命名空间不回退；当前快照中的字符串位点或数字实体引用整份作废，从 `position=0` 重建。保留 `aim_device_id`，不清空整个 localStorage。

`--keep-environment` 的 `environment.json` 保存该轮隔离 project 的精确连接地址与清理 argv；运行证据需分别记录资源目标、端到端、数据库、浏览器及静态检查结果。没有真实后端或角色启动失败不能标记切换完成。


---

## 部署

### Docker Compose（本地开发/联调）

`docker-compose.yml` 同时编排基础设施与应用服务：Postgres、Redis、MinIO、Kafka（含 `init-kafka-topics` 幂等建 topic）、Milvus、Elasticsearch、otel-collector、Jaeger、Prometheus、Kibana、Grafana，以及 10 个工作负载（`user-service`、`message-service`、`file-service`、`llm-gateway`、`knowledge-base`、`knowledge-ingest`、`bot-service`、`bot-runtime`、`realtime-service`、`gateway`）。

- MinIO 社区版已改为[仅发布源码](https://github.com/minio/minio#source-only-distribution)，Compose 从固定版本源码构建 `aim-minio`，不再拉取不可用的 `minio/minio:latest`；首次构建需要访问 Go module proxy。
- 消费者服务等待 `init-kafka-topics` 成功后再启动，避免空环境首次启动时因 topic 不存在退出。
- 应用配置全部来自 `.env` 与环境变量；变更后需要 `--force-recreate`，不支持热更新。

```bash
cp .env.example .env   # 填写 Postgres/MinIO 凭据、JWT_SECRET、AIM_ENC_KEY、INGEST_EMBEDDING_TOKEN
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

### Bot 最终切换的运维验收边界

本节是验收步骤，不是本次已运行集群的证明。停止旧写入角色后，同仓发布前后端及 control/runtime；使用隔离 UUID 数据库分别验证空库顺序初始化、已有 UUID 库升级与重复启动，核对 `schema_migrations` 的版本/时间不被重写。旧整数库不支持就地映射，失败不能用清库掩盖。

- `017` 要求先有 `016`，只退役旧消息级耦合表/列；保留输入来源、实际接受版本、真实授权及历史个人删除覆盖。仅从仍存在的同会话原消息补齐缺失的 seq，不重建已清除正文，不解除历史暂停或复活终态。`018` 增加规则/委派来源与预算；`019` 使旧第三方认领过期等待当前恢复；`020` 丢弃缺乏边界证据的框架 checkpoint，但平台轨迹与模型调用事实继续保留。
- 在不启动 Neo4j、不创建长期记忆集合的环境分别启动 control/runtime，运行知识上传→ingest ready→检索→来源引用及受控网络工具；Milvus/MinIO 是共享知识依赖，不能作为退役记忆资源删除。
- 至少两个 realtime 实例验证多人发起、补充、承接、显式停止与重连；分别核对聊天 `seq`、用户收件箱 `position`、Run `version`。组合检查规则/委派来源、父子实际停止结局、执行器换代、未知写停放及唯一最终消息，不使用付费、邮件或真实删除动作。
- 新增组合旅程：`python3 tests/e2e/run.py --scenario bot-cutover --timeout 60s`；覆盖同 Bot 跨会话/并行/私人隔离、多人补充及断线补收、规则→行动确认→消息操作→失联换代→明确委派、父子不同停止结局及真实撤权。最终 `all` 同时运行该旅程、压缩重启、完整故障矩阵和知识库/模型保留场景。首次启动前须创建 `document.uploaded` topic，避免知识入库消费者在首次上传之前因队列不存在退出；Compose 和 Helm topic 声明均包含它。
- 记录本轮环境目标、迁移/持久化、公开场景、浏览器和故障恢复证据；历史 issue 的通过不是本轮输出，部署清单通过不是 k3s 运行通过，受控 provider 的缓存 usage 不是远端命中或延迟收益。退役归档、图数据、集合、volume/PVC 的销毁必须另核实所有权并获得独立授权。

---

## CI

`.github/workflows/ci.yml` 仅由 PR 创建、更新、重新打开或标记 ready 触发；普通 push、合并后和定时任务不运行。包括草稿 PR，每次有效运行都执行后端全量 `--scenario all`，不按改动路径缩小验收范围。同一 PR 的新提交取消旧运行。

四个独立 job：

- **Go checks**：`gofmt`、`go mod tidy -diff`、全仓编译/`go vet`、静态 lint。lint 使用固定版本 golangci-lint，启用 `govet`、Staticcheck 的 `SA*`、`ineffassign`、`unused`、`errcheck`；仅排除标准标记的生成代码，不自动忽略历史问题。
- **Unit tests and race detector**：全仓无 `integration` 标签的测试，开启 race detector，覆盖率分母包含 `app/`、`pkg/`、`migrations/`，包括没有测试的业务包。
- **PostgreSQL integration regression**：独立 PostgreSQL 16，运行 message-service repo 和消息同步 logic 的带标签回归，开启 race detector；不与 E2E 的 Outbox 消费者共库。不是全仓所有带标签集成测试。
- **Full backend E2E**：复用现有隔离 Compose harness、真实内部 service/中间件与受控外部 provider，执行 `all`。构建镜像仅用于验收，不发布，不部署，不包含浏览器前端测试。执行预算 75 分钟，job 总超时 90 分钟；不足时应调整 runner/预算，不删场景或自动重试掩盖失败。

`CI required` 汇总这四个 job：失败、取消或跳过均不通过。管理员需在 GitHub **Settings → Rules → Rulesets** 为 `main` 设置 PR 合并要求与 required status check **`CI required`**，并要求合并前与目标分支保持最新；不允许普通 push 绕过验收。仅提交工作流不会自动启用这些远端规则。公开 fork 首次贡献可能需要维护者批准工作流执行，不能将其改为带写权限的 `pull_request_target` 来执行 PR 代码。

Go 工具链与容器统一为 `go1.26.8`。本地复用同一命令：

```bash
make ci-format ci-tidy ci-build ci-lint
make ci-test
INBOX_TEST_DSN='postgres://.../<isolated_database>?sslmode=disable&search_path=messaging' make ci-integration
make ci-e2e
```

本地 lint 安装：`GOTOOLCHAIN=go1.26.8 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`。格式与模块检查不修改文件；失败后在本地修正并提交。不要在同一资源受限机器上同时运行全仓编译/race 测试和重负载 E2E。

Codecov 仅展示覆盖率，不设 project/patch 门禁；忽略 `*.pb.go` 与验收工具，不忽略业务逻辑。单元测试和数据库回归分别上传报告，当前 E2E 不提供 Go 覆盖率。测试/报告生成失败阻断；外部上传失败仅告警，报告仍可从 Actions artifacts 下载，保留 14 天。E2E 的有界诊断日志也保留 14 天。

仓库管理员需在 Codecov 连接 `MaoMengww/MAIM` 并授权 GitHub 集成。仓库内 PR 使用 OIDC，不需要长期 `CODECOV_TOKEN`；公开 fork PR 使用 Codecov 的 tokenless 支持。只跑 PR 意味着默认分支没有自动更新的覆盖率基线，不能保证准确的主分支覆盖率增减比较。

## 端到端验收

前置条件：Python 3、Docker Engine、支持 `--wait` 的 Docker Compose 插件及 Buildx。验收通过 gateway REST 与 WebSocket 驱动真实服务；没有公开通知创建入口时直接调用现有 domain gRPC，不新增测试 API。不替换内部 RPC、Kafka 或数据库。

```bash
python3 tests/e2e/run.py --scenario all --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario user-identity --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario messaging --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario attachments --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario llm-chat --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --cross-instance --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario stage-p3 --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario stage-p5 --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario stage-p6 --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario user-sync --artifacts /tmp/aim-e2e-artifacts
python3 tests/e2e/run.py --scenario broadcasts --artifacts /tmp/aim-e2e-artifacts
```

每次使用独立 Compose project、网络与数据卷，默认无宿主端口映射。所选场景全部依赖就绪后才施加流量；realtime 的两个实例分别可寻址，但共用一个 Kafka 消费组。成功或失败后均清理该 project 的容器、数据卷与本次构建的镜像，不清理共享 BuildKit 缓存；`--artifacts` 保留诊断日志。显式 `--keep-environment DIR` 保留本轮私有环境，`environment.json` 记录清理命令；配合 `--browser-access` 或 `--database-access` 只发布随机回环端口。浏览器验收须将保留环境的 realtime 心跳/登记 TTL 恢复生产配置（30/90 秒），不能让 Web 的 25 秒心跳运行于故障场景的 1/6 秒配置。

默认 `all` 启动全部 10 个工作负载与 realtime B，检查 relationships、user-identity、conversations、conversation-unread、broadcasts、user-sync、attachments、同实例 A/B、Bot 同/跨实例、knowledge-ingest、llm-chat 和跨实例双向投递；`--scenario` 可选择单个场景。同实例与跨实例走同一条 Redis 定向投递路径。隔离 runner 限定 Java heap 与 Milvus 线程并行度，保留真实后端、四个大文档、既有 online/ingest 配额与检索截止时间；不要与宿主全仓编译同时运行重负载存储验收。

`user-identity` 只启动 user、gateway、双 realtime 及其真实中间件，复用 relationships 并验证注册/登录/刷新/资料/设置、跨账号拒绝、通知持久化/跨实例 WS/离线列表、设备稳定重连和撤销后旧 token 不复活。单场景结果不能代替 `all` 的完整 domain 验收。

`messaging` 只启动 user、message、gateway、双 realtime 与 PostgreSQL/Redis/Kafka/Elasticsearch 等真实依赖，复用 conversation-unread、broadcasts、user-sync、同实例 A/B 和跨实例场景。覆盖丢确认后的原结果重试、并发唯一提交、异义冲突、发送者/会话隔离、权限恢复、历史序号分页、个人删除与搜索；不以替身冒充 Bot、文件或知识业务。

`attachments` 启动 user、message、file、gateway、双 realtime 与真实 MinIO/PostgreSQL/Redis/Kafka/Elasticsearch。验证真实 PUT/确认/下载的字节一致性、四种附件的 UUID 在确认/WS/历史/离线收件箱中一致、同键重试与换文件冲突（包括文件删除后的原提交回放）、回复与文本搜索引用、私有对象匿名访问拒绝，以及消息删除、单个/批量文件删除的对象隔离。没有 Bot 或知识 service 替身。普通聊天上传显式使用既有公开访问级别；私有附件仅上传者读取，未新增会话成员授权策略。文件实体身份与对象键、临时签名 URL 各司其职；消息 domain 分配提交结果，gateway 不拥有附件提交判定。

使用 `attachments --keep-environment DIR --browser-access` 时，清单额外记录真实 MinIO 的随机回环端口。容器验收默认使用 `minio:9000`；浏览器验证前将该隔离环境 file-service 的 `MINIO_PUBLIC_ENDPOINT` 设置为清单的 MinIO 地址并重建该容器。SDK 对浏览器可见 host 本身签名，不在签名后替换 host。公开对象只允许 `public/*` 匿名读取，私有对象通过鉴权后的有效期下载 URL 访问。

`user-sync` 检查空流重建、单个位点跨会话分页、消息正文与账号隔离、新设备最近历史及置顶/免打扰设置、未知位点重建和续增量；同账号两设备个人删除他人消息后同步隐藏，原发送者仍可读取，搜索/历史/回复摘要/会话预览及新设备重建不泄露正文。过期回收后不复活与并发未提交写入窗口由真实 PostgreSQL 的集成回归覆盖。

真实消息事务补充验收使用无应用消费者连接的隔离 PostgreSQL database：设置 `INBOX_TEST_DSN=postgres://.../<isolated_database>?sslmode=disable&search_path=messaging`，执行 `go test -tags integration -count=1 ./app/message-service/internal/repo ./app/message-service/internal/logic/messageservice`。不与运行中的 Outbox dispatcher 共库；活跃消费者会合法取走测试待发布记录，造成错误的并发判定。Web 补充边界为 `cd web-client && node --test tests/user-sync.test.mjs`。

`broadcasts` 检查 `user/group/all` 范围、并发首次广播只创建一个用户系统会话、后续复用与递增 `seq`、两副本上的普通 `message.new` 投递，以及离线账号经用户收件箱增量/重建和会话历史读取广播。广播无需客户端专用事件分支。

`stage-p5` 在 P4 场景上追加 Bot 配置/令牌、真实网络 MCP 工具发现与调用、Kafka 回复及 WS/REST 精确内容核对，以及四个大文档并发入库期间的检索和失败隔离。OpenAI/MCP 外部协议由 `e2e-provider` 提供；AIM 内部 RPC、Kafka、PostgreSQL、Milvus 不替换。在线查询每次硬截止 5 秒；入库总截止 10 分钟，容纳默认 20 RPM 预算，不提高配额或缩小文档负载。

`bot-runtime` / `bot-runtime-cross-instance` 在不启动 Neo4j、不创建长期记忆集合的隔离环境中运行真实 knowledge online/ingest、文件与 Milvus：覆盖 Bot/会话绑定、模型所有权、真实网络 MCP 调用、目标提及启动 Run，以及知识来源在 Run、WS 和持久消息正文中的一致性。旧群摘要、Todo、回复候选、专用翻译和直接 chat/stream 接口不再提供。外部 provider fixture 只决定模型/MCP 响应，不替换 AIM 内部服务与存储。

`llm-chat` 经公开模型 CRUD 和现有 LLM gRPC 调用真实网关，受控 HTTP provider 验证同步/流式请求、工具流及结果配对、DeepSeek 私有 reasoning 回放、真实首 token、usage 缺失/零值/缓存读写、截断流/错误/取消、端点快照拒绝与当前密钥轮换。它不验证远端缓存实际命中；Bot Run、计费、知识库与跨实例链路仍由 `all` 的既有场景覆盖。

Chat/ChatStream 锁定 Eino 核心 `v0.9.21`、OpenAI component `v0.1.13`、ACL `v0.1.17`，通过兼容接口连接 OpenAI、DeepSeek、Qwen 和配置的端点。同步调用也消费组件的 Stream，以实际内容/reasoning/tool delta 计算首 token；窄 wire bridge 保留 SDK 无法表达的全部 choices、usage presence、usage-only 帧及 `[DONE]` 证据。模型授权、HMAC 快照、当前密钥、计费与额度仍由网关负责；没有旧 Chat HTTP/SSE fallback。Cohere embedding、远程 rerank 和 VLM 在各自后续票处理，不由本次 Chat 迁移改写。

本地 Go 验证使用与容器一致的工具链：`GOTOOLCHAIN=go1.26.8 go test ./...`；网关流生命周期回归为 `GOTOOLCHAIN=go1.26.8 go test -race ./app/llm-gateway/internal/handler ./app/llm-gateway/internal/llm`。

`stage-p6` 覆盖 P3/P4 主链路、跨实例 Bot 最终回复与版本化 Run 状态、发送方其它设备回显、非成员隔离、撤回/编辑/删除与未读同步。验收通过 `presence.query` 查询设备所在实例，并真实执行强制终止、TTL 自然失效、TTL 内投递失败负反馈、重启恢复，以及 readiness 摘流量后的多连接平滑排空；遗漏消息按用户同步位点补拉，Run 过程通过权威查询恢复。

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

bot-service 的 runtime 角色使用锁定版本的 **CloudWeGo Eino `ChatModelAgent + Runner`** 编排模型与工具。Run、接受输入、当前授权、执行代次、轨迹、预算和输出 receipt 仍由 AIM 持有；模型经 llm-gateway，框架 checkpoint 不授予资源许可。

#### 整体处理流程

```
公开请求 / 目标 mention
  → 校验当前 Bot、会话绑定、请求者及明确来源
  → 持久接受输入与 Run / outbox，重试按原提交键去重
  → 以资源属主许可收集显式文件与历史等必要材料，不固定检索知识
  → Eino Runner 经网关推理，在统一工具边界检查当前许可
  → 外部写操作等待具体行动批准或匹配受限自动许可
  → 生成结果，重新授权后提交群消息或保留个人可见结果
```

#### 唯一模型与工具执行边界

`ChatModelAgent` 负责模型—工具调度；桥接器从 AIM 的持久轨迹组装完整请求。已完成工具往返不重放，未结束的外部写操作不能因恢复而再次调用。批准等待使用 `compose.Interrupt`，恢复使用同一 Runner；恢复数据、普通补充和框架 checkpoint 都不能绕过当前具体行动权限。

1. **固定执行配置**：冻结模型路由、系统指令、工具定义和累计预算，不复制供应商或工具凭据。
2. **资源材料**：仅使用此次选定的有效资源许可；读取与向会话披露分别授权。个人材料进入个人 Run，不混入群共享请求、轨迹投影或结果。
3. **显式历史来源**：Bot 使用自己的绑定与限定历史许可读取新材料，不借用户调用身份；接受前尊重当前来源可见性，已接受工作快照独立于原消息。引用来源不变成本 Bot 的旧 assistant 发言，也不默认吸收最近群消息。

#### 知识检索

知识属主 RPC 使用明确的 Bot、绑定、请求者、可见范围与资源许可，不借用户调用身份绕过知识权限。检索在 embedding、rerank 与返回来源边界复核当前许可；知识绑定移除后重新加入也不复活旧许可。

内置执行器按需调用现有 Run 工具 `search_knowledge`，参数仅为 `query`（1–4096 个字符）；列库不是前置条件。查询可由模型结合已接受输入与交互上下文改写或拆分，但不能修改任务目标、约束、身份、获准范围或执行配置。空命中返回成功的空 `items`；参数、配置和暂不可用错误分别持久为安全的 `invalid_argument`、`configuration`、`unavailable` 结果，允许模型换获准查询或报告缺口。授权撤销、停止及租约失效仍是执行控制边界，不作为可绕行的普通错误。

搜索资料、行动查询与结果沿用 Run 材料、来源、轨迹及恢复事实；命中片段与父块补充上下文分别标记 `matched`、`supplemental`，只公开实际文档／片段 ID 及已有定位元数据。每次消费与披露复核当前许可和文档／片段版本；失效来源及派生内容从 Run、摘要与新消息读取投影中移除，不声称能收回已经披露给外部系统的内容。第三方执行器保留原有获准资源读取路径，不新增工具协议。

`python3 tests/e2e/run.py --scenario bot-knowledge-tools --timeout 60s` 经现有 gateway 验收真实入库、索引、授权、按需搜索、空命中、可恢复失败、停止、预算与恢复。可控供应响应只用于确定性边界测试，最终答案依赖实际返回资料；它不能证明自然模型指代能力或命中率提升。

KB、文档和 chunk UUID 保留在获准 sources 与共享消息 metadata 中。私人正文与派生结果仅对个人请求者可见；撤权阻止后续模型请求和输出，不声称已经发给外部系统的材料被收回。

#### 已退役能力与数据保留

Bot 不再自动抽取事实、生成画像或检索跨交互长期记忆；启动不依赖 Neo4j，也不创建长期记忆向量集合。知识库及共享 Milvus 保留。Todo、群摘要、回复候选和专用翻译的 UI、HTTP/RPC、异步工作与结果订阅已完整退出；语言偏好、普通消息历史、Bot 模型/MCP 与工作状态不受影响。

新库基线不创建记忆配置、群摘要或 Todo 结构。已有 UUID 库通过 `002_retire_bot_capabilities` 顺序迁移：旧配置保存在 `bot_retired.memory_configs`，旧群摘要和 Todo 表移入 `bot_retired`，活动 `bot` schema 的字段与表退役。应用不读取该归档；原迁移版本与时间保留，重复迁移和重启不重建旧能力。归档、图数据、向量集合、Docker volume 与集群 PVC 的实际销毁须另获明确授权。

#### 流式输出

| 通道 | 实现 |
|------|------|
| **WebSocket** | Bot 解析会话收件人后发布 `delivery.requested`，realtime 按连接登记投递流式 chunk，不向无关在线用户广播 |

#### MCP 工具集成

MCP (Model Context Protocol) 工具集成流程：

1. 从 bot 域仓储读取 Bot 绑定的 MCP Server 配置，不调用控制面 RPC
2. 创建 `mcp-go` 客户端（支持 SSE 和 StreamableHTTP 两种传输）
3. 发送 MCP `Initialize` 请求（协议版本协商）
4. 经 `ListTools` 获取定义，与资源管理者的可信风险及分享范围配置合并；远端 annotations 和模型自判不构成许可

内置工具：`web_search`


---

### LLM 模型网关

llm-gateway 是所有 LLM 调用的统一入口，提供多 Provider 抽象、计费、限流。

#### 多 Provider 抽象

Chat 与 ChatStream 唯一经锁定的 Eino OpenAI ChatModel `Stream`；同步 Chat 收集同一供应商流并保留真实首 token 证据，不维护第二套 HTTP/SSE fallback。VLM 使用 ChatModel `Generate` 的多模态表示，OpenAI-compatible Embedding 使用 Eino Embedder；没有等价组件的 Cohere Embedding 与 Cohere/DashScope 远程 Rerank 保留真实窄 HTTP 实现。

模型注册、可见性、冻结路由、凭据、并发额度及实际 usage 计费仍由 llm 域持有。BaseURL 优先当前模型配置；provider、端点或密钥身份变更均拒绝旧冻结快照，须创建新 Run/快照；密钥不进入公开快照、日志或材料。完整 schema、工具配对、零温度、私有推理回放、usage 缺失/显式零和缓存读写语义保留；组件接入不证明真实供应商缓存命中。

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

#### 独立身份与会话绑定

Bot 使用稳定 UUID，通过 `messaging.conv_bots` 的独立绑定参与会话，不创建用户账号或普通群成员。人类成员数、容量、角色、设备、presence、未读和已读不计入 Bot。会话管理员可邀请他人拥有的 active Bot；Bot 所有者、绑定邀请者 `added_by`、本次 `requester_id` 和消息 `sender_id` 分别表达，所有权不授予会话内容读取权。

消息实体统一返回 `sender_id`、`sender_type=user|bot|system`、展示快照 `sender_name/sender_avatar`；新 Bot 输出携带开始时的 `binding_id` 与实际请求者。内容独立使用文本 1、文件 3、结果 10；结果使用 `result: {type,data}`，执行来源和工具 metadata 位于顶层 `raw_payload`。原 Bot 内容类型 9 已退出，历史、引用、搜索、收件箱和实时投递采用同一身份。非法 metadata 在持久化前统一拒绝，不能通过 Bot 输出阻断人的同步流。

公开接口：

- 用户 JWT：`POST /api/v1/convs/:id/bots/:bot_id/history-grants`，提交 `{binding_id,start_seq,end_seq}`，返回 `grant_id`。授权者必须仍是当前人类成员，范围不得超过已提交历史或包含其不可见来源。
- Bot bearer：`GET /api/v1/bot-app/convs/:id/messages?binding_id=…&history_grant_id=…&cursor=0&limit=30`。没有默认全群历史；查询校验当前绑定、授权者资格和来源可见性，持久记录实际读取范围。cursor 不得越过授权范围，引用不扩张权限。
- Bot bearer：`POST /api/v1/bot-app/runs/update`，与 WS 和签名 Webhook 使用同一 Run 更新 JSON。凭据只决定执行器身份，不赋予会话、资源或结果发布许可；旧 `/bot-app/messages` 和 `message.send` 直接输出路径已删除。
- Bot WS / 签名 Webhook：工作输入为 `bot.run.request`，携带平台分配的 Run、认领 `generation`、`input_version`、`operation` 及获准输入；执行器不能自报 Bot、请求者、绑定或授权。结果 `msg_type=text|file|result` 由 AIM 校验后进入同一消息事务；私有文件在进入公开 Run 前拒绝，文件元信息采用权威记录，不信执行器 URL/名称。

移出设置 `revoked_at`，重新加入产生新绑定 UUID；旧授权、旧输出和旧工作关系不能借重入恢复。绑定在邀请事务中记录会话 `start_seq`，异步输入按 seq 水位而非秒级时间过滤，避免同秒重入接收迟到旧输入。普通消息事务、seq、outbox、收件箱、扇出和实时投递继续复用，消息热路径不调用 Bot 控制进程。

新库及已有 UUID 库统一应用 `003_bot_conversation_identity.sql`：清理旧伪成员、重算人类数量、保留撤销绑定，转换旧类型9消息及待发布/失败事件；旧消息无法证明的请求者和绑定保持缺失，不虚构。既有搜索投影同步退役类型9；浏览器旧身份缓存整份失效并重建。

验收入口：`python3 tests/e2e/run.py --scenario bot-identity`，也包含在 `all` 中。覆盖跨会话身份、REST/WS/Webhook 文本/文件/结果、限定历史、移出重入、owner/inviter/requester 分离、搜索/引用/同步和恶意 metadata 拒绝。

#### 内置 Bot 的持久执行

- `POST /api/v1/bots/:id/runs`：提交 `{conv_id,submission_key,message,visibility?,resource_grant_ids?,reply_to_msg_id?,history_grant_id?,message_ids?,file_ids?,parent_run_id?}`。`visibility` 默认 `conversation`，个人工作显式为 `personal`。默认独立交互；`parent_run_id` 仅承接相同绑定和可见范围的终态工作，不转移个人许可。
- `GET /api/v1/runs/:id` 按当前身份、来源与许可查询；`GET /api/v1/conversations/:id/runs?limit=50` 只列共享 Run。个人工作仅请求者可通过 ID 查询，不进入群消息、共享 WS、共享列表或浏览器持久重试缓存。
- Run 的 `input_sources` 按接受输入版本展示作者与来源；可选 `content:string` 是该版本实际接受的固定正文，仅当前授权后的 REST Get/List 与人类 Control 投影提供。省略表示仅有路由元数据，不是空正文；消息来源链接只定位当前可见的原消息，不能冒充接受快照，也不能用原消息正文补齐。共享卡片与个人工作面板直接展示已获授权的 `content`，保留换行，不回读消息代替；WS 通知、停止 receipt 与第三方内部路由不自动携带私有正文。重连、重建或当前授权失败时清除旧查看者投影，并通过 REST 重新读取。
- `bot.run.updated` WS 事件携带 `{type,conv_id,bot_id,run}` 的生命周期通知，不携带结果正文、工具内容或材料来源正文；客户端通过当前查看者的 REST 读取重建，不从迟到事件恢复个人隐藏内容。卡片区分 `accepted/running/waiting_input/waiting_authorization/stopping/stopped/succeeded/failed` 与输出状态。缺信息通过 `ask_for_input` 等待补充；具体行动等待授权与负责人离群等待接手是不同阶段。
- `004_single_run.sql` 持有交互、接受输入、追加轨迹、不可变模型调用快照和 Bot outbox；`005_bot_output_submission.sql` 在消息事务内保存最终输出 receipt。重复输入返回同一 Run，重复输出返回同一消息，同键不同内容冲突；重试仍校验当前绑定和权限。
- 开始时固定模型路由、提示词、工具/schema 和预算；密钥仅在所属 domain 瞬时取得。读取、模型派发、工具与输出检查当前许可；外部写操作要求具体批准或匹配有效受限自动许可。provider、端点或密钥身份变更使旧路由快照失效，需新 Run/快照，不向旧端点发送新密钥。
- 固定指令、真实 assistant/tool 往返只追加；模型调用保留输入上界、轨迹版本、实际请求/输出和 nullable usage/首 token 延迟。支持的官方 OpenAI 端点使用原生 `prompt_cache_key`；不支持缓存照常执行，缺失缓存字段不推断命中，不自建 KV 状态。
- `007_interaction_input_continuity.sql` 分离已接受 `input_version` 和实际入轨 `applied_input_version`，每条输入保存作者与固定材料表示。本次模型调用冻结输入上界；调用中的补充进入下一轮，旧材料不重新读取正文、不前插或重排。
- 新接受消息与历史输入时检查当前绑定、补充者可见性及消息版本；已接受输入固定正文、来源身份、顺序和版本，后续工作不再依赖原消息行存在、编辑或隐藏状态。历史授权持续校验接受时保存的消息 seq 与 grant 范围及有效性，不重新读取原消息。文件读取真实正文并验证群共享许可。当前支持不超过 1 MiB 的 UTF-8 文本、JSON/XML/YAML；未知二进制明确拒绝。过大历史范围要求缩小选择，不静默截断。
- `close_interaction` / `clear_context` 关闭继续关系，必须先结束交互中的所有活动 Run；不删除消息历史、执行事实或已有结果。关闭后不能承接旧交互，但可发起独立请求。

交互验收：`python3 tests/e2e/run.py --scenario bot-interactions`，覆盖多人并发补充、调用中的新增输入、同 Bot 并行隔离、显式来源、承接与清空，以及内置和 WS/Webhook 执行器的共同输入语义。

验收入口：`python3 tests/e2e/run.py --scenario bot-runs`，覆盖明确操作/目标提及、同键并发重试/冲突、真实网络工具、稳定前缀、模型失败、配置冻结/撤权、最终历史与 WS/断线恢复。外部 fixture 证明协议与输入，不代表真实远端 KV 命中。

#### 独立工作快照、摘要与真实授权失效

- 模型预算包括冻结指令、工具定义、已接受输入、引用资料、工具结果及输出/工具增长空间。`max_context_tokens=0` 使用默认 32768；字节估算用于安全预算，不作为供应商 token 用量或缓存命中的报告。过大引用以明确“未纳入、未分析”材料替代并显示 `context_notice`，当前接受请求不被截断成另一个请求。
- `max_step` 是包含最终交付轮的内置模型轮数（1–16）；创建时省略沿用默认 5，更新时省略保持当前值。最后一轮及上下文边界关闭新工具，以现有证据和明确缺口交付；不为最终回答越过极小步数上限，亦不截断已接受目标或已确认约束。
- 仅预算不足时压缩已完成工作段。固定指令后接有效工作摘要与稳定轨迹尾部，当前输入和未完成工具往返保留完整关系；不每轮重写摘要。摘要仅表达原交互的目标、已确认约束、完成和待处理事项，作为不可信 user 材料，不是 system、行动批准、资源许可、画像、跨交互记忆或群摘要产品。
- `013_working_compaction.sql` 保存压缩检查点及独立摘要调用事实：交互、Run 分支、连续轨迹范围、接受输入上界、来源版本、指令/工具/组装版本和有效性。轨迹位置不等于输入版本、消息 seq 或收件箱位置。摘要调用走既有 Eino 模型输入边界与 llm-gateway，并有耐久累计上限；框架控制流 checkpoint 不冒充压缩检查点。
- 摘要生成固定水位，发布在会话屏障与 Run 锁下重新校验当前授权、分支、代次及前一检查点。并发输入留在尾部，竞争压缩、真实撤权、显式停止、清空或代次变化拒绝陈旧结果；原消息编辑、个人删除、撤回或全员删除不拒绝压缩。完整仍获准工作轨迹保持，重启及冷缓存从有效检查点和尾部恢复同一已接受视图。
- 平台持久接受请求、补充或引用后，普通消息操作只改变原消息，不修改工作快照，不停止排队或进行中的 Run，也不清除材料、轨迹、模型请求/响应、摘要或框架恢复数据。等待输入、等待授权及已完成工作采用相同边界；改变工作通过明确补充或新 Run，取消通过显式停止。公开 Run 不再提供消息来源变更字段或重新确认门槛。
- 原消息的历史、单条读取、搜索、回复引用、预览、未读、WebSocket、同步、重建与缓存仍遵守 IM 可见性，不能恢复隐藏或撤回正文；个人删除只影响该账号所有设备。普通消息操作不清除独立 Run 缓存；当前授权失败、真实 Run 事件及重连/重建仍安全刷新当前查看者投影。
- 原消息隐藏不向独立结果、Run 或关联子 Run 递归传播。删除或撤回 Bot 结果消息只作用于该消息，有权者仍可查看独立工作记录、其他结果并明确承接旧 Run；承接仍校验当前会话、绑定、交互及资源范围，不自动继承跨交互记忆或扩大权限。
- 原消息操作不撤销具体行动参数、已有批准或行动记录，不脱敏其目标、参数及结果；批准过期、参数变化、接手、执行代次或真实授权变化仍拒绝旧批准。已完成外部副作用保留为事实，不因消息操作或恢复重复执行。当前资源、工具、历史授权、分享许可与参与资格仍在模型/工具边界和最终消息事务中复核；真实撤权移除必要失效材料，不以追加禁止指令替代。
- `017_accepted_work_independence.sql` 退役 `messaging.message_source_refs`、`bot.runs.source_changes` 等仅服务旧消息级失效的结构，替代 014–016 的消息操作级联语义；保留历史迁移记账、仍有用途的输入/来源/授权/压缩数据及主动个人删除记录。旧逻辑已经清除的正文不可恢复；无法区分主动与派生来源的历史个人隐藏记录保守保留，不猜测性批量清空。新库、已有库升级与重复启动使用同一新合同。
- 压缩及消息变更验收：`python3 tests/e2e/run.py --scenario bot-compaction --timeout 60s`，包含在 `all` 中。受控外部端观察实际请求、配对、固定水位、真实撤权/显式停止/清空/代次变化及并发压缩导致的陈旧摘要拒绝，以及真实 runtime 重启；原消息编辑、个人删除、撤回或全员删除不拒绝摘要提交。它不实现 KV 缓存，不代表已验证真实供应商缓存命中。供应商缓存读写与首 token 延迟只保留实际报告，缺失仍为缺失。

#### 资源、分享与具体行动授权

- 用户 JWT：`POST /api/v1/bot-authorizations/resources`。`op=create` 提交 `{bot_id,conv_id,resource_type:file|knowledge,resource_id,scope:conversation|personal,can_read,can_share,expires_at}`；`expires_at` 为 Unix 秒，响应期限为 RFC3339。`list` 返回当前用户自己授予的许可；`revoke` 使用 `grant_id`。仅实际资源所有者可授予；许可绑定 Bot、会话绑定代次、成员资格、资源绑定及有效期。
- 明确请求通过 `resource_grant_ids` 选定许可，文件还通过 `file_ids` 选定正文。群工作须同时可读、可分享；个人工作只需有效读取许可。不能分享的知识或文件不先进入群模型输入再遮盖输出。群内目标 mention 可使用该绑定已经获准分享的会话资源，不继承 Bot 所有者的私人资源。
- MCP 管理者通过 `advanced_config.tool_policies` 配置工具的 `risk=read|draft|send|modify|delete|pay`、`target_parameter`、`can_share` 与精确 `conversation_ids`。用户 MCP 未配置风险时不执行；平台既有公开网络读取白名单和内置 `web_search` 保留可信读取语义。工具可调用不等于结果可分享到所有 Bot 会话。
- 用户 JWT：`POST /api/v1/bot-authorizations/actions`。`list` 查询 `run_id` 的行动；`approve|deny|revoke` 关联具体 `action_id,generation,input_version,submission_key,expires_at`。实际工具资源所有者才能批准，会话管理员或请求者不自动代批。批准绑定目标、精确参数、当前负责人、成员代次和有效期；变参、接手、过期、撤权或代次变化不能沿用旧批准。
- 个人工作默认不向工具所有者披露确认。请求者可对当前某一待批行动显式 `share_confirmation`，分享该行动的具体目标与参数给当时的工具所有者；这不是批准、整份工作读取权或未来行动许可。所有者使用 `{op:list,bot_id,conv_id}` 的窄确认收件箱处理获准条目，不获取私人 Run、来源或结果。秘密和参数内容可能含个人资料，界面在分享前明确提示披露对象与范围。
- 个人面板通过同一 `ControlRun` 合同补充、停止与明确恢复；工作 ID、私人输入、结果和重试键只保留在组件内存，账号或会话切换后清除。
- `op=grant` 提交 `{bot_id,conv_id,server_id,tool_name,arguments,expires_at}` 授予精确参数的自动许可，`revoke` 使用 `grant_id`。许可限定资源所有者作为请求者的原成员资格与绑定，不跨接手或委派扩张。不提供无限 Run 批准或通配范围；平台写工具没有既有管理员批准入口时拒绝自动放行。
- 第三方 Bot bearer：`POST /api/v1/bots/:id/executor/resources` 的 `read` 使用 `{run_id,grant_id,generation,input_version,query?}`；`POST /api/v1/bots/:id/executor/actions` 的 `request|execute` 使用当前已认领 Run 与具体行动合同。实际 AIM 工具调用通过同一批准与认领边界，重复批准不重复外部行动；不明结局记录 `unknown` 并禁止盲目重放。
- 私人参数对无批准权者脱敏，凭据不进入执行记录。RPC 不记录私人请求正文，SQL 诊断不展开参数。AIM 能撤销后续自有资源访问与输出，但无法追回已经复制给第三方的材料、已经签发的存储 URL，或全面拦截第三方自行持有外部凭据的动作。
- `010_resource_authorization.sql`、`011_run_actions.sql` 与 `012_action_confirmation_sharing.sql` 为顺序、可重复执行迁移。公开验收：`python3 tests/e2e/run.py --scenario bot-authorizations --timeout 60s`；受控外部端验证实际模型输入和工具副作用，不在真实外部系统执行删除或付费。

#### 第三方执行器的共同运行合同

- 创建或更新第三方 Bot 必须在 `capabilities` JSON 中明确声明 `input_types:["text"]`、`supports_input`、`supports_stop`、`supports_resume`。无需采用 AIM 的模型、记忆或工具框架；最低能力为接受文本工作并提交结果。
- 工作投递和 Webhook 回报归 `bot-runtime`，控制面只管理配置/凭据并转发运行请求。`006_third_party_run.sql` 保存执行器事实、补充输入来源、报告重试 receipt 和凭据签发/撤销事实。
- `run.delivery_ack` 只确认收到，`run.claim`（`status=accepted`）确认接受工作，`run.update` 报告 `running/waiting_input/failed/completed/stopped/stop_rejected`。三者均包含 `run_id,generation,input_version,sequence,update_key`；ACK 还须原样携带该投递的 `operation`，认领 sequence=0，更新从1严格连续。同键同内容返回原 receipt，异义冲突；重试重新鉴权，错误代次/版本、回退及终态后推进被拒绝。`stop_rejected` 明确无法取消，不伪称工作已停；之后真实停工仍可报告 `stopped`。
- WS 回报返回 `bot.run.receipt`；Webhook 回报使用 `POST /api/v1/webhook`、`X-AIM-Signature`（原始 body HMAC-SHA256）与 `X-AIM-Timestamp`（Unix 秒），返回公开 Run。Webhook 2xx 只表示传输送达，WS 入 Kafka 不标为已送达；未认领工作持久重投同一代次。只有消息提交后 Run 才成功。
- 用户 `POST /api/v1/runs/:id/control` 提交 `{operation:input|stop|resume|takeover|grant_control|revoke_control|close_interaction|clear_context,submission_key,message?,source_message_id?,message_ids?,file_ids?,history_grant_id?,target_user_id?}`。只有 grant/revoke 接收且要求目标为当前成员。请求者、当前负责人、明确获授控制权者及当前会话管理员可控制；普通成员可按输入合同补充，但不能停止或抢接。重复请求也重新检查当前资格。不支持的补充或恢复操作明确409且不改状态；不支持停止的执行器接受停止意图并记录 `stop_status=rejected`，不伪称已停工。第三方 `bot.run.request.inputs` 按版本保留作者、正文、材料和来源；停止则仅投递 root `run_id,bot_id,conv_id,binding_id,generation,input_version,operation=stop,reason`，不复制输入或材料。
- 输入订阅与响应触发分别配置：管理员通过会话绑定 `bot_settings.activity_types` 选定 `message.created|member.joined|member.left|business.activity`，通过 `response_rules` 明确启用相应条件；规则包含 UUID `id`、`enabled`、`activity_type`，可选 `message_contains` 或 `business_type`。平台在会话事务内记录管理员/成员资格、规则版本与 `activity_start_seq`；不补授历史。当前绑定/可见性复核后的 `bot.activity` 只表示获准观察，不创建工作、不授权行动或输出。默认无订阅/规则；精确目标提及与明确操作独立有效。普通 Bot 发言、消息编辑撤回及工作进展不自动启动。
- 公开卡片展示投递、执行器接受/完成、实际能力、输入来源、认领代次、结果和最终消息；断线后以权威查询恢复。验收 `python3 tests/e2e/run.py --scenario bot-executor-runs` 覆盖真实 WS/Webhook 计算、凭据隔离、顺序/幂等、补充/停止/恢复、获准活动、跨实例输出和历史。

#### 显式规则、委派与关联停止

- `018_explicit_collaboration.sql` 保存活动接受身份、规则/委派来源、独立子 Run 与协作预算。规则工作记录 `origin.kind=rule`、规则及活动快照；委派记录 `origin.kind=delegation`、来源 Run、委派 Bot 和真实授权来源，不填写假请求者。
- 内置 `delegate_run` / `delegation_result` 和第三方 `POST /api/v1/bot-app/delegations` 共用当前认领及平台 Run 边界。仅传递任务所需、当前可分享的材料；目标须有有效绑定和资源许可，普通 Bot 消息不是委派。子 Run 独立身份、状态和结果，父工作须明确消费获准子结果。
- 协作受深度 3、后代 8、累计输入 262144 字节与预留输出 16384 token 限制；不能重复祖先 Bot。停止父工作分别请求关联子工作停止，并显示各自确认、拒绝或未知，不承诺原子停止或副作用回滚。验收入口：`python3 tests/e2e/run.py --scenario bot-collaboration --timeout 100s`。

#### 执行器故障与安全恢复

- `019_safe_runtime_recovery.sql` 持久化认领期限和内置 execute outbox 的代次。第三方成功认领或新业务更新续租 60 秒；长步骤、等待输入及等待授权期间每 20 秒提交 `run.heartbeat`：`{type:"run.heartbeat",run_id,generation,input_version,sequence:0,update_key}`，不带业务状态/正文、不占业务 sequence。每次心跳使用新键；重复 receipt、传输 ACK 和 WS ping 不续租。
- 认领过期显示 `executor_status=lost`；普通工作阶段为 `executor_lost`，等待输入、等待授权及停止未确认保留原语义。公开 `executor_lease_until` 为 Unix 秒。恢复不能把失联解释为实际停工；内置安全步骤由 outbox 再认领，第三方未结束且无未知外部行动的工作须明确 `resume`。新认领推进代次；旧代次的进展、结果、资源、行动及 receipt 回放均被拒绝。终态的同代次、同输入版本、同内容耐久 receipt 可在原租约过期后返回当前授权投影；原代次的无材料停止确认亦可记录实际停工，两者均不续租、不恢复执行权。终态不得复活；继续终态工作使用 `parent_run_id` 创建关联新 Run。
- 平台模型请求快照、已完成轨迹/行动、压缩检查点和 Eino execution checkpoint 分别核对。`020_agent_checkpoint_boundary.sql` 保存框架检查点的分支、来源版本、轨迹水位、接受输入上界及组装版本；检查点与平台事实不匹配时从耐久轨迹恢复，不重放已完成模型或工具事实，不依赖供应商 KV 缓存。聊天消息的编辑、删除或撤回不使已接受快照失效；当前真实授权仍逐边界检查。
- 外部行动成功且 receipt 已耐久提交时复用可核对结果；只读/草稿步骤的未确认往返可安全重试。外部写已派发但没有耐久结果且无外部核对能力时，行动为 `unknown`，工作停在 `waiting_authorization/action_outcome_unknown`，等待人工核对；补充、恢复、接手或重放批准不能盲目重做。已发生副作用不自动回滚，也不承诺外部系统恰好一次。
- 输出请求与最终消息 receipt 使用稳定发布键恢复，跨 domain 不假设原子事务；临时输出丢失不影响权威 Run 查询。公开故障验收：`python3 tests/e2e/run.py --scenario bot-recovery --timeout 100s`，实际 SIGKILL、认领到期/重放及消息服务停服，核对受控外部动作数量和唯一最终消息；`bot-compaction` 另验证重启后的摘要/尾部表示和接受快照。

#### 停止、接手与当前资格

- `008_run_control_membership.sql` 持久化原请求者、后续负责人、显式控制授权及成员资格代次。`takeover` 只改变后续负责人并清空旧显式授权，不改原来源、不转移私人资源许可；退群重入不会复活旧控制权。REST 的 `can_control` 是当前查看者投影，不随共享 WS 或执行器信封广播。
- `stop_status=none|requested|confirmed|rejected|unknown` 与 `stream_status=open|closed` 独立。请求接受或临时断流不等于停工；第三方五秒未确认显示 unknown，只有实际确认才显示已停止。真实完成可赢过未确认停止，在资格仍有效时提交最终消息，不重新开启临时流、不改写已有副作用。
- 内置模型与工具调用继承取消 context，并在后续读取、工具、模型和提交边界重新检查权威资格。负责人离群使工作进入 `waiting_authorization/awaiting_takeover`；仍有权者明确接手共享工作后才继续，第三方必须先确认暂停。Bot 所有权不授予群内容读取权，新人访问执行记录沿用当前成员历史规则。
- 管理员通过现有绑定 PUT 的 `bot_settings` JSON 设置 `enabled:false` 撤销参与许可。移出、全局停用及参与许可撤销立即阻止后续访问与输出并请求停止；Bot/绑定授权代次阻止再次启用或重入恢复旧 Run/交互。撤销后的执行器仅能通过已签发凭据报告无材料的停止确认或拒绝，不能借确认入口读取来源或发布结果。
- 验收：`python3 tests/e2e/run.py --scenario bot-run-controls --timeout 60s`。受控第三方实际计算计数证明确认后停工、未确认和拒绝时仍计算；受控模型与 MCP 网络端证明取消及无后续步骤。`--keep-environment DIR --browser-access` 保留实际浏览器旅程及私有 fixture，凭据不进入版本库。


---

### Realtime 定向投递

`realtime-service` 不拥有会话成员、消息业务判断或未读数；消息域产出的投递意图已包含收件人和客户端信封。

1. **连接登记**：每设备独立 TTL；建立、心跳与断开维护，`generation` 条件更新防止旧连接删除新登记。在线状态查询直接复用登记。
2. **在线投递**：Kafka 单消费组读取 `delivery.requested`；查询目标设备实例，发布到 `rt:node:{instance}`。同实例与跨实例使用同一路径，发送方自己的其它设备也收到回显。
3. **负反馈**：节点无订阅、连接缺失或写失败时清理对应 generation 的登记，记录降级并触发配置启用的 FCM/APNs。没有 ACK、重投或未确认队列。
4. **正确性**：WebSocket 是 best-effort；聊天变化通过用户级收件箱 `position` 增量补齐/重建，不能用 WS 最大 `seq` 推进同步位点；Run 通过当前授权的 REST 查询及自身版本恢复，临时 token、活动与 Run 进度不写用户收件箱。未读由消息域持有。
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
| AI Bot | 独立 Bot 身份、明确交互与持久 Run，Eino ChatModelAgent + Runner，MCP、获准规则与受限委派，流式输出及故障安全恢复 |
| 知识库 | 多格式文档上传，RAG 向量检索问答，混合检索 + Reranker 重排序 |
| MCP 集成 | MCP Server 管理 + 工具发现 + Bot 绑定 + 运行时工具调用 |
| 多端同步 | 同账号多设备消息同步，已读状态一致，离线消息补推 |
| 文件存储 | MinIO Presigned URL，文件/头像/附件统一管理 |
| 离线推送 | 在线 WebSocket 实时推送 + 离线 FCM/APNs 通知 |
| 服务治理 | 超时控制、限流、熔断、重试，YAML + 环境变量配置 |
| 可观测性 | Jaeger 分布式追踪、Prometheus 监控、Grafana 仪表盘、EFK 日志 |
| 部署运维 | k3s 集群，Helmfile 声明式，一键部署脚本 |
