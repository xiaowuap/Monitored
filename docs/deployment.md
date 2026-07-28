# 自部署

## 最小要求

- Docker 24+、Docker Compose v2。
- amd64 或 arm64 Linux 主机；PostgreSQL 数据盘建议使用本地 SSD。
- Web/API 对用户暴露时使用 HTTPS；Agent 端口使用 mTLS。
- 生产建议至少 4 vCPU、8 GiB RAM；容量目标环境建议为 PostgreSQL 预留 16 GiB 以上内存和足够 IOPS。

## Live Mode

```bash
cp .env.example .env
```

至少修改：

```dotenv
MONITORED_POSTGRES_PASSWORD=<random>
MONITORED_MASTER_KEY=<32+ random characters>
MONITORED_AGENT_TOKEN=<random compatibility token>
MONITORED_ADMIN_PASSWORD=<strong password>
MONITORED_AUTH_DISABLED=false
NEXT_PUBLIC_MONITORED_MODE=live
NEXT_PUBLIC_API_BASE=https://monitored.example/api/v1
```

然后：

```bash
docker compose up -d --build
docker compose ps
curl http://localhost:8080/healthz
```

Web 默认端口 3000、REST 8080、Agent gRPC 9443。PostgreSQL/Redis 默认不发布宿主机端口。

首次登录后可调用 `/api/v1/auth/totp/enroll`，把返回 URI 导入认证器，再用 `/api/v1/auth/totp/confirm` 确认。启用后登录需要六位 `totpCode`。

Worker 的 TrueNAS/Redfish/IPMI/NUT/SNMP/SSH/HTTP/TCP 连接无需额外容器；配置见 [连接配置](connections.md)。OpenAI-compatible/Ollama 为可选本地或外部 provider，见 [LLM](llm.md)。

## Release 安装

`v*` tag 会触发 GitHub Release 工作流。工作流先执行 Go/Web 全量检查，再生成
amd64/arm64 Linux 与 amd64 FreeBSD Agent 二进制包、Web standalone 包、SHA256 校验和，并向 GHCR 发布
server、worker、web 和 agent 多架构镜像。

下载 Release 中的 `compose.release.yaml` 与 `release.env.example`：

```bash
cp release.env.example .env
# 修改 .env 中的所有 replace-with 值、公开 URL 和持久化目录
docker compose --env-file .env -f compose.release.yaml pull
docker compose --env-file .env -f compose.release.yaml up -d
curl http://localhost:8080/healthz
```

官方 GHCR 镜像为 public，可匿名拉取。升级时先把 `.env` 的
`MONITORED_VERSION` 改为目标 Release 版本，再执行：

```bash
./scripts/update-release.sh .env compose.release.yaml
```

生产数据目录与 PKI 目录必须位于持久化存储。Agent CA 私钥由 Master signer
只读挂载，不得进入其他容器；Agent 的托管凭据文件保持 `0600`。
Agent 更新包缓存在 `${MONITORED_DATA_DIR}/update-cache`，升级或重建 Master 时必须保留。
Release Compose 使用 bind mount；首次启动前创建目录并允许 Server 的 UID 65532 写入：

```bash
install -d -o 65532 -g 0 -m 0770 "${MONITORED_DATA_DIR}/update-cache"
```

远程 Linux Agent 使用 Release 中对应架构的 `monitored-agent_*` 包、
`deploy/systemd/monitored-agent.service` 和一次性配对令牌。每台主机的
`MONITORED_AGENT_NAME` 必须唯一，并保持 `MONITORED_ALLOW_ROOT_ACTIONS=false`。
systemd Agent 可通过本机策略启用原子二进制更新；Docker digest pull 的权限边界与配置见
[Agent 自主更新](agent-updates.md)。

## 生产 mTLS

按 [Agent mTLS](agent-mtls.md) 配置证书。Compose 自带 `compose.mtls.yaml` 作为文件挂载示例：

```bash
./scripts/generate-agent-certs.sh ./secrets/pki local-agent server
docker compose -f compose.yaml -f compose.mtls.yaml --profile agent up -d --build
```

脚本证书适合小型自管安装。Master 挂载 Agent CA key 后，Admin 可在
“设置 → Agent 与证书”中生成一次性配对命令；Agent 会在本地生成私钥并自动换钥续签。
大型环境可把相同 signer 边界接到受控内部 CA/HSM。

Compose Agent 固定以非 root UID 65532 运行。它内置 smartctl、ipmitool、ethtool 和 devlink，但读取宿主设备仍需逐个映射并授予最小权限。只有确有需要的现场 Agent 才应以 root-capable service 运行，并同时设置 `MONITORED_ALLOW_ROOT_ACTIONS=true`；Master 端仍会要求 Admin 密码与 TOTP 二次认证。

TrueNAS SCALE 上的设备映射、SMART 权限和可选 JSON-RPC 配置见
[TrueNAS SCALE Agent](truenas-scale-agent.md)。

## 反向代理

代理 `/` 到 Web 3000，代理 `/api/v1` 与 `/healthz` 到 Master 8080。SSE 路径 `/api/v1/events` 需关闭响应缓冲并允许长连接。gRPC 9443 使用 HTTP/2 透传，不应终止为普通 HTTP/1.1。

`MONITORED_PUBLIC_URL` 必须与浏览器 Origin 完全匹配；只有 localhost 环境允许本地端口变化。

## 离线安装

在联网构建机执行：

```bash
docker compose build
docker save monitored-server monitored-worker monitored-web monitored-local-agent \
  postgres:17-alpine redis:7.4-alpine > monitored-images.tar
```

在目标机 `docker load -i monitored-images.tar` 后使用同一仓库的 Compose 和 `.env` 启动。生成的 Protobuf 代码已提交，不要求目标机安装 protoc。

## Demo

```bash
docker compose -f compose.demo.yaml up -d --build
```

Demo 只有 Web 容器，详见 [Demo Mode](demo-mode.md)。
