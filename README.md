# Monitored

设备遥测优先的智能可观测平台。Monitored 把“先完整发现、再确定性告警、最后由人审核智能建议”作为默认工作流，面向 Linux/FreeBSD 主机、存储、GPU、网卡、BMC、UPS 与 SNMP 设备。

> 当前仓库是可运行的首个纵向 MVP，不代表计划中的每一种硬件适配器都已完成。它交付了数据模型、Linux hwmon 采集、Agent WAL、REST/SSE、操作工作流、真实 PostgreSQL 种子数据、Worker 保留策略以及完整 WebUI；gRPC/mTLS 运行时、SMART/ZFS/TrueNAS/NVIDIA/Mellanox/Redfish/IPMI/NUT/SNMP 采集器和五种通知通道仍按路线图逐步接入。

## 已实现

- Go 控制面：健康检查、认证会话、资产/传感器/指标、发现任务、告警、Incident、LLM 建议审核、动作审批、审计与 Agent ingestion。
- PostgreSQL：资产父子拓扑、稳定传感器身份、指标序列、按时间分区的原始样本、1 分钟/1 小时聚合、发现/规则/告警/Incident/建议/动作以及追加式哈希链审计。
- Worker：幂等降采样与默认 `7d / 90d / 2y` 保留策略。
- Linux Agent：完整枚举 `/sys/class/hwmon/*/*_input`，保留原始 chip/channel/path，规范化为 UCUM 单位；网络中断时使用 24 小时或 1 GiB 的本地有界 WAL 顺序补传。
- React 控制台：中英双语、响应式导航、总览、资产拓扑、发现中心、告警、Incident、诊断、动作审批、通知、审计和设置；支持真实 API，API 不可达时回退为确定性的演示数据。
- 公共契约：[OpenAPI 3.1](openapi/monitored.v1.yaml)、[Agent 双向流 Protobuf](proto/monitored/v1/agent.proto)、稳定 Go 扩展接口与声明式 [SNMP profile schema](profiles/snmp/schema.json)。
- Docker Compose：`server`、`worker`、`web`、`postgres`、`redis`，以及可选 `local-agent` profile。

## 架构

```text
Linux Agent ── REST ingest ──► Go Server ──► PostgreSQL
    │             │               │              ▲
    └─ bounded WAL│               ├─ SSE         │
                  │               └─ REST API     │
                  │                    ▲          │
                  └────────────────────┤          │
                                   React UI   Go Worker

Redis 是可选的队列/实时状态依赖；它的故障不会阻塞基础采集入库。
```

仓库按一个领域模型组织：

```text
app/                       React/TypeScript 控制台
cmd/server                 Master REST/SSE 入口
cmd/worker                 降采样与保留策略 Worker
cmd/agent                  Linux hwmon Agent
internal/agent             WAL 与 ingestion 客户端
internal/collector         硬件采集器
internal/domain            公共领域模型
internal/extension         Collector/Notifier/LLM/Action 等稳定接口
internal/httpapi           认证、API、SSE
internal/store             PostgreSQL repository 与 migration
openapi/                   REST/OpenAPI 3.1
profiles/snmp              声明式厂商 profile 与 JSON Schema
proto/                     Agent gRPC 协议
```

## 快速启动

要求 Docker 24+ 和 Docker Compose v2：

```bash
cp .env.example .env
docker compose up --build
```

打开 `http://localhost:3000`。本地 Compose 默认把 `MONITORED_AUTH_DISABLED` 设为 `true`，方便首次体验；用于任何共享环境前必须在 `.env` 中改为 `false`，并替换管理密码、Agent token、主密钥和数据库密码。

启用本机 Agent：

```bash
docker compose --profile agent up --build
```

该容器只读挂载宿主机 `/sys` 和 machine-id，并把 WAL 放在持久卷中。若不希望暴露宿主机硬件信息，不要启用此 profile。

仅启动依赖和 Go 服务：

```bash
docker compose up -d postgres redis server worker
curl http://localhost:8080/healthz
curl http://localhost:8080/api/v1/overview
```

## 本地开发

要求 Go 1.23+ 与 Node.js 22.13+：

```bash
npm install
npm run dev
```

控制台默认读取演示数据。连接本地 Go API：

```bash
NEXT_PUBLIC_API_BASE=http://localhost:8080/api/v1 npm run dev
```

