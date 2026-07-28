# API

REST 基址为 `/api/v1`，完整机器可读契约见 `openapi/monitored.v1.yaml`。

主要只读资源：

| 路径 | 内容 |
| --- | --- |
| `/overview` | 总体健康、数量、摄取速率与趋势 |
| `/assets`、`/sensors`、`/sensors/{id}/metrics` | 拓扑、定义、最新值与历史 |
| `/alerts`、`/incidents`、`/diagnoses` | 告警与诊断 |
| `/discovery/runs`、`/discovery/scans`、`/connections`、`/agents` | Agent 发现、受限网络扫描和接入 |
| `/agents/enrollment-tokens`、`/agents/enroll` | 一次性 Agent 配对与短期 mTLS 证书签发 |
| `/agent-update-packages`、`/agents/{id}/updates` | Master 更新缓存与受审计的 Agent 更新下发 |
| `/rules`、`/maintenance-windows` | 确定性规则 |
| `/actions`、`/notification-channels`、`/notification-routes` | 审批与通知 |
| `/users`、`/audit`、`/capabilities` | 治理与能力 |
| `/llm/suggestions`、`/llm/suggestion-jobs`、`/diagnoses` | 人工触发的标注与诊断工作流 |

写操作使用 JSON，返回统一错误：

```json
{"error":{"code":"invalid_request","message":"..."}}
```

浏览器使用 `monitored_session` Cookie。兼容 REST Agent 只允许 Bearer token 访问 `/ingest`，单批有严格基数和体积限制。

SSE `/events` 事件包含 telemetry.ingested、discovery.progress/completed、suggestion.reviewed 与 action.reviewed。

TOTP 使用 `/auth/totp/enroll` 和 `/auth/totp/confirm` 两步启用。启用后 `/auth/login` 需要 `totpCode`。root-risk 动作审批还必须在 decision 请求中再次提交 `password` 与 `totpCode`。

Agent 正式接口是 `AgentControl.Connect` 双向 gRPC。协议见 `proto/monitored/v1/agent.proto`，包含 Registration、CapabilityManifest、AssetSnapshot、SampleBatch、Heartbeat、GapReport、DiscoveryTask、DiagnosticTask、ApprovedAction、AgentUpdateTask、TaskResult、Acknowledgement、CertificateSigningRequest 和 CertificateBundle。
