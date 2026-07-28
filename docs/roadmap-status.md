# Roadmap 交付状态

本文件区分“接口/安全闭环已交付”和“所有厂商/固件深度都已验证”。硬件生态无法仅靠通用 fixture 宣称全覆盖。

## 已完成

- Compose、自部署、显式 Demo/Live、认证/RBAC、资产/传感器/指标、REST/SSE/gRPC。
- Agent mTLS 运行时、注册/能力/快照/样本/心跳/缺口/任务与 bounded WAL。
- Linux sysfs/hwmon/PCI/USB/EDAC/DRM/devlink、scan-aware ATA/NVMe/SCSI SMART、多温度、ZFS pool/vdev/dataset/snapshot/ARC、FreeBSD 基线。
- TrueNAS SCALE 可选 Agent JSON-RPC system/pool/disk/temperature collector、Redfish、NUT、SNMP v2c/v3 与 profile 框架。
- 直接动态 NVML + fallback、DCGM 能力检测；Mellanox/RDMA sysfs 与 MFT 温度增强。
- 确定性规则、维护窗口、告警转换、Incident 关联、通知持久队列与五种传输。
- OpenAI-compatible/Ollama 待审核标注、完整遥测证据包诊断、手动/按规则显式自动诊断和 token/cooldown 边界。
- 无 Shell 动作验证、OS 身份执行、不可变哈希、Admin 审批、root TOTP 再认证、一次性任务令牌和审计。
- HTTP/TCP 与显式 systemd 基础服务监控。
- 设备专页、连接、规则、用户、Live 真实空态与 Demo 明示。
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
