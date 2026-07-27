export type Health = "healthy" | "warning" | "critical" | "unknown";
export type Connection = "online" | "degraded" | "offline";

export interface TrendPoint {
  at: string;
  average: number;
  maximum: number;
  powerW: number;
}

export interface Asset {
  id: string;
  name: string;
  kind: "host" | "pool" | "disk" | "gpu" | "nic" | "bmc" | "ups" | "snmp";
  vendor: string;
  model: string;
  health: Health;
  connection: Connection;
  host: string;
  address?: string;
  lastSeenSeconds: number;
  sensors: number;
  unknown: number;
  primaryMetric: string;
  secondaryMetric: string;
  source: string;
  tags: string[];
  capabilities: Array<{
    label: string;
    state: "ready" | "enhanced" | "permission" | "unavailable";
  }>;
}

export interface Alert {
  id: string;
  severity: "critical" | "warning" | "info";
  state: "Firing" | "Pending" | "Resolved" | "Silenced";
  ruleName: string;
  assetName: string;
  summary: string;
  value: string;
  started: string;
  fingerprint: string;
}

export interface Suggestion {
  id: string;
  sensorId: string;
  rawName: string;
  assetName: string;
  sourcePath: string;
  currentValue: string;
  suggestedName: string;
  suggestedCategory: string;
  confidence: number;
  evidence: string[];
  status: "Pending" | "Accepted" | "Rejected";
  context: string[];
}

export interface ActionRequest {
  id: string;
  assetName: string;
  title: string;
  kind: string;
  command: string;
  identity: string;
  timeout: string;
  risk: "low" | "medium" | "high" | "root";
  state: "pending" | "approved" | "running" | "succeeded" | "rejected";
  requestedBy: string;
  requestedAt: string;
  immutableHash: string;
}

export interface DiscoveryRun {
  id: string;
  target: string;
  state: "running" | "succeeded" | "failed";
  started: string;
  progress: number;
  assets: number;
  sensors: number;
  unknown: number;
  collectors: string[];
}

export interface Incident {
  id: string;
  title: string;
  severity: "critical" | "warning" | "info";
  state: "investigating" | "open" | "mitigated" | "resolved";
  opened: string;
  alerts: number;
  assets: number;
  diagnosis: string;
}

export interface AuditRecord {
  id: number;
  actor: string;
  action: string;
  resource: string;
  time: string;
  result: string;
  hash: string;
}

export interface OverviewData {
  healthScore: number;
  assetCount: number;
  onlineAssetCount: number;
  sensorCount: number;
  unknownCount: number;
  firingAlertCount: number;
  pendingActionCount: number;
  ingestRate: number;
  trend: TrendPoint[];
}

const now = Date.now();

export const trend: TrendPoint[] = Array.from({ length: 49 }, (_, index) => {
  const phase = index / 4.2;
  const load = index > 21 && index < 37 ? 1 : 0;
  return {
    at: new Date(now - (48 - index) * 30 * 60_000).toISOString(),
    average: Number((43.5 + Math.sin(phase) * 2.8 + load * 3.2).toFixed(1)),
    maximum: Number((66 + Math.sin(phase + 0.8) * 4.5 + load * 7.5).toFixed(1)),
    powerW: Math.round(212 + Math.sin(phase - 0.4) * 34 + load * 78),
  };
});

export const overviewData: OverviewData = {
  healthScore: 92,
  assetCount: 38,
  onlineAssetCount: 36,
  sensorCount: 12864,
  unknownCount: 23,
  firingAlertCount: 2,
  pendingActionCount: 1,
  ingestRate: 2184,
  trend,
};

