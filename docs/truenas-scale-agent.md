# TrueNAS SCALE Agent

TrueNAS SCALE 支持两条互补采集链路：

1. **本机 Agent（完整硬件链路）**：直接读取只读 sysfs/hwmon，并通过 `smartctl` 采集 ATA、NVMe、SCSI 和 HBA 暴露的 SMART 数据。
2. **TrueNAS JSON-RPC（可选补充）**：读取 `system.info`、`pool.query`、`disk.query` 和 `disk.temperatures`。该链路适合补充 TrueNAS 的 pool/盘位信息，不能替代完整 SMART。

当前 TrueNAS middleware 的 `disk.temperatures` 使用 5 分钟缓存；25.10 起公开 middleware SMART 接口被收缩，因此完整 SMART 以 Agent 本机 `smartctl` 为准。

## 已采集字段

本机 SMART collector 默认每 5 分钟运行一次，并始终使用：

```text
smartctl -a -j -n standby,0
```

这表示休眠盘不会因采集被唤醒。collector 先执行 `smartctl --scan -j`，保留 SAT、SCSI、NVMe、MegaRAID 等扫描得到的 `-d` 设备类型；受限容器内扫描为空时回退到 host sysfs 枚举。

结构化字段包括：

- 型号族、型号、序列号、固件、WWN/LUN、容量、转速、协议和 SMART 支持状态；
- 综合健康、`smartctl` 状态位、通电时间、上电次数；
- 当前/周期最小最大/寿命最小最大温度、NVMe 多温度探头和 SCSI 临界温度；
- NVMe spare、寿命消耗、读写数据量、host commands、busy time、unsafe shutdown、media/error log、过温与 thermal management；
- 所有 ATA SMART attribute 的 raw/normalized/worst/threshold/flags；
- ATA error log 计数、ATA/NVMe self-test 结果；
- SCSI grown defect list 与读写纠错/不可纠错计数。

字段在设备 JSON 中不存在时不会写入伪造的零；设备明确返回的零计数会正常保存。

## TrueNAS 24.10+ Custom App 示例

先构建并发布仓库的 `agent` 镜像。镜像已包含 `smartmontools`，默认以非 root 用户运行。SMART 需要为容器逐个映射目标设备，并以 root 加最小的 `SYS_RAWIO` capability 启动。下面示例故意不使用 `privileged`：

```yaml
services:
  monitored-agent:
    image: registry.example/monitored-agent:0.2.0
    user: "0:0"
    cap_add:
      - SYS_RAWIO
    devices:
      - /dev/sda:/dev/sda
      - /dev/sdb:/dev/sdb
      - /dev/nvme0:/dev/nvme0
      - /dev/nvme0n1:/dev/nvme0n1
    volumes:
      - /sys:/host/sys:ro
      - /etc/machine-id:/host/etc/machine-id:ro
      - /mnt/tank/monitored-agent:/var/lib/monitored-agent
      - /mnt/tank/monitored-pki:/run/monitored-pki:ro
    environment:
      MONITORED_AGENT_PROTOCOL: grpc
      MONITORED_GRPC_TARGET: master.example:9443
      MONITORED_AGENT_NAME: truenas-scale-01
      MONITORED_AGENT_TLS_CERT_FILE: /run/monitored-pki/agent.crt
      MONITORED_AGENT_TLS_KEY_FILE: /run/monitored-pki/agent.key
      MONITORED_AGENT_TLS_CA_FILE: /run/monitored-pki/ca.crt
      MONITORED_AGENT_TLS_SERVER_NAME: master.example
      MONITORED_SYS_ROOT: /host
      MONITORED_DEVICE_ROOT: /dev
      MONITORED_SMART_INTERVAL: 5m
      MONITORED_WAL_PATH: /var/lib/monitored-agent/wal.ndjson
    restart: unless-stopped
```

设备映射到容器内相同的 `/dev` 路径后，`smartctl --scan -j` 才能保留扫描得到的设备类型。SAS HBA/RAID 环境还应映射扫描返回的控制器设备；例如 MegaRAID 可能对同一控制器返回多个 `-d megaraid,N` 条目，Agent 会按扫描结果逐盘采集。若改用 `/host/dev`，普通 block 设备仍可通过 sysfs 回退采集，但 HBA 的 `-d` 类型可能无法自动发现。

如果新增或更换磁盘，需要同步更新 Custom App 的 `devices` 列表。不要只挂载 `/dev` 后假设容器拥有设备 cgroup 权限，也不要为省事默认开启 `privileged`。

## 可选 TrueNAS API 补充

配置以下变量后，Agent 会额外注册 `truenas-scale-api` collector：

```dotenv
MONITORED_TRUENAS_ENDPOINT=wss://truenas.example/api/current
MONITORED_TRUENAS_TOKEN=<read-only-api-key>
```

API key 只应授予 system/pool/disk/reporting 所需的只读权限。生产环境使用 `wss://` 并保持证书校验；不要把管理员 API key 写入 Compose 明文，优先使用权限受限的 secret 注入。

API 返回的 host 会按 Agent 的 machine-id 与本机 collector 合并，不会重复创建一台 TrueNAS 主机。`disk.temperatures` 返回的临界温度会保存在传感器 metadata 中；方法不可用时回退到 `disk.query.temperature`。

## 现场验证

进入 Agent 容器后检查：

```bash
smartctl --scan -j
smartctl -a -j -n standby,0 /dev/sda
ls -l /host/sys/class/hwmon /host/sys/class/thermal
```

Agent 日志应出现 `smart-nvme` 和 `truenas-scale-api`（若启用），Master `/api/v1/agents` 应显示对应 capability。首次采集后确认磁盘资产包含温度、health、power-on、ATA/NVMe/SCSI 指标，并检查休眠盘没有被唤醒。