后端：

```bash
docker compose up -d postgres redis
MONITORED_DATABASE_URL='postgres://monitored:monitored@localhost:5432/monitored?sslmode=disable' \
MONITORED_AUTH_DISABLED=true \
go run ./cmd/server
```

Worker 和 Agent 分别通过 `go run ./cmd/worker` 与 `go run ./cmd/agent` 启动；所有配置及安全敏感项见 [.env.example](.env.example)。

## API

浏览器 API 位于 `/api/v1`，使用 HMAC 签名、HttpOnly、SameSite=Strict 的会话 Cookie。Agent 只允许通过独立 bearer token 访问 `/api/v1/ingest`。关键入口：

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/healthz` | 数据库就绪状态 |
| `POST` | `/api/v1/auth/login` | 创建管理会话 |
| `GET` | `/api/v1/overview` | 健康、摄取速率与趋势 |
| `GET` | `/api/v1/assets` | 资产和设备摘要 |
| `GET` | `/api/v1/sensors` | 定义、原始身份和最新样本 |
| `GET` | `/api/v1/sensors/{id}/metrics` | 24 小时等时间窗趋势 |
| `POST` | `/api/v1/discovery/runs` | 创建发现任务 |
| `POST` | `/api/v1/llm/suggestions/{id}/decision` | 接受/拒绝标注建议 |
| `POST` | `/api/v1/actions/{id}/decision` | 管理员审批/拒绝动作 |
| `GET` | `/api/v1/events` | SSE 实时事件 |
| `POST` | `/api/v1/ingest` | Agent 批量写入遥测 |

请求/响应模式和错误模型以 [OpenAPI 文件](openapi/monitored.v1.yaml) 为准。

## 数据与安全边界

- 原始传感器名称、单位、来源和路径不可被 LLM 建议覆盖；接受建议只会写展示别名。
- LLM 不拥有告警触发权，也不能自动执行命令。动作在审批时固定目标、命令、工作目录、身份、超时和不可变哈希。
- Ingest 单批限制为 500 个资产、5,000 个传感器和 20,000 个样本，避免单 Agent 失控放大序列基数。
- 审计记录采用 previous-hash/entry-hash 链，可发现应用层篡改；它不声称防御同时掌握数据库与主密钥的宿主机管理员。
- 示例密钥、密码与开放认证模式只适用于本地开发。生产部署应使用外部 secret 管理、TLS、强随机凭据，并把数据库与 API 放在受控网络。
- 当前 REST Agent 通道用于 MVP 验证；计划中的远程 Agent 正式信任边界是出站 mTLS/gRPC、证书轮换和短期任务凭据。
- 未实现的动作执行器不会执行任何命令。界面审批仅记录决策和审计，保留安全默认。

## 验证

```bash
# Web 构建、渲染契约、TypeScript 与 lint
npm test
npx tsc --noEmit
npm run lint

# Go 单元/竞态检查
go test ./...
go test -race ./...

# 与 CI 相同的容器化 Go 测试
docker build --target test -t monitored/test:dev .
```

现有 fixture 覆盖 hwmon 数值/单位规范化、NaN 忽略与稳定身份；WAL 覆盖失败后保留未发送尾部及顺序恢复；认证覆盖会话签名、篡改和过期。Compose 冒烟测试覆盖 migration、健康检查、总览、资产、发现任务与 Agent ingestion。

容量目标仍是验收门槛而非当前仓库的性能承诺：50 节点、500 磁盘、20,000+ 传感器、持续 2,000 samples/s，常用 24 小时图表 p95 < 2 秒。

## 下一阶段

1. 生成并接通 mTLS/gRPC Agent runtime、证书轮换和任务 ACK。
2. 用录制 fixture 实现 PCI/USB、SMART/NVMe、ZFS 和 FreeBSD Collector。
3. TrueNAS SCALE/CORE、NVML/DCGM、ethtool/RDMA/MFT、Redfish/IPMI、NUT 和 SNMP 标准 MIB 适配。
4. 完整规则状态机、Incident 关联、通知重试与 OpenAI-compatible/Ollama provider。
5. 只读诊断任务和经过二次认证的一次性动作执行器；再进行安全、故障和容量基准验收。

## 许可证

AGPL-3.0-or-later。参见 [LICENSE](LICENSE)。
