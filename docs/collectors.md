# Collector 与 Adapter 矩阵

所有 collector 先做能力探测。字段不支持、权限不足或工具缺失时标记 unavailable/degraded，不写入伪造零值。

| 范围 | 实现 | 默认周期 | 增强/依赖 |
| --- | --- | ---: | --- |
| Linux hwmon | 全部 `*_input` 原始通道、标签、阈值、UCUM 归一化 | 10s | sysfs |
| Linux inventory | PCI、USB、block、net、thermal、power_supply、EDAC、DRM、RDMA counters/hw_counters | 15m/30s | sysfs；`devlink -j dev info` 可选增强 |
| SMART/NVMe/SCSI | 身份、多温度、寿命、磨损、错误、self-test、全部 ATA 属性与 NVMe/SCSI 健康计数 | 5m | `smartctl --scan -j` + `smartctl -a -j -n standby,0`；保留 HBA `-d` 类型，默认不唤醒磁盘 |
| ZFS | pool/vdev 拓扑、健康、读写/校验错误、scrub/resilver 文本状态、dataset 配额、snapshot、ARC/L2ARC | 30s | `zpool`、`zfs`、`/proc/spl/kstat/zfs/arcstats`；字段随平台能力降级 |
| FreeBSD | CPU 温度、硬件型号基线 | 30s | `sysctl`；smartctl/IPMI 按工具能力增强 |
| NVIDIA | 直接动态 NVML：温度、风扇、功率/限制、能量、利用率、显存、时钟、PCIe 吞吐、ECC、retired pages、节流、驱动/VBIOS、MIG 状态 | 10s | CGO Agent；无 NVML 时 `nvidia-smi` fallback；检测 DCGM |
| Mellanox/RDMA | PCI/NIC、通用 net stats、InfiniBand/RDMA counters | 30s | MFT `mget_temp` 只读增强；MST 初始化必须审批 |
| IPMI local | SDR 传感器 CSV | 30s | `ipmitool sensor list -c` |
| Redfish | ServiceRoot 超媒体发现 Chassis/Thermal/Power/EnvironmentMetrics | 30s | HTTPS，默认验证 TLS |
| TrueNAS SCALE | `system.info`、`pool.query`、`disk.query`、`disk.temperatures` JSON-RPC WebSocket | 5m | 可选 Agent collector；完整 SMART 走本机 `smartctl`，CORE 使用明确 legacy/Agent 降级 |
| NUT | LIST UPS / LIST VAR，电量、负载、运行时间、电压、频率、温度与状态 | 30s | upsd |
| SNMP | v2c、v3 authPriv、scalar OID、enum/scale/add/multiply profile 映射 | 30s | YAML profile；不执行表达式 |
| HTTP/TCP | 可达性、状态和延迟 | 30s | 只做服务基线，不包含代理、日志、APM 或追踪 |
| systemd | 明确配置的 unit active/sub 状态 | 30s | Host Agent；设置 `MONITORED_SYSTEMD_UNITS` |

SNMP 示例在 `profiles/snmp/example-ups.yaml`，schema 在 `profiles/snmp/schema.json`。profile 使用严格 YAML 字段校验，OID 必须为数字路径，transform 只允许 multiply/add。

Worker 会直接轮询未绑定 Agent 的 TrueNAS、Redfish、IPMI、NUT、SNMP、SSH、HTTP/TCP connection；失败状态和 capability 会写回连接记录，目标不可用的服务样本仍按 error 质量入库。字段与凭据格式见 [连接配置](connections.md)。

网络扫描通过 `/discovery/scans` 下发给指定 Agent。必须提供 CIDR、协议；SNMP/IPMI 必须显式给出端口。默认超过 256 地址要求 `confirmLarge=true`，最大 65,536 地址，限速且不猜测凭据。当前扫描只做端口可达性候选发现，发现后仍需 Admin 创建带凭据的 connection。

TrueNAS SCALE 的最小权限设备映射与环境变量见
[TrueNAS SCALE Agent](truenas-scale-agent.md)。
