# 运维

## 健康与日志

`GET /healthz` 检查 PostgreSQL。Compose healthcheck 使用同一端点。所有 Go 服务输出结构化 JSON 日志：

```bash
docker compose ps
docker compose logs -f server worker local-agent
```

`/api/v1/agents` 显示 lastSeen、证书 serial、能力、WAL 字节、最旧 WAL 时间和最近缺口。`/api/v1/capabilities` 显示设计能力；实际设备状态以 Agent/connection 探测为准。

## 保留与分区

Worker 默认每分钟执行幂等聚合：

- raw 10s：7 天
- 1m：90 天
- 1h：2 年

当前清理可处理 default partition；大型安装应按日预创建分区并通过删除完整过期分区降低 vacuum 压力。

## 故障行为

- PostgreSQL 不可用：Agent 写入 WAL；Master 明确返回 degraded。
- Redis 不可用：基础采集和 PostgreSQL 入库继续。
- LLM 不可用：规则与采集继续；标注/诊断 job 记录 failed，可重新显式提交。
- 通知失败：数据库队列指数退避，最多 8 次后 failed。
- Agent 断线：24h/1GiB 内顺序恢复；超过上限记录 gap。

## 日常检查

```bash
curl http://localhost:8080/healthz
docker compose exec postgres psql -U monitored -d monitored -c \
  "select state,count(*) from alerts group by state"
docker compose exec postgres psql -U monitored -d monitored -c \
  "select status,count(*) from notification_deliveries group by status"
docker compose exec postgres psql -U monitored -d monitored -c \
  "select status,count(*) from diagnoses group by status"
```

备份、恢复和升级见 [Backup & Upgrade](backup-upgrade.md)。