export const assets: Asset[] = [
  {
    id: "asset-edge-01",
    name: "edge-01",
    kind: "host",
    vendor: "Supermicro",
    model: "AS-1115CS-TNR",
    health: "warning",
    connection: "online",
    host: "edge-01",
    address: "10.20.0.11",
    lastSeenSeconds: 8,
    sensors: 2841,
    unknown: 11,
    primaryMetric: "63°C VRM",
    secondaryMetric: "318 W",
    source: "Agent + TrueNAS API",
    tags: ["TrueNAS SCALE 25.10", "Rack A-03"],
    capabilities: [
      { label: "hwmon", state: "ready" },
      { label: "SMART", state: "ready" },
      { label: "ZFS", state: "enhanced" },
      { label: "NVML", state: "ready" },
      { label: "IPMI", state: "permission" },
    ],
  },
  {
    id: "asset-sda",
    name: "sda · Exos X20",
    kind: "disk",
    vendor: "Seagate",
    model: "ST20000NM007D",
    health: "critical",
    connection: "online",
    host: "edge-01 / tank / bay 12",
    lastSeenSeconds: 52,
    sensors: 34,
    unknown: 0,
    primaryMetric: "52°C",
    secondaryMetric: "18,421 h",
    source: "SMART",
    tags: ["WWN …CAFE", "Never wake"],
    capabilities: [
      { label: "Identity", state: "ready" },
      { label: "Attributes", state: "ready" },
      { label: "Self-test", state: "enhanced" },
    ],
  },
  {
    id: "asset-a40",
    name: "NVIDIA A40",
    kind: "gpu",
    vendor: "NVIDIA",
    model: "A40 48 GB",
    health: "healthy",
    connection: "online",
    host: "edge-01 / PCI 41:00.0",
    lastSeenSeconds: 9,
    sensors: 167,
    unknown: 2,
    primaryMetric: "69°C",
    secondaryMetric: "178 W · 74%",
    source: "NVML",
    tags: ["Driver 570.86.15", "GPU-a938"],
    capabilities: [
      { label: "NVML", state: "ready" },
      { label: "ECC", state: "ready" },
      { label: "DCGM", state: "unavailable" },
      { label: "MIG", state: "unavailable" },
    ],
  },
  {
    id: "asset-cx6",
    name: "ConnectX-6 Dx",
    kind: "nic",
    vendor: "NVIDIA",
    model: "MCX623106AN",
    health: "healthy",
    connection: "online",
    host: "edge-01 / PCI 81:00.0",
    lastSeenSeconds: 9,
    sensors: 231,
    unknown: 1,
    primaryMetric: "100 GbE",
    secondaryMetric: "2.1 GB/s",
    source: "ethtool + RDMA",
    tags: ["RoCE v2", "FW 22.41.1000"],
    capabilities: [
      { label: "ethtool", state: "ready" },
      { label: "RDMA", state: "ready" },
      { label: "Optics", state: "ready" },
      { label: "MFT", state: "unavailable" },
    ],
  },
  {
    id: "asset-bmc",
    name: "edge-01 BMC",
    kind: "bmc",
    vendor: "Supermicro",
    model: "Redfish 1.16",
    health: "healthy",
    connection: "online",
    host: "edge-01 / management",
    address: "10.20.1.11",
    lastSeenSeconds: 19,
    sensors: 82,
    unknown: 4,
    primaryMetric: "Healthy",
    secondaryMetric: "6,840 rpm",
    source: "Redfish",
    tags: ["TLS verified", "Read only"],
    capabilities: [
      { label: "Thermal", state: "ready" },
      { label: "Power", state: "ready" },
      { label: "Storage", state: "ready" },
      { label: "SEL", state: "enhanced" },
    ],
  },
  {
    id: "asset-ups",
    name: "UPS · Rack A",
    kind: "ups",
    vendor: "APC",
    model: "Smart-UPS SRT",
    health: "warning",
    connection: "degraded",
    host: "nut@10.20.1.30 / ups-a",
    lastSeenSeconds: 31,
    sensors: 41,
    unknown: 0,
    primaryMetric: "42%",
    secondaryMetric: "24 min · 73%",
    source: "NUT",
    tags: ["OL LB", "Rack A"],
    capabilities: [
      { label: "Status", state: "ready" },
      { label: "Battery", state: "ready" },
      { label: "Bypass", state: "permission" },
    ],
  },
  {
    id: "asset-compute-02",
    name: "compute-02",
    kind: "host",
    vendor: "Dell",
    model: "PowerEdge R760xa",
    health: "healthy",
    connection: "online",
    host: "compute-02",
    address: "10.20.0.12",
    lastSeenSeconds: 7,
    sensors: 4162,
    unknown: 3,
    primaryMetric: "51°C max",
    secondaryMetric: "684 W",
    source: "Agent + Redfish",
    tags: ["Ubuntu 24.04", "Rack A-04"],
    capabilities: [
      { label: "hwmon", state: "ready" },
      { label: "NVML", state: "ready" },
      { label: "Redfish", state: "ready" },
      { label: "DCGM", state: "enhanced" },
    ],
  },
  {
    id: "asset-switch",
    name: "spine-sw-01",
    kind: "snmp",
    vendor: "NVIDIA",
    model: "SN3700",
    health: "healthy",
    connection: "online",
    host: "SNMPv3 / 10.20.2.1",
    lastSeenSeconds: 21,
    sensors: 1487,
    unknown: 2,
    primaryMetric: "32 / 32 ports",
    secondaryMetric: "49°C ASIC",
    source: "SNMPv3",
    tags: ["authPriv", "Cumulus Linux"],
    capabilities: [
      { label: "IF-MIB", state: "ready" },
      { label: "ENTITY-MIB", state: "ready" },
      { label: "LLDP-MIB", state: "ready" },
      { label: "Vendor profile", state: "enhanced" },
    ],
  },
];

