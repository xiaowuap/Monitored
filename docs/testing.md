# 测试与验收

## 自动测试

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

fixture 覆盖：

- hwmon 单位、NaN、原始通道与稳定身份。
- Linux PCI 地址变化、USB/EDAC/DRM/devlink、thermal/NIC、FreeBSD sysctl、ATA/NVMe/SCSI SMART（含零计数、HBA 类型、packed 温度）、ZFS pool/vdev/dataset/snapshot/ARC、NVML fallback、Mellanox MFT、IPMI。
- TrueNAS WebSocket JSON-RPC 与 `disk.temperatures` 阈值/回退、Redfish 超媒体链接、NUT 文本协议、SNMP profile/transform。
- gRPC 注册/快照/样本/心跳和证书身份匹配。
- WAL 顺序恢复、过期丢弃与 gap。
- 规则 duration/recovery/maintenance/counter reset/missing。
- AES-GCM 目的绑定与篡改、TOTP RFC 向量、动作 shell 注入/哈希/root 身份边界、安全 LLM JSON/prompt boundary、Webhook HMAC/retry。
- Web 构建、渲染契约、SNMP schema 和 OpenAPI。

## Compose smoke

脚本使用独立 project name、随机宿主机端口和临时 volumes，启动 PostgreSQL/Redis/Master/Worker，并验证：

- 新 Live 数据库没有 Demo fixture。
- 全部只读资源、真实 ingest 和 Master 侧 HTTP connection 轮询。
- 通知 channel/route/test queue。
- 规则进入 Firing 并自动关联 Incident。
- 未配置 provider 时，标注与诊断队列明确进入 failed，且不影响采集。

脚本结束时自动删除临时容器与 volumes。

## 容量

```bash
go run ./cmd/loadgen \
  -endpoint http://localhost:8080/api/v1/ingest \
  -token "$MONITORED_AGENT_TOKEN" \
  -nodes 50 -sensors 20000 -rate 2000 -batch 1000 -duration 60m
```

验收目标：

- 50 节点、500 磁盘、至少 20,000 传感器。
- 持续 2,000 samples/s。
- 常用 24h 图表 p95 < 2s。
- 一小时断线在 WAL 上限内不丢样。
- PostgreSQL、Redis、LLM、通知分别故障时行为符合 operations 文档。

仓库提供负载驱动器和功能测试，但具体硬件/存储上的容量数字必须在目标部署环境实测，不能把开发机结果当作普遍性能承诺。
