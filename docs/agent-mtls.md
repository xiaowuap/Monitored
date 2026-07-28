# Agent mTLS、自动配对与证书轮换

## 信任模型

Agent 只建立出站连接。遥测和任务使用 TLS 1.3 双向认证；Master 把
`agent_id` 绑定到客户端证书的 CN、DNS SAN 或
`spiffe://monitored.local/agent/<agent_id>` URI SAN。

自动配对采用以下流程：

1. Admin 在“设置 → Agent 与证书”中为指定 Agent ID 创建一次性令牌。
2. Agent 在本机生成 Ed25519 私钥和 CSR，通过 Master 的 HTTPS API 提交 CSR 与令牌。
3. Master 只保存令牌的 SHA-256 哈希，原子消费令牌后返回短期客户端证书和 Agent CA 公钥。
4. Agent 把证书、私钥和 CA 作为单个 `0600` 凭据文件原子保存，再建立 mTLS gRPC 流。
5. 到达续签窗口后，Agent 生成一把新私钥，通过已经认证的 gRPC 流交换 CSR；安装新证书后自动重连。

私钥始终留在 Agent 上。一次性令牌默认 15 分钟失效且只能用于它绑定的
Agent ID；客户端证书默认有效 30 天，并提前 7 天续签。

## Master 配置

Master 的 HTTPS 公网地址必须可由待添加设备验证；本机 loopback 开发环境除外。
gRPC 服务证书 SAN 必须覆盖 Agent 使用的 Master 主机名。

```dotenv
MONITORED_PUBLIC_URL=https://monitored.example
MONITORED_GRPC_ADDR=:9443
MONITORED_GRPC_PUBLIC_TARGET=monitored.example:9443
MONITORED_GRPC_INSECURE=false
MONITORED_GRPC_CERT_FILE=/run/secrets/server.crt
MONITORED_GRPC_KEY_FILE=/run/secrets/server.key
MONITORED_GRPC_CLIENT_CA_FILE=/run/secrets/agent-ca.crt

# 启用配对和自动续签；CA 私钥只读挂载，仅 Master 可读。
MONITORED_AGENT_CA_CERT_FILE=/run/secrets/agent-ca.crt
MONITORED_AGENT_CA_KEY_FILE=/run/secrets/agent-ca.key
MONITORED_AGENT_CERTIFICATE_TTL=720h
MONITORED_AGENT_ENROLLMENT_TTL=15m
```

`MONITORED_AGENT_CA_CERT_FILE` 必须与 gRPC 的 Client CA 信任相容。生产环境应把
CA 私钥放在受限 secret/HSM 挂载中，并限制 Master 容器和备份的访问；不应把它挂载到
Agent、Worker 或 Web。

小型安装仍可生成初始 CA 和服务端证书：

```bash
./scripts/generate-agent-certs.sh ./secrets/pki local-agent server
docker compose -f compose.yaml -f compose.mtls.yaml up -d --build
```

## 添加设备

在 Live Mode 打开“设置 → Agent 与证书”，输入例如 `edge-01`，复制生成的配对命令到
目标设备运行。等价的环境变量如下：

```dotenv
MONITORED_AGENT_PROTOCOL=grpc
MONITORED_SERVER_URL=https://monitored.example
MONITORED_GRPC_TARGET=monitored.example:9443
MONITORED_AGENT_NAME=edge-01
MONITORED_AGENT_GRPC_INSECURE=false
MONITORED_AGENT_ENROLLMENT_TOKEN=menr_one_time_value
MONITORED_AGENT_CREDENTIAL_FILE=/var/lib/monitored-agent/credentials.json
```

首次连接成功后，删除环境或编排配置中的
`MONITORED_AGENT_ENROLLMENT_TOKEN`。其余连接参数应保留供服务重启使用；凭据文件必须位于
Agent 可写的持久状态目录中。systemd 示例已将该目录限制为
`/var/lib/monitored-agent`。

也可直接调用 Admin API：

```http
POST /api/v1/agents/enrollment-tokens
Content-Type: application/json

{"agentId":"edge-01"}
```

响应只显示一次明文令牌，并同时返回可复制的环境变量。

## 自动轮换与故障恢复

Agent 每小时检查证书剩余有效期；进入
`MONITORED_AGENT_CERT_RENEW_BEFORE`（默认 `168h`）窗口后自动换钥续签。Master 只接受来自
已通过 mTLS 验证且身份一致的流的续签请求；开发用 insecure gRPC 明确禁止签发。

新凭据会先完成私钥匹配、Agent 身份、CA 链和 ClientAuth EKU 验证，再原子替换旧凭据。
若签发、落盘或重连失败，当前连接和旧凭据不变；Agent 继续使用有界 WAL。恢复连接后按序补传，
超过 24 小时或 1 GiB 的丢弃会作为 gap report 上报。
若设备离线时间超过整个证书有效期，Admin 为同一 Agent ID 创建一个新的配对令牌即可恢复，
无需复制或删除旧私钥文件。

运维可通过 `/api/v1/agents` 的 `certificateId` 确认序列号已经变化。

## 兼容静态证书

已有部署仍可提供：

```dotenv
MONITORED_AGENT_TLS_CERT_FILE=/etc/monitored/edge-01.crt
MONITORED_AGENT_TLS_KEY_FILE=/etc/monitored/edge-01.key
MONITORED_AGENT_TLS_CA_FILE=/etc/monitored/master-ca.crt
MONITORED_AGENT_TLS_SERVER_NAME=monitored.example
```

未设置一次性令牌且不存在托管凭据文件时，Agent 会继续使用这条静态 mTLS 路径。静态证书不会由
Agent 自动替换；迁移时为同一 Agent ID 生成一次配对令牌即可。
