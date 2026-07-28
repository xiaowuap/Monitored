# 备份、恢复与升级

## 备份

数据库是事实来源：

```bash
docker compose exec -T postgres \
  pg_dump -U monitored -d monitored -Fc > monitored-$(date +%F).dump
```

另外备份 `.env`、外部 secret、Agent CA/证书和自定义 `profiles/snmp/*.yaml`。私钥备份必须加密并限制访问。Redis 和 Agent WAL 是可重建/短期状态，通常不作为主备份。

## 恢复演练

```bash
docker compose exec -T postgres createdb -U monitored monitored_restore
docker compose exec -T postgres pg_restore -U monitored -d monitored_restore --clean --if-exists < monitored.dump
```

在隔离环境把 Master 指向恢复库，检查 migrations、资产/序列数量、最新告警和审计链。

## 升级

1. 阅读 `docs/roadmap-status.md` 与版本说明。
2. 备份 PostgreSQL 和 secret。
3. `docker compose pull` 或重新 build。
4. `docker compose up -d --build`。
5. Server/Worker 通过 advisory lock 串行执行事务 migration，并记录 `schema_migrations`。
6. 检查 `/healthz`、`schema_migrations`、Worker 日志与 Agent 重连。

不支持自动降级 schema。回退应用前先在备份副本验证旧版本是否兼容新 schema。
