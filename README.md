# Monitored

Monitored 是设备遥测优先的智能可观测平台：先完整保留硬件原始身份，再用确定性规则告警，最后让人审核 LLM 标注、诊断和操作建议。交付形态是自部署 Docker Compose，不依赖任何云端托管服务。

## 两种明确的运行模式

- **Demo Mode**：WebUI 使用内置、确定性的样例数据，不连接 Master、不保存凭据、不发送通知、不执行动作。页面顶部始终显示 `DEMO MODE · 演示模式`。
- **Live Mode**：WebUI 只读取配置的自部署 Master。API 断开时显示真实错误/空状态，绝不静默回退到演示数据。

只启动 Demo：

```bash
docker compose -f compose.demo.yaml up --build
```

打开 `http://localhost:3000`。这是本地容器，不会发布到云端。

## 自部署快速启动

要求 Docker 24+ 与 Docker Compose v2：

```bash
cp .env.example .env
# 修改数据库密码、Master key、管理员密码和 Agent token
docker compose up --build
```

打开 `http://localhost:3000`。生产环境必须把 `MONITORED_AUTH_DISABLED=false`，并使用反向代理为 Web/API 提供 HTTPS。

启用本机 Agent 的开发链路：

```bash
docker compose --profile agent up --build
```

Compose 的默认 gRPC 是显式的本机开发非加密模式。正式 Agent 使用 mTLS：

```bash
./scripts/generate-agent-certs.sh ./secrets/pki local-agent server
docker compose -f compose.yaml -f compose.mtls.yaml --profile agent up --build
```

证书签发、远程 Agent 和轮换步骤见 [Agent mTLS](docs/agent-mtls.md)。

## 已实现的主链路

- Go Master/Worker/Linux-FreeBSD Agent；REST/OpenAPI、SSE、Protobuf 双向 gRPC。
- gRPC mTLS 客户端证书身份绑定、Agent 注册/能力/发现快照/样本/心跳/WAL 缺口/任务 ACK。
- 24 小时或 1 GiB 有界 Agent WAL；恢复后顺序补传，因保留上限丢弃时上报时间缺口。
- PostgreSQL 资产拓扑、传感器身份、分区样本、1 分钟/1 小时聚合、规则、告警、Incident、诊断、动作、通知、用户和哈希链审计。
- Linux hwmon、PCI、USB、block、NIC、thermal、power_supply、EDAC、DRM、devlink、InfiniBand/RDMA；FreeBSD sysctl 基线。
- standby-safe ATA/NVMe/SCSI SMART、多温度、ZFS pool/vdev/scrub/dataset/snapshot/ARC、动态加载 NVML 与 `nvidia-smi` fallback、MFT/IPMI 本机只读采集。
- Worker 直接轮询 TrueNAS JSON-RPC、Redfish、IPMI、NUT、SNMP v2c/v3、严格 host-key SSH、HTTP/TCP；Agent 也可补充 TrueNAS SCALE 本机视角。
- Admin 可向指定 Agent 下发有界、限速且不猜测凭据的 Redfish/IPMI/SNMP/NUT CIDR 扫描。
- 阈值/变化率/计数器增量/状态/缺失/持续时间/恢复/维护窗口规则状态机。
- Telegram、飞书、Webhook(HMAC)、SMTP、Bark 通知及持久化指数退避重试。
- OpenAI-compatible 与 Ollama 的待审核传感器标注和结构化诊断；自动诊断默认关闭，证据按不可信输入处理，模型不拥有告警或执行权。
- 无 Shell 受限动作执行器：允许列表、真实 OS 身份边界、不可变哈希、一次性短期令牌、低权限默认、超时、进程组终止和输出上限；root 需 Admin 密码 + TOTP 再认证和 Agent 显式启用。
- Viewer/Operator/Admin RBAC、多用户 bcrypt 密码、自包含 Live/Demo WebUI 和设备专页。

硬件功能具有运行时 capability 状态；缺少工具、权限或字段时返回 unavailable/degraded，不把不支持值写成零。详细矩阵见 [Collectors](docs/collectors.md)，实际交付深度见 [Roadmap 状态](docs/roadmap-status.md)。

## 仓库结构

```text
app/                    React/TypeScript WebUI
cmd/server              REST/SSE 与 gRPC Master
cmd/worker              聚合、保留、规则与通知队列
cmd/agent               Linux/FreeBSD Agent
cmd/loadgen             20k 传感器 / 2k samples/s 容量驱动器
gen/                    生成的 Protobuf Go 代码
internal/adapter        TrueNAS、Redfish、IPMI、NUT、SNMP、SSH、HTTP/TCP
internal/collector      本机硬件 collectors
internal/grpcapi        双向 Agent 控制面
internal/rules          确定性规则引擎
internal/action         受限动作执行器
internal/notifier       五种通知传输
internal/llm            可选诊断 providers
internal/security       AES-256-GCM 凭据封装
internal/store          PostgreSQL repository 与 migrations
openapi/                REST OpenAPI 3.1
proto/                  Agent Protobuf
profiles/snmp           SNMP profile schema 与示例
docs/                   部署、安全、运维和测试文档
```

## 验证

```bash
go test ./...
CGO_ENABLED=0 go test ./...
go test -race ./...
go vet ./...
npm test
npm run audit:prod
npx tsc --noEmit
npm run lint
docker build --target test -t monitored/test:dev .
./scripts/smoke-compose.sh
```

容量驱动：

```bash
go run ./cmd/loadgen \
  -endpoint http://localhost:8080/api/v1/ingest \
  -token "$MONITORED_AGENT_TOKEN" \
  -nodes 50 -sensors 20000 -rate 2000 -duration 10m
```

测试方法、达标判定与非承诺项见 [Testing](docs/testing.md)。

## 文档

- [架构](docs/architecture.md)
- [部署](docs/deployment.md) · [Demo Mode](docs/demo-mode.md) · [Agent mTLS](docs/agent-mtls.md)
- [TrueNAS SCALE Agent](docs/truenas-scale-agent.md)
- [Collectors](docs/collectors.md) · [连接配置](docs/connections.md) · [LLM](docs/llm.md) · [API](docs/api.md)
- [安全](docs/security.md) · [运维](docs/operations.md) · [备份与升级](docs/backup-upgrade.md)
- [测试](docs/testing.md) · [故障排查](docs/troubleshooting.md) · [Roadmap 状态](docs/roadmap-status.md)

## 许可证

AGPL-3.0-or-later，参见 [LICENSE](LICENSE)。
