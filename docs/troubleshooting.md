# 故障排查

## Web 显示 Demo

确认构建参数 `NEXT_PUBLIC_MONITORED_MODE=live`。默认模式由构建固定；只有显式设置 `NEXT_PUBLIC_ALLOW_RUNTIME_SWITCH=true` 时，localStorage 的 `monitored-runtime-mode` 才会覆盖初始模式。

## Live 显示 Master 未连接

检查 `NEXT_PUBLIC_API_BASE`、`MONITORED_PUBLIC_URL`、浏览器 Origin、HTTPS mixed content 和登录 Cookie。直接访问 `/healthz` 与 `/api/v1/auth/me`。

## Agent 无法注册

- 确认 Master 9443 可达且 HTTP/2 未被代理降级。
- 证书 SAN/CN 必须匹配 `MONITORED_AGENT_NAME`。
- 检查 CA、ServerName 和证书有效期。
- 不要用 insecure 配置跨主机排障；可用 `openssl s_client` 验证证书链。

Agent 连接失败时检查 WAL 文件权限和 `/api/v1/agents` 的最后 gap。

## Collector unavailable

这是能力状态，不会写零值。检查 Agent 日志和工具：

```bash
command -v smartctl zpool nvidia-smi ipmitool mget_temp
ls -l /sys/class/hwmon /sys/class/infiniband
```

容器 Agent 需要只读 `/sys`/machine-id 挂载。SMART/IPMI 等通常还需要设备节点或额外权限；不要为了方便授予整个 privileged，按 collector 最小化授权。

TrueNAS SCALE 若只看到 sysfs 温度而没有 SMART，检查镜像内是否有
`smartctl`、`MONITORED_DEVICE_ROOT` 是否指向映射目录，以及 Custom App
是否逐个映射了磁盘/控制器设备。参见
[TrueNAS SCALE Agent](truenas-scale-agent.md)。

## Migration 或 Worker 失败

```bash
docker compose logs server worker postgres
docker compose exec postgres psql -U monitored -d monitored -c \
  "select * from schema_migrations order by applied_at"
```

Migrations 使用 advisory lock 与事务。不要手工标记未完成 migration；从备份恢复后重新启动。

## 通知重试

查询 `notification_deliveries` 的 status/attempt/last_error。凭据解封失败通常是 Master key 变化或 associated-data 对不上；恢复原 key 后重试，不能从密文推回丢失 key。