export const alerts: Alert[] = [
  {
    id: "alert-disk-temp",
    severity: "critical",
    state: "Firing",
    ruleName: "磁盘温度过高",
    assetName: "sda · Exos X20",
    summary: "温度持续高于硬件警告阈值 50°C",
    value: "52°C · 19 分钟",
    started: "19 分钟前",
    fingerprint: "disk-temp:wwn-cafe",
  },
  {
    id: "alert-ups-charge",
    severity: "warning",
    state: "Firing",
    ruleName: "UPS 电量偏低",
    assetName: "UPS · Rack A",
    summary: "电池电量低且 NUT 连接质量降级",
    value: "42% · 24 分钟",
    started: "11 分钟前",
    fingerprint: "ups-charge:ups-a",
  },
  {
    id: "alert-rdma",
    severity: "warning",
    state: "Pending",
    ruleName: "RDMA 丢弃增长",
    assetName: "ConnectX-6 Dx",
    summary: "物理层接收丢弃在 5 分钟窗口增长 288",
    value: "+288 / 5m",
    started: "3 分钟前",
    fingerprint: "rdma-discard:guid-b83f",
  },
];

export const suggestions: Suggestion[] = [
  {
    id: "suggestion-temp7",
    sensorId: "hwmon:nct6798:temp7",
    rawName: "nct6798/temp7",
    assetName: "edge-01",
    sourcePath: "/sys/class/hwmon/hwmon2/temp7_input",
    currentValue: "47.2 Cel",
    suggestedName: "10GbE PHY 温度",
    suggestedCategory: "温度 · Cel",
    confidence: 0.86,
    evidence: [
      "通道值随网络负载变化，与 CPU 负载相关性较低",
      "主板公开布局显示 NCT6798 temp7 邻近板载 X710 区域",
      "硬件阈值和相邻通道的尺度一致",
    ],
    status: "Pending",
    context: ["Supermicro H13SSL-NT", "nct6798", "Intel X710", "temp6 / temp8"],
  },
  {
    id: "suggestion-fan4",
    sensorId: "redfish:bmc:fan4",
    rawName: "FAN4",
    assetName: "edge-01 BMC",
    sourcePath: "/redfish/v1/Chassis/1/Thermal#/Fans/4",
    currentValue: "6,840 rpm",
    suggestedName: "后置系统风扇",
    suggestedCategory: "风扇 · rpm",
    confidence: 0.78,
    evidence: [
      "Redfish PhysicalContext 为 SystemBoard",
      "同机型 inventory 中 Fan4 对应 rear system fan",
      "当前转速与 FAN3/FAN5 同组变化",
    ],
    status: "Pending",
    context: ["Redfish 1.16", "MemberId Fan4", "AS-1115CS-TNR"],
  },
];

export const actionRequests: ActionRequest[] = [
  {
    id: "action-smart-short",
    assetName: "sda · Exos X20",
    title: "运行 SMART 短测",
    kind: "smart-test",
    command: "smartctl -t short /dev/sda",
    identity: "monitored-action",
    timeout: "60 秒",
    risk: "medium",
    state: "pending",
    requestedBy: "ops@monitored.local",
    requestedAt: "4 分钟前",
    immutableHash:
      "sha256:bf9c307f84d5a2f0d53bc81fdc0c92f5a63c7b4ef4309d88ca15b56933d3d888",
  },
  {
    id: "action-smart-read",
    assetName: "edge-01",
    title: "读取盘位 12 SMART 详情",
    kind: "diagnostic",
    command: "smartctl -x -n standby /dev/sda",
    identity: "monitored-readonly",
    timeout: "30 秒",
    risk: "low",
    state: "approved",
    requestedBy: "ops@monitored.local",
    requestedAt: "16 分钟前",
    immutableHash:
      "sha256:0ddb482dd66e4425f227c4724b98f2cbd8c1883c87a60ea34c35f1c83149670e",
  },
];

