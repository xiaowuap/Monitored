# 连接配置

未设置 `agentId` 的连接由 Worker 直接轮询；绑定 Agent 的连接保留给现场 Agent/任务编排。所有 `secret` 在 Master 侧以目的绑定的 AES-256-GCM 密文保存，GET API 只返回 `hasCredentials`。

创建：

```http
POST /api/v1/connections
Content-Type: application/json

{
  "name": "rack-a-bmc",
  "kind": "redfish",
  "endpoint": "https://10.20.0.31",
  "tlsMode": "custom_ca",
  "enabled": true,
  "config": {},
  "secret": {
    "username": "monitored-readonly",
    "password": "...",
    "caPEM": "-----BEGIN CERTIFICATE-----..."
  }
}
```

## 类型与字段

| kind | endpoint | config | secret |
| --- | --- | --- | --- |
| `truenas` | `https://nas`、`wss://nas/api/current` | 无必填 | `token`；custom CA 时 `caPEM` |
| `redfish` | BMC HTTPS 根地址 | 无必填 | `username`/`password` 或 `token`；可含 `caPEM` |
| `ipmi` | `host` 或 `host:623` | 无必填 | `username`、`password` |
| `nut` | `host` 或 `host:3493` | 无必填 | 可选 `username`、`password` |
| `snmp` | `host`、`host:161` 或 `snmp://host:161` | `profile`、`version`、v3 的 `authProtocol`/`privProtocol`/`contextName` | v2c 的 `community`；v3 的 `username`/`authPassword`/`privPassword` |
| `ssh` | `host` 或 `host:22` | 必填 `hostKeySHA256` | `username`，以及 `password` 或 `privateKey`；可选 `privateKeyPassword` |
| `http` | 完整 HTTP(S) URL | 可选 `method` (`GET`/`HEAD`) 与 `expectedStatus` | 可选 Basic `username`/`password` 或 `token` |
| `tcp` | `host:port` | 无必填 | 无 |

`tlsMode`：

- `verify`：使用系统 CA 验证，默认值。
- `custom_ca`：仍验证主机名，并把 secret 中的 `caPEM` 加入信任池。
- `insecure`：显式跳过证书验证，只应用于隔离网络中的临时排障。

SSH 必须提供 SHA-256 host key pin，不能使用 `InsecureIgnoreHostKey`。IPMI 使用 `lanplus`，密码通过子进程环境传递，不拼接到 argv。SNMP profile 文件名不能包含路径；profile 只允许声明式 OID、枚举和线性换算。

## 调度与状态

Worker 使用 `MONITORED_CONNECTION_CONCURRENCY` 控制并发，以 `MONITORED_CONNECTION_TIMEOUT` 限制单次采集。轮询成功后写入资产/传感器/样本和 capability；目标返回 down/error 时仍保存错误质量样本，同时连接状态标记 failed/degraded。Worker 崩溃不会丢失连接定义，下一周期会重新选择任务。

生产凭据应只授予只读权限。TrueNAS 只读 API、写 API、SSH 和 Agent 执行身份必须分离。
