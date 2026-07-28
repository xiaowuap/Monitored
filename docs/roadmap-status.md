# Roadmap 交付状态

本文件区分“接口/安全闭环已交付”和“所有厂商/固件深度都已验证”。硬件生态无法仅靠通用 fixture 宣称全覆盖。

## 2026-07-29 原始计划复核

这次复核确认此前“已完成”的表述偏乐观。以下能力已有接口或基线，但尚未达到原始计划的
完整验收标准，不能以 Demo 页面或通用 JSON 列表代替：

- Live WebUI 除总览、Agent 配对和 Agent 更新外，多数资源仍使用通用 JSON 行；资产拓扑、
  未知传感器审核、TrueNAS/GPU/RDMA/BMC/UPS/SNMP 专页及规则/通知/动作创建表单还需要接入
  真实 API 的专用交互。
- SSE 目前覆盖 HTTP 进程内的部分事件。gRPC 摄取/任务完成、Worker 告警与通知事件尚未通过
  Redis 或数据库 outbox 汇聚，因此还不是计划中的统一实时事件面。
- 指标表具备分区父表和 1m/1h 幂等聚合，但仍主要写入 default partition，清理使用行级
  `DELETE`；每日分区预创建、watermark 管理和整分区过期删除尚未完成。
- 各硬件 Collector 是 capability baseline，不等于原计划列出的所有深度字段与固件组合已经
  实机验收；尤其 TrueNAS 版本矩阵、完整 NVML/DCGM、Mellanox 光模块/固件计数和 FreeBSD
  深层工具输出仍需扩展 fixture 与现场认证。
- 仓库提供 load generator 和 smoke test，但尚无可重复报告证明 50 节点、20,000 传感器、
  2,000 samples/s 和 24 小时图表 p95 小于 2 秒全部同时达标。

后续发布判定应以本节为准；下面“已完成”指代码闭环或安全基线已经存在，不代表上述缺口
已经关闭。

## 已完成

- Compose、自部署、显式 Demo/Live、认证/RBAC、资产/传感器/指标、REST/SSE/gRPC。
- Agent mTLS 运行时、一次性配对、本地密钥生成、自动换钥续签、注册/能力/快照/样本/心跳/缺口/任务与 bounded WAL。
- Agent 自主更新：Master 持久包缓存、目标 OS/架构匹配、短期签名下载、SHA-256/版本验证、二进制原子替换与 Docker digest pull；Agent 本地策略默认禁用。
- Linux sysfs/hwmon/PCI/USB/EDAC/DRM/devlink、scan-aware ATA/NVMe/SCSI SMART、多温度、ZFS pool/vdev/dataset/snapshot/ARC、FreeBSD 基线。
- TrueNAS SCALE 可选 Agent JSON-RPC system/pool/disk/temperature collector、Redfish、NUT、SNMP v2c/v3 与 profile 框架。
- 直接动态 NVML + fallback、DCGM 能力检测；Mellanox/RDMA sysfs 与 MFT 温度增强。
- 确定性规则、维护窗口、告警转换、Incident 关联、通知持久队列与五种传输。
- OpenAI-compatible/Ollama 待审核标注、完整遥测证据包诊断、手动/按规则显式自动诊断和 token/cooldown 边界。
- 无 Shell 动作验证、OS 身份执行、不可变哈希、Admin 审批、root TOTP 再认证、一次性任务令牌和审计。
- HTTP/TCP 与显式 systemd 基础服务监控。
- 设备专页、连接、规则、用户、Live 真实空态与 Demo 明示。
- 顶部通知中心可打开，并可从活动告警/待审批事项直接跳转。
- 单元/fixture/gRPC/Compose smoke/负载驱动与完整操作文档。

## 作为 capability baseline 交付

- TrueNAS API 当前覆盖 system/pool/disk/temperature，完整 SMART 由本机 Agent 提供；vdev/scrub/resilver/ARC 的不同版本 fixture 仍需随现场版本扩展。
- FreeBSD 覆盖 sysctl；devstat/camcontrol 的深层字段依赖现场工具。
- NVIDIA 覆盖核心 NVML；Xid、retired pages、完整 MIG/NVLink/NvSwitch 深度依赖驱动/DCGM 版本。
- Mellanox 覆盖通用 NIC/RDMA counters 与 MFT 温度；devlink/ethtool netlink、光模块和固件特定计数需按硬件补 fixture。
- SNMP 交付标准 MIB/profile 框架与 UPS 示例，不承诺厂商私有 MIB 完整覆盖。
- CORE 明确走 legacy API/SSH/FreeBSD Agent 降级，不假装拥有 SCALE 同等能力。

## 设备 GA 之后

AMD ROCm、Intel Level Zero 深度 GPU adapter。专用反向代理、完整日志检索、APM、分布式追踪、Windows Agent、多租户、多 Master 和 Kubernetes 不在本里程碑。

## 发布判定

代码和 fixture 通过不等于某个硬件组合已认证。稳定版本发布前，必须在目标 TrueNAS、GPU 驱动、ConnectX 固件、BMC、UPS 和网络设备上执行 `docs/testing.md` 的现场矩阵，并记录 unavailable 字段、权限与性能结果。
