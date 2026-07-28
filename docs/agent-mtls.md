# Agent mTLS 与证书轮换

## 信任模型

Agent 只建立出站 gRPC 长连接。Master 要求客户端证书由配置的 Client CA 签发，并把 envelope 的 `agent_id` 绑定到证书 CN、DNS SAN 或 URI SAN 的末段。TLS 最低版本为 1.3。

## 小型安装

```bash
./scripts/generate-agent-certs.sh ./secrets/pki local-agent server
docker compose -f compose.yaml -f compose.mtls.yaml --profile agent up -d --build
```

脚本不会覆盖已有私钥。生成：

- `ca.crt/ca.key`
- `server.crt/server.key`，SAN 包含 `server`、`localhost`、`127.0.0.1`
- `local-agent.crt/local-agent.key`，90 天有效，身份为 `local-agent`

Agent 的 `MONITORED_AGENT_NAME` 必须与证书身份一致。
Compose 只把 CA 公钥和各进程自己的证书/私钥挂载进容器；CA 私钥不会进入运行容器。运行私钥为 `0640`，通过只读附加组访问。

## 远程 Agent

Master：

```dotenv
MONITORED_GRPC_ADDR=:9443
MONITORED_GRPC_INSECURE=false
MONITORED_GRPC_CERT_FILE=/run/secrets/server.crt
MONITORED_GRPC_KEY_FILE=/run/secrets/server.key
MONITORED_GRPC_CLIENT_CA_FILE=/run/secrets/agent-ca.crt
```

Agent：

```dotenv
MONITORED_AGENT_PROTOCOL=grpc
MONITORED_GRPC_TARGET=master.example:9443
MONITORED_AGENT_NAME=edge-01
MONITORED_AGENT_TLS_CERT_FILE=/etc/monitored/edge-01.crt
MONITORED_AGENT_TLS_KEY_FILE=/etc/monitored/edge-01.key
MONITORED_AGENT_TLS_CA_FILE=/etc/monitored/master-ca.crt
MONITORED_AGENT_TLS_SERVER_NAME=master.example
```

## 轮换

1. 用相同 Agent 身份签发新证书。
2. 原子替换 Agent 的 cert/key 文件，权限保持 0600。
3. 重启 Agent；Master 会看到新的 certificate serial。
4. 确认 `/api/v1/agents` 的 lastSeen 与 certificateId 更新。
5. 吊销旧证书；当前实现使用 CA 信任与数据库 `revoked` 状态，生产 PKI 可在入口代理实施 CRL/OCSP。

若轮换失败，Agent 继续写入有界 WAL。恢复连接后按顺序补传；超过 24h/1GiB 的丢弃会作为 gap report 上报。

`MONITORED_GRPC_INSECURE=true` 与 `MONITORED_AGENT_GRPC_INSECURE=true` 只允许隔离的本机开发环境。
