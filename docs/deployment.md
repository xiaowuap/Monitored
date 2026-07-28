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

## 生产 mTLS

按 [Agent mTLS](agent-mtls.md) 配置证书。Compose 自带 `compose.mtls.yaml` 作为文件挂载示例：

```bash
./scripts/generate-agent-certs.sh ./secrets/pki local-agent server
docker compose -f compose.yaml -f compose.mtls.yaml --profile agent up -d --build
```

脚本证书适合小型自管安装。大型环境应使用内部 CA、短期证书和自动轮换。

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