export const discoveryRuns: DiscoveryRun[] = [
  {
    id: "discovery-001",
    target: "edge-01",
    state: "succeeded",
    started: "12 分钟前",
    progress: 100,
    assets: 16,
    sensors: 2841,
    unknown: 11,
    collectors: ["hwmon", "PCI/USB", "SMART", "ZFS", "NVML", "RDMA"],
  },
  {
    id: "discovery-002",
    target: "10.20.2.1 · SNMPv3",
    state: "succeeded",
    started: "2 小时前",
    progress: 100,
    assets: 38,
    sensors: 1487,
    unknown: 2,
    collectors: ["IF-MIB", "ENTITY-MIB", "ENTITY-SENSOR-MIB", "LLDP-MIB"],
  },
];

export const incidents: Incident[] = [
  {
    id: "INC-2026-071",
    title: "存储节点 edge-01 热风险",
    severity: "critical",
    state: "investigating",
    opened: "18 分钟前",
    alerts: 1,
    assets: 2,
    diagnosis:
      "磁盘 sda 温度异常，邻近盘位及机箱风扇未见同步升高。优先检查盘位 12 风道与硬盘健康；当前证据不足以建议立即下线设备。",
  },
  {
    id: "INC-2026-070",
    title: "Rack A UPS 电量与链路降级",
    severity: "warning",
    state: "open",
    opened: "11 分钟前",
    alerts: 1,
    assets: 1,
    diagnosis:
      "UPS 仍处于市电在线状态，但电池电量低于预期且 NUT 查询出现间歇超时。建议先验证 upsd 链路，再检查最近一次电池自检。",
  },
];

export const auditRecords: AuditRecord[] = [
  {
    id: 30219,
    actor: "ops@monitored.local",
    action: "action.requested",
    resource: "SMART 短测 · sda",
    time: "4 分钟前",
    result: "待审批",
    hash: "bf9c307f…d3d888",
  },
  {
    id: 30218,
    actor: "system",
    action: "alert.firing",
    resource: "disk-temp:wwn-cafe",
    time: "19 分钟前",
    result: "critical",
    hash: "5ae8b71e…114a2d",
  },
  {
    id: 30217,
    actor: "lin@monitored.local",
    action: "suggestion.accepted",
    resource: "nct6798/temp2 → 主板 VRM 温度",
    time: "36 分钟前",
    result: "成功",
    hash: "1ab09f41…73be0f",
  },
  {
    id: 30216,
    actor: "agent/edge-01",
    action: "discovery.completed",
    resource: "edge-01 · 2,841 sensors",
    time: "41 分钟前",
    result: "11 unknown",
    hash: "694caa19…eb3a01",
  },
  {
    id: 30215,
    actor: "system",
    action: "credential.used",
    resource: "truenas-edge-01-ro",
    time: "42 分钟前",
    result: "只读 API",
    hash: "00c64d1a…20c80d",
  },
];

export const notifications = [
  {
    name: "飞书 · 基础设施告警",
    kind: "Feishu",
    route: "critical, warning",
    status: "healthy",
    lastTest: "今天 09:42",
  },
  {
    name: "Telegram · On-call",
    kind: "Telegram",
    route: "critical",
    status: "healthy",
    lastTest: "昨天 18:11",
  },
  {
    name: "邮件 · 运维日报",
    kind: "SMTP",
    route: "digest + recovery",
    status: "healthy",
    lastTest: "今天 08:00",
  },
  {
    name: "自动化 Webhook",
    kind: "Webhook",
    route: "all events",
    status: "degraded",
    lastTest: "重试 2/5",
  },
  {
    name: "Bark · 值班手机",
    kind: "Bark",
    route: "critical",
    status: "healthy",
    lastTest: "昨天 18:11",
  },
];

export const collectorMatrix = [
  {
    label: "Linux Agent",
    state: "可采集",
    detail: "hwmon · PCI/USB · SMART · ZFS · RDMA",
    enhancement: "udev 热插拔",
  },
  {
    label: "TrueNAS SCALE",
    state: "已增强",
    detail: "JSON-RPC 25.10 · Reporting · ZFS",
    enhancement: "只读凭据",
  },
  {
    label: "NVIDIA GPU",
    state: "可采集",
    detail: "NVML 已加载 · 8 张 GPU",
    enhancement: "DCGM 可增强",
  },
  {
    label: "Mellanox / NVIDIA",
    state: "可采集",
    detail: "ethtool · devlink · RDMA counters",
    enhancement: "MFT 未安装",
  },
  {
    label: "Redfish BMC",
    state: "可采集",
    detail: "TLS 已验证 · Thermal / Power / Storage",
    enhancement: "SEL 需额外权限",
  },
  {
    label: "SNMPv3",
    state: "可采集",
    detail: "authPriv · 6 个标准 MIB",
    enhancement: "1 个厂商 profile",
  },
];
