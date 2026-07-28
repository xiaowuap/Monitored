"use client";

import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Bell,
  Bot,
  Boxes,
  Cable,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Circle,
  ClipboardCheck,
  Clock,
  Cpu,
  Database,
  Download,
  Eye,
  Fan,
  FileText,
  Gauge,
  HardDrive,
  Info,
  Languages,
  LayoutDashboard,
  LockKeyhole,
  Mail,
  Menu,
  MessageCircle,
  MoreHorizontal,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  Play,
  Plus,
  Power,
  Radio,
  RefreshCw,
  ScanSearch,
  ScrollText,
  Search,
  Send,
  Server,
  Settings,
  ShieldCheck,
  Siren,
  SlidersHorizontal,
  Smartphone,
  Sparkles,
  Terminal,
  Thermometer,
  Webhook,
  X,
  Zap,
} from "lucide-react";
import dynamic from "next/dynamic";
import { useEffect, useMemo, useState } from "react";
import { I18nextProvider, useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  actionRequests as initialActions,
  alerts,
  assets,
  auditRecords,
  collectorMatrix,
  discoveryRuns as initialDiscoveryRuns,
  incidents,
  notifications,
  overviewData,
  suggestions as initialSuggestions,
  type ActionRequest,
  type Asset,
  type DiscoveryRun,
  type Health,
  type OverviewData,
  type Suggestion,
} from "./monitored-data";

const ReactECharts = dynamic(() => import("echarts-for-react"), {
  ssr: false,
});

type Page =
  | "overview"
  | "assets"
  | "storage"
  | "gpu"
  | "network"
  | "bmc"
  | "ups"
  | "snmp"
  | "discovery"
  | "connections"
  | "rules"
  | "alerts"
  | "incidents"
  | "diagnostics"
  | "actions"
  | "notifications"
  | "audit"
  | "users"
  | "settings";

type Modal =
  | { type: "discovery" }
  | { type: "asset"; asset: Asset }
  | { type: "suggestion"; suggestion: Suggestion }
  | { type: "action"; action: ActionRequest }
  | { type: "command" }
  | null;

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 20_000,
      refetchInterval: 30_000,
      retry: 1,
    },
  },
});

const apiBase = process.env.NEXT_PUBLIC_API_BASE?.replace(/\/$/, "") ?? "";
type RuntimeMode = "demo" | "live";
const configuredMode: RuntimeMode =
  process.env.NEXT_PUBLIC_MONITORED_MODE === "live" ? "live" : "demo";
const allowRuntimeSwitch =
  process.env.NEXT_PUBLIC_ALLOW_RUNTIME_SWITCH === "true";

async function getOverview(mode: RuntimeMode): Promise<OverviewData> {
  if (mode === "demo") {
    await new Promise((resolve) => setTimeout(resolve, 280));
    return overviewData;
  }
  if (!apiBase) {
    throw new Error("Live Mode requires NEXT_PUBLIC_API_BASE");
  }
  const response = await fetch(`${apiBase}/overview`, {
    credentials: "include",
  });
  if (!response.ok) throw new Error("overview unavailable");
  const value = (await response.json()) as {
    healthScore: number;
    assetCount: number;
    onlineAssetCount: number;
    sensorCount: number;
    unknownCount: number;
    firingAlertCount: number;
    pendingActionCount: number;
    ingestRate: number;
    temperatureTrend?: OverviewData["trend"];
  };
  return {
    healthScore: value.healthScore,
    assetCount: value.assetCount,
    onlineAssetCount: value.onlineAssetCount,
    sensorCount: value.sensorCount,
    unknownCount: value.unknownCount,
    firingAlertCount: value.firingAlertCount,
    pendingActionCount: value.pendingActionCount,
    ingestRate: value.ingestRate,
    trend: value.temperatureTrend ?? [],
  };
}

async function postAPI(path: string, body: unknown, mode: RuntimeMode) {
  if (mode === "demo") return;
  if (!apiBase) throw new Error("Live Mode requires NEXT_PUBLIC_API_BASE");
  const response = await fetch(`${apiBase}${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(`Request failed with ${response.status}`);
}

function MonitoredShell() {
  const { t } = useTranslation();
  const [page, setPage] = useState<Page>("overview");
  const [runtimeMode, setRuntimeMode] = useState<RuntimeMode>(() => {
    if (allowRuntimeSwitch && typeof window !== "undefined") {
      const saved = window.localStorage.getItem("monitored-runtime-mode");
      if (saved === "demo" || saved === "live") return saved;
    }
    return configuredMode;
  });
  const [sidebarCompact, setSidebarCompact] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [modal, setModal] = useState<Modal>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [suggestionRows, setSuggestionRows] =
    useState<Suggestion[]>(initialSuggestions);
  const [actionRows, setActionRows] =
    useState<ActionRequest[]>(initialActions);
  const [discoveryRows, setDiscoveryRows] =
    useState<DiscoveryRun[]>(initialDiscoveryRuns);
  const overviewQuery = useQuery({
    queryKey: ["overview", runtimeMode],
    queryFn: () => getOverview(runtimeMode),
  });
  const disconnectedOverview: OverviewData = {
    healthScore: 0,
    assetCount: 0,
    onlineAssetCount: 0,
    sensorCount: 0,
    unknownCount: 0,
    firingAlertCount: 0,
    pendingActionCount: 0,
    ingestRate: 0,
    trend: [],
  };
  const overview =
    overviewQuery.data ??
    (runtimeMode === "demo" ? overviewData : disconnectedOverview);

  useEffect(() => {
    const saved = window.localStorage.getItem("monitored-language");
    if (saved === "en" || saved === "zh") void i18n.changeLanguage(saved);
  }, []);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), 3200);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const navigate = (target: Page) => {
    setPage(target);
    setMobileNavOpen(false);
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  const toggleLanguage = () => {
    const next = i18n.language.startsWith("zh") ? "en" : "zh";
    void i18n.changeLanguage(next);
    window.localStorage.setItem("monitored-language", next);
  };

  const changeRuntimeMode = (mode: RuntimeMode) => {
    if (!allowRuntimeSwitch) return;
    setRuntimeMode(mode);
    window.localStorage.setItem("monitored-runtime-mode", mode);
    void queryClient.invalidateQueries({ queryKey: ["overview"] });
    setToast(
      mode === "demo"
        ? "已进入 Demo Mode；所有数据和操作均为本地演示"
        : "已进入 Live Mode；数据仅来自自部署 Master",
    );
  };

  const reviewSuggestion = async (
    suggestion: Suggestion,
    decision: "Accepted" | "Rejected",
  ) => {
    setSuggestionRows((rows) =>
      rows.map((row) =>
        row.id === suggestion.id ? { ...row, status: decision } : row,
      ),
    );
    setModal(null);
    setToast(
      decision === "Accepted"
        ? `已应用展示别名「${suggestion.suggestedName}」，原始身份保持不变`
        : `已拒绝建议，${suggestion.rawName} 将保持原始名称`,
    );
    try {
      await postAPI(
        `/llm/suggestions/${suggestion.id}/decision`,
        { decision },
        runtimeMode,
      );
    } catch {
      setToast("本地演示已更新；连接 Master 后会写入审计链");
    }
  };

  const reviewAction = async (
    action: ActionRequest,
    decision: "approve" | "reject",
  ) => {
    const nextState = decision === "approve" ? "approved" : "rejected";
    setActionRows((rows) =>
      rows.map((row) =>
        row.id === action.id ? { ...row, state: nextState } : row,
      ),
    );
    setModal(null);
    setToast(
      decision === "approve"
        ? "动作已绑定不可变哈希并签发一次性执行令牌"
        : "动作请求已拒绝并写入审计",
    );
    try {
      await postAPI(
        `/actions/${action.id}/decision`,
        { decision },
        runtimeMode,
      );
    } catch {
      setToast("本地演示已更新；连接 Master 后会进入真实审批队列");
    }
  };

  const startDiscovery = async (target: string, collectors: string[]) => {
    const run: DiscoveryRun = {
      id: `discovery-${Date.now()}`,
      target,
      state: "running",
      started: "刚刚",
      progress: 12,
      assets: 0,
      sensors: 0,
      unknown: 0,
      collectors,
    };
    setDiscoveryRows((rows) => [run, ...rows]);
    setModal(null);
    setPage("discovery");
    setToast(`发现任务已下发到 ${target}`);
    window.setTimeout(() => {
      setDiscoveryRows((rows) =>
        rows.map((row) =>
          row.id === run.id
            ? {
                ...row,
                state: "succeeded",
                progress: 100,
                assets: 16,
                sensors: 2841,
                unknown: 11,
              }
            : row,
        ),
      );
    }, 2400);
    try {
      await postAPI(
        "/discovery/runs",
        { agentId: target, kind: "full" },
        runtimeMode,
      );
    } catch {
      setToast("Master 未连接；Live Mode 没有写入任何演示数据");
    }
  };

  const pageTitle = {
    overview: t("overview"),
    assets: t("assets"),
    storage: "TrueNAS / 存储",
    gpu: "GPU",
    network: "Mellanox / RDMA",
    bmc: "BMC",
    ups: "UPS",
    snmp: "SNMP",
    discovery: t("discovery"),
    connections: "连接",
    rules: "规则",
    alerts: t("alerts"),
    incidents: t("incidents"),
    diagnostics: t("diagnostics"),
    actions: t("actions"),
    notifications: t("notifications"),
    audit: t("audit"),
    users: "用户与 RBAC",
    settings: t("settings"),
  }[page];

  return (
    <div
      className={`app-shell ${sidebarCompact ? "sidebar-compact" : ""}`}
    >
      <Sidebar
        page={page}
        compact={sidebarCompact}
        mobileOpen={mobileNavOpen}
        navigate={navigate}
        closeMobile={() => setMobileNavOpen(false)}
        toggleCompact={() => setSidebarCompact((value) => !value)}
        alertCount={overview.firingAlertCount}
        actionCount={
          runtimeMode === "demo"
            ? actionRows.filter((action) => action.state === "pending").length
            : overview.pendingActionCount
        }
        runtimeMode={runtimeMode}
        connected={runtimeMode === "demo" || overviewQuery.isSuccess}
      />

      <main className="main-area">
        <Topbar
          title={pageTitle}
          onMenu={() => setMobileNavOpen(true)}
          onCommand={() => setModal({ type: "command" })}
          onLanguage={toggleLanguage}
          onDiscover={() => setModal({ type: "discovery" })}
          languageLabel={t("language")}
          runtimeMode={runtimeMode}
          canSwitchRuntime={allowRuntimeSwitch}
          onRuntimeMode={changeRuntimeMode}
        />

        <div className="page-canvas">
          {runtimeMode === "demo" && (
            <div className="demo-mode-banner" role="status">
              <Sparkles size={16} />
              <strong>DEMO MODE · 演示模式</strong>
              <span>确定性样例数据；不会连接 Master，也不会执行真实操作。</span>
            </div>
          )}
          {runtimeMode === "live" && overviewQuery.isError && (
            <div className="live-mode-error" role="alert">
              <Cable size={16} />
              <div>
                <strong>Master 未连接</strong>
                <span>
                  Live Mode 不会回退到演示数据。请检查 API 地址、认证与网络。
                </span>
              </div>
              <button onClick={() => void overviewQuery.refetch()}>
                <RefreshCw size={14} /> 重试
              </button>
            </div>
          )}
          {runtimeMode === "demo" && page === "overview" && (
            <OverviewPage
              overview={overview}
              onNavigate={navigate}
              onDiscovery={() => setModal({ type: "discovery" })}
              onAsset={(asset) => setModal({ type: "asset", asset })}
              onSuggestion={(suggestion) =>
                setModal({ type: "suggestion", suggestion })
              }
              suggestions={suggestionRows}
            />
          )}
          {runtimeMode === "demo" && page === "assets" && (
            <AssetsPage
              onAsset={(asset) => setModal({ type: "asset", asset })}
            />
          )}
          {runtimeMode === "demo" && page === "storage" && (
            <DeviceWorkspacePage category="storage" onAsset={(asset) => setModal({ type: "asset", asset })} />
          )}
          {runtimeMode === "demo" && page === "gpu" && (
            <DeviceWorkspacePage category="gpu" onAsset={(asset) => setModal({ type: "asset", asset })} />
          )}
          {runtimeMode === "demo" && page === "network" && (
            <DeviceWorkspacePage category="network" onAsset={(asset) => setModal({ type: "asset", asset })} />
          )}
          {runtimeMode === "demo" && page === "bmc" && (
            <DeviceWorkspacePage category="bmc" onAsset={(asset) => setModal({ type: "asset", asset })} />
          )}
          {runtimeMode === "demo" && page === "ups" && (
            <DeviceWorkspacePage category="ups" onAsset={(asset) => setModal({ type: "asset", asset })} />
          )}
          {runtimeMode === "demo" && page === "snmp" && (
            <DeviceWorkspacePage category="snmp" onAsset={(asset) => setModal({ type: "asset", asset })} />
          )}
          {runtimeMode === "demo" && page === "discovery" && (
            <DiscoveryPage
              runs={discoveryRows}
              suggestions={suggestionRows}
              onStart={() => setModal({ type: "discovery" })}
              onSuggestion={(suggestion) =>
                setModal({ type: "suggestion", suggestion })
              }
            />
          )}
          {runtimeMode === "demo" && page === "connections" && (
            <ControlPlanePage kind="connections" onToast={setToast} />
          )}
          {runtimeMode === "demo" && page === "rules" && (
            <ControlPlanePage kind="rules" onToast={setToast} />
          )}
          {runtimeMode === "demo" && page === "alerts" && (
            <AlertsPage
              onToast={setToast}
              onNavigate={navigate}
            />
          )}
          {runtimeMode === "demo" && page === "incidents" && <IncidentsPage />}
          {runtimeMode === "demo" && page === "diagnostics" && (
            <DiagnosticsPage onToast={setToast} />
          )}
          {runtimeMode === "demo" && page === "actions" && (
            <ActionsPage
              actions={actionRows}
              onReview={(action) => setModal({ type: "action", action })}
            />
          )}
          {runtimeMode === "demo" && page === "notifications" && (
            <NotificationsPage onToast={setToast} />
          )}
          {runtimeMode === "demo" && page === "audit" && <AuditPage />}
          {runtimeMode === "demo" && page === "users" && (
            <ControlPlanePage kind="users" onToast={setToast} />
          )}
          {runtimeMode === "demo" && page === "settings" && <SettingsPage onToast={setToast} />}
          {runtimeMode === "live" && page === "overview" && (
            <LiveOverviewPage overview={overview} loading={overviewQuery.isLoading} />
          )}
          {runtimeMode === "live" && page !== "overview" && (
            <LiveResourcePage page={page} />
          )}
        </div>
      </main>

      {modal?.type === "discovery" && (
        <DiscoveryModal
          onClose={() => setModal(null)}
          onStart={startDiscovery}
        />
      )}
      {modal?.type === "asset" && (
        <AssetDrawer asset={modal.asset} onClose={() => setModal(null)} />
      )}
      {modal?.type === "suggestion" && (
        <SuggestionDrawer
          suggestion={
            suggestionRows.find((row) => row.id === modal.suggestion.id) ??
            modal.suggestion
          }
          onClose={() => setModal(null)}
          onReview={reviewSuggestion}
        />
      )}
      {modal?.type === "action" && (
        <ActionModal
          action={
            actionRows.find((row) => row.id === modal.action.id) ?? modal.action
          }
          onClose={() => setModal(null)}
          onReview={reviewAction}
        />
      )}
      {modal?.type === "command" && (
        <CommandPalette
          runtimeMode={runtimeMode}
          onClose={() => setModal(null)}
          onNavigate={(target) => {
            navigate(target);
            setModal(null);
          }}
          onAsset={(asset) => {
            setModal({ type: "asset", asset });
          }}
        />
      )}
      {toast && (
        <div className="toast" role="status">
          <CheckCircle2 size={17} />
          <span>{toast}</span>
          <button aria-label="关闭" onClick={() => setToast(null)}>
            <X size={15} />
          </button>
        </div>
      )}
    </div>
  );
}

export default function MonitoredApp() {
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <MonitoredShell />
      </QueryClientProvider>
    </I18nextProvider>
  );
}

function Sidebar({
  page,
  compact,
  mobileOpen,
  navigate,
  closeMobile,
  toggleCompact,
  alertCount,
  actionCount,
  runtimeMode,
  connected,
}: {
  page: Page;
  compact: boolean;
  mobileOpen: boolean;
  navigate: (page: Page) => void;
  closeMobile: () => void;
  toggleCompact: () => void;
  alertCount: number;
  actionCount: number;
  runtimeMode: RuntimeMode;
  connected: boolean;
}) {
  const { t } = useTranslation();
  const sections: Array<{
    label?: string;
    items: Array<{
      id: Page;
      label: string;
      icon: typeof Activity;
      count?: number;
    }>;
  }> = [
    {
      items: [
        { id: "overview", label: t("overview"), icon: LayoutDashboard },
        { id: "assets", label: t("assets"), icon: Boxes },
        { id: "discovery", label: t("discovery"), icon: ScanSearch },
        { id: "connections", label: "连接", icon: Cable },
      ],
    },
    {
      label: "设备遥测",
      items: [
        { id: "storage", label: "TrueNAS / 存储", icon: Database },
        { id: "gpu", label: "GPU", icon: Cpu },
        { id: "network", label: "Mellanox / RDMA", icon: Network },
        { id: "bmc", label: "BMC", icon: Server },
        { id: "ups", label: "UPS", icon: Power },
        { id: "snmp", label: "SNMP", icon: Radio },
      ],
    },
    {
      label: t("operations"),
      items: [
        {
          id: "alerts",
          label: t("alerts"),
          icon: Bell,
          count: alertCount,
        },
        { id: "incidents", label: t("incidents"), icon: Siren },
        { id: "rules", label: "规则", icon: Gauge },
        { id: "diagnostics", label: t("diagnostics"), icon: Bot },
        {
          id: "actions",
          label: t("actions"),
          icon: ClipboardCheck,
          count: actionCount,
        },
        { id: "notifications", label: t("notifications"), icon: Send },
      ],
    },
    {
      label: t("governance"),
      items: [
        { id: "audit", label: t("audit"), icon: ScrollText },
        { id: "users", label: "用户与 RBAC", icon: ShieldCheck },
        { id: "settings", label: t("settings"), icon: Settings },
      ],
    },
  ];
  return (
    <>
      {mobileOpen && (
        <button
          className="mobile-scrim"
          aria-label="关闭导航"
          onClick={closeMobile}
        />
      )}
      <aside
        className={`sidebar ${mobileOpen ? "mobile-open" : ""}`}
        aria-label="主导航"
      >
        <div className="brand">
          <div className="brand-mark" aria-hidden="true">
            <Activity size={20} strokeWidth={2.4} />
          </div>
          {!compact && (
            <div className="brand-copy">
              <strong>Monitored</strong>
              <span>DEVICE INTELLIGENCE</span>
            </div>
          )}
          <button
            className="mobile-close"
            onClick={closeMobile}
            aria-label="关闭导航"
          >
            <X size={18} />
          </button>
        </div>

        <nav className="nav-sections">
          {sections.map((section, index) => (
            <div className="nav-section" key={section.label ?? index}>
              {section.label && !compact && (
                <div className="nav-label">{section.label}</div>
              )}
              {section.items.map((item) => {
                const Icon = item.icon;
                return (
                  <button
                    key={item.id}
                    className={`nav-item ${page === item.id ? "active" : ""}`}
                    onClick={() => navigate(item.id)}
                    title={compact ? item.label : undefined}
                  >
                    <Icon size={18} />
                    {!compact && <span>{item.label}</span>}
                    {item.count ? (
                      <span className="nav-count">{item.count}</span>
                    ) : null}
                  </button>
                );
              })}
            </div>
          ))}
        </nav>

        <div className="sidebar-foot">
          <div className="master-state">
            <span className={`state-beacon ${connected ? "" : "offline"}`} />
            {!compact && (
              <div>
                <strong>
                  {runtimeMode === "demo"
                    ? "Demo 数据集"
                    : connected
                      ? "Master 正常"
                      : "Master 断开"}
                </strong>
                <span>
                  {runtimeMode === "demo" ? "不连接后端" : "自部署 Live Mode"}
                </span>
              </div>
            )}
          </div>
          <button
            className="compact-toggle"
            onClick={toggleCompact}
            aria-label={compact ? "展开侧边栏" : "收起侧边栏"}
          >
            {compact ? (
              <PanelLeftOpen size={17} />
            ) : (
              <>
                <PanelLeftClose size={17} />
                <span>收起</span>
              </>
            )}
          </button>
        </div>
      </aside>
    </>
  );
}

function Topbar({
  title,
  onMenu,
  onCommand,
  onLanguage,
  onDiscover,
  languageLabel,
  runtimeMode,
  canSwitchRuntime,
  onRuntimeMode,
}: {
  title: string;
  onMenu: () => void;
  onCommand: () => void;
  onLanguage: () => void;
  onDiscover: () => void;
  languageLabel: string;
  runtimeMode: RuntimeMode;
  canSwitchRuntime: boolean;
  onRuntimeMode: (mode: RuntimeMode) => void;
}) {
  const { t } = useTranslation();
  return (
    <header className="topbar">
      <div className="topbar-title">
        <button className="mobile-menu" onClick={onMenu} aria-label="打开导航">
          <Menu size={20} />
        </button>
        <div>
          <span className="breadcrumb">Monitored /</span>
          <strong>{title}</strong>
        </div>
      </div>
      <button className="search-trigger" onClick={onCommand}>
        <Search size={16} />
        <span>{t("search")}</span>
        <kbd>⌘ K</kbd>
      </button>
      <div className="runtime-switch" aria-label="运行模式">
        <button
          className={runtimeMode === "demo" ? "active" : ""}
          disabled={!canSwitchRuntime}
          onClick={() => onRuntimeMode("demo")}
          title={canSwitchRuntime ? "使用内置确定性样例数据" : "运行模式由构建配置固定"}
        >
          Demo
        </button>
        <button
          className={runtimeMode === "live" ? "active" : ""}
          disabled={!canSwitchRuntime}
          onClick={() => onRuntimeMode("live")}
          title={canSwitchRuntime ? "连接自部署 Master" : "运行模式由构建配置固定"}
        >
          Live
        </button>
      </div>
      <div className="topbar-actions">
        <button className="icon-button language-button" onClick={onLanguage}>
          <Languages size={17} />
          <span>{languageLabel}</span>
        </button>
        <button className="icon-button notification-button" aria-label="通知">
          <Bell size={18} />
          <span className="notification-dot" />
        </button>
        <button className="discover-button" onClick={onDiscover}>
          <ScanSearch size={16} />
          <span>{t("discoverNow")}</span>
        </button>
        <button className="avatar-button" aria-label="用户菜单">
          <span>林</span>
          <ChevronDown size={14} />
        </button>
      </div>
    </header>
  );
}

function OverviewPage({
  overview,
  onNavigate,
  onDiscovery,
  onAsset,
  onSuggestion,
  suggestions,
}: {
  overview: OverviewData;
  onNavigate: (page: Page) => void;
  onDiscovery: () => void;
  onAsset: (asset: Asset) => void;
  onSuggestion: (suggestion: Suggestion) => void;
  suggestions: Suggestion[];
}) {
  const { t } = useTranslation();
  const [range, setRange] = useState("24h");
  const pendingSuggestions = suggestions.filter(
    (suggestion) => suggestion.status === "Pending",
  );
  return (
    <div className="page-stack">
      <section className="fleet-intro">
        <div>
          <div className="eyebrow">
            <span className="live-pulse" />
            {t("live")} · 2026-07-28 14:36 CST
          </div>
          <h1>{t("fleetHealth")}</h1>
          <p>{t("fleetSubtitle")}</p>
        </div>
        <div className="intro-actions">
          <button className="button secondary" onClick={onDiscovery}>
            <RefreshCw size={16} />
            {t("discoverNow")}
          </button>
          <button className="button primary" onClick={onDiscovery}>
            <Plus size={16} />
            {t("addDevice")}
          </button>
        </div>
      </section>

      <section className="metrics-row">
        <article className="metric-card health-card">
          <div className="metric-icon mint">
            <Gauge size={19} />
          </div>
          <div className="metric-content">
            <span>{t("healthScore")}</span>
            <strong>{overview.healthScore}</strong>
            <small>
              <span className="positive">↑ 2.4%</span> 较上周
            </small>
          </div>
          <HealthRing value={overview.healthScore} />
        </article>
        <MetricCard
          icon={Server}
          iconTone="blue"
          label={t("assetsOnline")}
          value={`${overview.onlineAssetCount}/${overview.assetCount}`}
          note="2 台设备降级"
          tone="warning"
        />
        <MetricCard
          icon={Radio}
          iconTone="violet"
          label={t("sensors")}
          value={formatNumber(overview.sensorCount)}
          note={`${overview.unknownCount} 个待识别`}
          tone="neutral"
        />
        <MetricCard
          icon={AlertTriangle}
          iconTone="amber"
          label={t("activeAlerts")}
          value={String(overview.firingAlertCount)}
          note="1 个严重 · 1 个警告"
          tone="critical"
        />
        <MetricCard
          icon={Activity}
          iconTone="cyan"
          label={t("ingestRate")}
          value={formatNumber(overview.ingestRate)}
          note="samples/s · p95 82ms"
          tone="positive"
        />
      </section>

      <section className="overview-grid">
        <article className="panel telemetry-panel">
          <PanelHeader
            title={t("telemetry")}
            subtitle="所有设备 · 规范化指标"
            icon={Thermometer}
            action={
              <div className="segmented">
                {["6h", "24h", "7d"].map((item) => (
                  <button
                    key={item}
                    className={range === item ? "active" : ""}
                    onClick={() => setRange(item)}
                  >
                    {item}
                  </button>
                ))}
              </div>
            }
          />
          <TelemetryChart data={overview.trend} range={range} />
          <div className="chart-legend">
            <span>
              <i className="legend-dot average" /> 平均温度
              <strong>46.8°C</strong>
            </span>
            <span>
              <i className="legend-dot maximum" /> 最高温度
              <strong>76.1°C</strong>
            </span>
            <span>
              <i className="legend-dot power" /> 总功耗
              <strong>318 W</strong>
            </span>
          </div>
        </article>

        <article className="panel attention-panel">
          <PanelHeader
            title={t("attention")}
            subtitle="按影响排序"
            icon={AlertTriangle}
            action={
              <button className="text-button" onClick={() => onNavigate("alerts")}>
                {t("viewAll")} <ChevronRight size={14} />
              </button>
            }
          />
          <div className="attention-list">
            {alerts.map((alert) => (
              <button
                key={alert.id}
                className="attention-item"
                onClick={() => onNavigate("alerts")}
              >
                <SeverityIcon severity={alert.severity} />
                <div>
                  <div className="attention-title">
                    <strong>{alert.assetName}</strong>
                    <span>{alert.started}</span>
                  </div>
                  <p>{alert.summary}</p>
                  <small>{alert.value}</small>
                </div>
                <ChevronRight size={15} />
              </button>
            ))}
          </div>
          <div className="attention-foot">
            <div>
              <Clock size={15} />
              <span>下一维护窗口</span>
            </div>
            <strong>周六 02:00</strong>
          </div>
        </article>
      </section>

      <section className="panel inventory-panel">
        <PanelHeader
          title={t("inventory")}
          subtitle="按风险与最近活动排序"
          icon={Boxes}
          action={
            <button className="text-button" onClick={() => onNavigate("assets")}>
              {t("viewAll")} <ArrowUpRight size={14} />
            </button>
          }
        />
        <AssetTable assets={assets.slice(0, 6)} onAsset={onAsset} />
      </section>

      <section className="lower-grid">
        <article className="panel discovery-preview">
          <PanelHeader
            title={t("discoveryCenter")}
            subtitle={`${pendingSuggestions.length} 条 LLM 建议待审核`}
            icon={Sparkles}
            action={
              <button
                className="text-button"
                onClick={() => onNavigate("discovery")}
              >
                {t("viewAll")} <ChevronRight size={14} />
              </button>
            }
          />
          {pendingSuggestions.slice(0, 2).map((suggestion) => (
            <button
              key={suggestion.id}
              className="suggestion-row"
              onClick={() => onSuggestion(suggestion)}
            >
              <div className="sensor-glyph">
                {suggestion.suggestedCategory.includes("风扇") ? (
                  <Fan size={18} />
                ) : (
                  <Thermometer size={18} />
                )}
              </div>
              <div className="suggestion-copy">
                <span>{suggestion.rawName}</span>
                <strong>{suggestion.suggestedName}</strong>
                <small>{suggestion.assetName} · {suggestion.currentValue}</small>
              </div>
              <Confidence value={suggestion.confidence} />
              <ChevronRight size={16} />
            </button>
          ))}
        </article>

        <article className="panel collector-panel">
          <PanelHeader
            title="Collector 状态"
            subtitle="最近 60 秒"
            icon={Cable}
          />
          <div className="collector-list">
            {[
              ["Agent", "2 / 2", "healthy"],
              ["TrueNAS API", "1 / 1", "healthy"],
              ["Redfish · SNMP · NUT", "4 / 5", "warning"],
              ["PostgreSQL", "82 ms p95", "healthy"],
            ].map(([label, value, state]) => (
              <div className="collector-row" key={label}>
                <span className={`state-dot ${state}`} />
                <span>{label}</span>
                <strong>{value}</strong>
              </div>
            ))}
          </div>
          <div className="collector-note">
            <ShieldCheck size={16} />
            <div>
              <strong>基础采集不依赖 Redis / LLM</strong>
              <span>所有核心写入路径正常</span>
            </div>
          </div>
        </article>
      </section>
    </div>
  );
}

function AssetsPage({ onAsset }: { onAsset: (asset: Asset) => void }) {
  const { t } = useTranslation();
  const [kind, setKind] = useState("all");
  const [query, setQuery] = useState("");
  const filtered = assets.filter(
    (asset) =>
      (kind === "all" || asset.kind === kind) &&
      `${asset.name} ${asset.model} ${asset.host}`
        .toLowerCase()
        .includes(query.toLowerCase()),
  );
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="ASSET GRAPH · 38 ASSETS"
        title={t("assets")}
        description="稳定硬件身份驱动的主机、总线、设备与传感器拓扑"
        actions={
          <button className="button primary">
            <Plus size={16} /> {t("addDevice")}
          </button>
        }
      />
      <section className="topology-strip">
        <div className="topology-node host">
          <Server size={20} />
          <div><strong>edge-01</strong><span>Host</span></div>
        </div>
        <ChevronRight size={18} />
        <div className="topology-branches">
          {[
            [Cpu, "PCI / USB", "9"],
            [HardDrive, "Storage", "13"],
            [Network, "Network", "4"],
            [Power, "BMC / Power", "3"],
          ].map(([Icon, label, count]) => {
            const NodeIcon = Icon as typeof Activity;
            return (
              <button key={String(label)}>
                <NodeIcon size={18} />
                <span>{String(label)}</span>
                <strong>{String(count)}</strong>
              </button>
            );
          })}
        </div>
        <div className="topology-summary">
          <span>2,841</span>
          <small>传感器</small>
        </div>
      </section>
      <section className="panel inventory-full">
        <div className="table-toolbar">
          <div className="filter-pills">
            {[
              ["all", "全部"],
              ["host", "主机"],
              ["disk", "存储"],
              ["gpu", "GPU"],
              ["nic", "网络"],
              ["bmc", "BMC"],
              ["ups", "电源"],
            ].map(([id, label]) => (
              <button
                key={id}
                className={kind === id ? "active" : ""}
                onClick={() => setKind(id)}
              >
                {label}
              </button>
            ))}
          </div>
          <label className="table-search">
            <Search size={15} />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="搜索名称、型号或路径"
            />
          </label>
          <button className="icon-button" aria-label="筛选">
            <SlidersHorizontal size={17} />
          </button>
        </div>
        <AssetTable assets={filtered} onAsset={onAsset} />
      </section>
    </div>
  );
}

function DiscoveryPage({
  runs,
  suggestions,
  onStart,
  onSuggestion,
}: {
  runs: DiscoveryRun[];
  suggestions: Suggestion[];
  onStart: () => void;
  onSuggestion: (suggestion: Suggestion) => void;
}) {
  const [tab, setTab] = useState<"unknown" | "runs" | "capabilities">("unknown");
  const pending = suggestions.filter((suggestion) => suggestion.status === "Pending");
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="READ-ONLY DISCOVERY"
        title="自动发现中心"
        description="新设备、未知通道、消失传感器、来源冲突与降级能力统一处理"
        actions={
          <button className="button primary" onClick={onStart}>
            <ScanSearch size={16} /> 新建发现任务
          </button>
        }
      />
      <section className="discovery-stats">
        {[
          [Sparkles, "新发现", "17", "mint"],
          [Bot, "待识别", String(pending.length), "violet"],
          [AlertTriangle, "来源冲突", "3", "amber"],
          [Clock, "即将 stale", "6", "blue"],
        ].map(([Icon, label, value, tone]) => {
          const StatIcon = Icon as typeof Activity;
          return (
            <article key={String(label)}>
              <div className={`metric-icon ${tone}`}><StatIcon size={18} /></div>
              <div><span>{String(label)}</span><strong>{String(value)}</strong></div>
            </article>
          );
        })}
      </section>
      <section className="panel discovery-workspace">
        <div className="workspace-tabs">
          <button
            className={tab === "unknown" ? "active" : ""}
            onClick={() => setTab("unknown")}
          >
            未知传感器 <span>{pending.length}</span>
          </button>
          <button
            className={tab === "runs" ? "active" : ""}
            onClick={() => setTab("runs")}
          >
            发现任务 <span>{runs.length}</span>
          </button>
          <button
            className={tab === "capabilities" ? "active" : ""}
            onClick={() => setTab("capabilities")}
          >
            Capability matrix
          </button>
        </div>
        {tab === "unknown" && (
          <div className="unknown-grid">
            {suggestions.map((suggestion) => (
              <button
                key={suggestion.id}
                className={`unknown-card ${suggestion.status.toLowerCase()}`}
                onClick={() => onSuggestion(suggestion)}
              >
                <div className="unknown-head">
                  <div className="sensor-glyph">
                    {suggestion.suggestedCategory.includes("风扇") ? (
                      <Fan size={19} />
                    ) : (
                      <Thermometer size={19} />
                    )}
                  </div>
                  <div>
                    <span>{suggestion.assetName}</span>
                    <strong>{suggestion.rawName}</strong>
                  </div>
                  <StatusPill status={suggestion.status} />
                </div>
                <code>{suggestion.sourcePath}</code>
                <div className="suggestion-proposal">
                  <Sparkles size={15} />
                  <span>建议：{suggestion.suggestedName}</span>
                  <Confidence value={suggestion.confidence} />
                </div>
                <div className="context-tags">
                  {suggestion.context.slice(0, 3).map((item) => (
                    <span key={item}>{item}</span>
                  ))}
                </div>
              </button>
            ))}
          </div>
        )}
        {tab === "runs" && (
          <div className="runs-list">
            {runs.map((run) => (
              <article className="run-row" key={run.id}>
                <div className={`run-icon ${run.state}`}>
                  {run.state === "running" ? (
                    <RefreshCw size={18} className="spin" />
                  ) : (
                    <Check size={18} />
                  )}
                </div>
                <div className="run-main">
                  <div><strong>{run.target}</strong><span>{run.started}</span></div>
                  <div className="run-collectors">
                    {run.collectors.map((collector) => (
                      <span key={collector}>{collector}</span>
                    ))}
                  </div>
                  <div className="progress-track">
                    <span style={{ width: `${run.progress}%` }} />
                  </div>
                </div>
                <div className="run-counts">
                  <span><strong>{run.assets}</strong> 资产</span>
                  <span><strong>{formatNumber(run.sensors)}</strong> 传感器</span>
                  <span><strong>{run.unknown}</strong> 未知</span>
                </div>
              </article>
            ))}
          </div>
        )}
        {tab === "capabilities" && (
          <div className="capability-matrix">
            <div className="capability-head">
              <span>接入方式</span><span>状态</span><span>能力</span><span>增强 / 缺口</span>
            </div>
            {collectorMatrix.map((capability) => (
              <div className="capability-row" key={capability.label}>
                <strong>{capability.label}</strong>
                <span className={`capability-state ${
                  capability.state === "已增强" ? "enhanced" : "ready"
                }`}>
                  <Circle size={8} fill="currentColor" /> {capability.state}
                </span>
                <span>{capability.detail}</span>
                <span>{capability.enhancement}</span>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function AlertsPage({
  onToast,
  onNavigate,
}: {
  onToast: (message: string) => void;
  onNavigate: (page: Page) => void;
}) {
  const [state, setState] = useState("active");
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="DETERMINISTIC RULE ENGINE"
        title="告警"
        description="硬件阈值优先；LLM 仅解释、归因和建议，不拥有触发权"
        actions={
          <button className="button primary" onClick={() => onToast("规则编辑器已准备就绪")}>
            <Plus size={16} /> 新建规则
          </button>
        }
      />
      <section className="rule-health">
        <div><span className="state-dot healthy" /><strong>规则引擎正常</strong><small>126 条启用规则</small></div>
        <div><Clock size={16} /><strong>3</strong><small>Pending</small></div>
        <div><AlertTriangle size={16} /><strong>2</strong><small>Firing</small></div>
        <div><ShieldCheck size={16} /><strong>1</strong><small>维护窗口</small></div>
      </section>
      <section className="panel alerts-workspace">
        <div className="workspace-tabs">
          {[
            ["active", "活动告警", 3],
            ["resolved", "已恢复", 18],
            ["rules", "规则", 126],
            ["silences", "静默", 1],
          ].map(([id, label, count]) => (
            <button
              key={String(id)}
              className={state === id ? "active" : ""}
              onClick={() => setState(String(id))}
            >
              {String(label)} <span>{String(count)}</span>
            </button>
          ))}
        </div>
        {state === "active" ? (
          <div className="alert-cards">
            {alerts.map((alert) => (
              <article key={alert.id} className={`alert-card ${alert.severity}`}>
                <SeverityIcon severity={alert.severity} />
                <div className="alert-body">
                  <div className="alert-title">
                    <div>
                      <StatusPill status={alert.state} />
                      <strong>{alert.ruleName}</strong>
                    </div>
                    <span>{alert.started}</span>
                  </div>
                  <h3>{alert.assetName}</h3>
                  <p>{alert.summary}</p>
                  <div className="alert-meta">
                    <code>{alert.fingerprint}</code>
                    <span>{alert.value}</span>
                  </div>
                </div>
                <div className="alert-actions">
                  <button
                    className="button secondary small"
                    onClick={() => onToast(`${alert.assetName} 已静默 1 小时`)}
                  >
                    <Bell size={14} /> 静默
                  </button>
                  <button
                    className="button primary small"
                    onClick={() => onNavigate("diagnostics")}
                  >
                    <Bot size={14} /> 诊断
                  </button>
                  <button className="icon-button"><MoreHorizontal size={17} /></button>
                </div>
              </article>
            ))}
          </div>
        ) : (
          <EmptyState
            icon={ShieldCheck}
            title={`${state === "rules" ? "规则清单" : "该视图"}已就绪`}
            copy="演示数据聚焦当前活动告警；连接 Master 后会显示完整历史。"
          />
        )}
      </section>
    </div>
  );
}

function IncidentsPage() {
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="CORRELATED EVIDENCE"
        title="Incident"
        description="把相关告警、资产拓扑、近期变化与诊断证据收敛为可处理事件"
      />
      <div className="incident-layout">
        <section className="incident-list">
          {incidents.map((incident, index) => (
            <article className={`incident-card ${index === 0 ? "selected" : ""}`} key={incident.id}>
              <div className="incident-card-head">
                <SeverityIcon severity={incident.severity} />
                <div><span>{incident.id}</span><strong>{incident.title}</strong></div>
                <StatusPill status={incident.state} />
              </div>
              <p>{incident.diagnosis}</p>
              <div className="incident-card-foot">
                <span><Bell size={14} /> {incident.alerts} 告警</span>
                <span><Boxes size={14} /> {incident.assets} 资产</span>
                <span><Clock size={14} /> {incident.opened}</span>
              </div>
            </article>
          ))}
        </section>
        <section className="panel incident-detail">
          <div className="incident-detail-head">
            <div>
              <span>INC-2026-071 · 调查中</span>
              <h2>存储节点 edge-01 热风险</h2>
            </div>
            <button className="button secondary small">标记已缓解</button>
          </div>
          <div className="evidence-summary">
            <Bot size={19} />
            <div>
              <span>AI 证据摘要 · 置信度 82%</span>
              <p>热异常集中在单个盘位，主板 VRM、GPU 与机箱进风温度稳定。SMART 未报告介质错误，但该盘已持续高温 19 分钟。</p>
            </div>
          </div>
          <h3 className="section-mini-title">事件时间线</h3>
          <div className="timeline">
            {[
              ["14:17", "告警进入 Pending", "sda 温度达到 50°C"],
              ["14:22", "告警开始 Firing", "持续时间满足 5 分钟条件"],
              ["14:24", "自动收集证据", "SMART、ZFS、BMC 和相邻传感器"],
              ["14:28", "生成诊断建议", "未执行任何命令"],
              ["14:32", "动作待审批", "SMART 短测"],
            ].map(([time, title, detail], index) => (
              <div className="timeline-row" key={time}>
                <span>{time}</span>
                <i className={index === 4 ? "active" : ""} />
                <div><strong>{title}</strong><small>{detail}</small></div>
              </div>
            ))}
          </div>
          <h3 className="section-mini-title">数据缺口</h3>
          <div className="gap-note">
            <Info size={16} />
            <span>缺少盘位 12 的独立进风温度；建议人工检查风道后再决定是否下线硬盘。</span>
          </div>
        </section>
      </div>
    </div>
  );
}

function DiagnosticsPage({ onToast }: { onToast: (message: string) => void }) {
  const [expanded, setExpanded] = useState(true);
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="HUMAN-GATED AI"
        title="智能诊断"
        description="结构化证据进入模型；诊断默认关闭，命令草案永不自动执行"
        actions={
          <button
            className="button primary"
            onClick={() => onToast("已创建只读诊断任务，token 预算 8,000")}
          >
            <Sparkles size={16} /> 新建诊断
          </button>
        }
      />
      <section className="diagnostic-banner">
        <div className="bot-orb"><Bot size={24} /></div>
        <div>
          <span>最近诊断 · INC-2026-071</span>
          <h2>单盘位散热受阻的可能性最高</h2>
          <p>OpenAI-compatible · 2 分钟前 · 3,842 tokens · 只读证据</p>
        </div>
        <Confidence value={0.82} large />
      </section>
      <div className="diagnostic-grid">
        <section className="panel diagnosis-report">
          <PanelHeader
            title="诊断报告"
            subtitle="可验证证据优先"
            icon={FileText}
            action={
              <button className="icon-button" aria-label="下载报告">
                <Download size={17} />
              </button>
            }
          />
          <div className="diagnosis-block">
            <span className="block-number">01</span>
            <div>
              <h3>摘要</h3>
              <p>edge-01 的 sda 温度在负载平稳时从 44°C 上升至 52°C。相邻系统温度与风扇转速无同步异常，问题更可能局限于盘位 12 或硬盘本体。</p>
            </div>
          </div>
          <div className="diagnosis-block">
            <span className="block-number">02</span>
            <div>
              <h3>主要假设</h3>
              {[
                ["盘位 12 风道受阻", "高", "局部温升且机箱整体温度稳定"],
                ["硬盘内部温度传感偏差", "中", "需要 SMART 自检与外部测温验证"],
                ["机箱风扇组异常", "低", "BMC FAN3/FAN4/FAN5 均在正常范围"],
              ].map(([title, confidence, evidence]) => (
                <div className="hypothesis" key={title}>
                  <span className={`confidence-word ${confidence}`}>{confidence}</span>
                  <div><strong>{title}</strong><small>{evidence}</small></div>
                </div>
              ))}
            </div>
          </div>
          <div className="diagnosis-block">
            <span className="block-number">03</span>
            <div>
              <h3>建议检查</h3>
              <ol className="check-list">
                <li>现场确认盘位 12 前后风道无遮挡。</li>
                <li>批准只读 SMART 详情采集，检查温度历史与自检记录。</li>
                <li>若温度超过 55°C 或出现介质错误，迁移负载并安排更换。</li>
              </ol>
            </div>
          </div>
        </section>
        <aside className="diagnostic-side">
          <section className="panel evidence-pack">
            <PanelHeader title="证据包" subtitle="14 项 · 已脱敏" icon={Database} />
            {[
              ["24h 温度趋势", "49 points", "ready"],
              ["SMART / NVMe", "34 fields", "ready"],
              ["ZFS pool status", "tank ONLINE", "ready"],
              ["BMC thermal", "82 sensors", "ready"],
              ["近期设备变更", "0 changes", "ready"],
              ["有限日志上下文", "permission denied", "warning"],
            ].map(([label, value, status]) => (
              <div className="evidence-row" key={label}>
                <span className={`state-dot ${status === "ready" ? "healthy" : "warning"}`} />
                <div><strong>{label}</strong><small>{value}</small></div>
              </div>
            ))}
          </section>
          <section className="panel command-draft">
            <button className="command-draft-head" onClick={() => setExpanded((v) => !v)}>
              <div><Terminal size={17} /><span>命令草案</span></div>
              <ChevronDown size={16} className={expanded ? "rotated" : ""} />
            </button>
            {expanded && (
              <>
                <code>smartctl -x -n standby /dev/sda</code>
                <div className="command-guard">
                  <LockKeyhole size={15} />
                  <span>草案不会执行。创建动作后需管理员审批。</span>
                </div>
                <button className="button secondary full" onClick={() => onToast("已创建待审批的只读动作")}>
                  创建审批请求
                </button>
              </>
            )}
          </section>
        </aside>
      </div>
    </div>
  );
}

function ActionsPage({
  actions,
  onReview,
}: {
  actions: ActionRequest[];
  onReview: (action: ActionRequest) => void;
}) {
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="IMMUTABLE APPROVAL"
        title="动作审批"
        description="目标、命令、身份、环境、超时与风险在批准时冻结并生成一次性令牌"
      />
      <section className="approval-principles">
        <div><LockKeyhole size={17} /><span>命令不可变哈希</span></div>
        <div><ShieldCheck size={17} /><span>管理员权限边界</span></div>
        <div><Clock size={17} /><span>强制超时与输出上限</span></div>
        <div><ScrollText size={17} /><span>追加式审计链</span></div>
      </section>
      <section className="panel actions-panel">
        <div className="workspace-tabs">
          <button className="active">待处理 <span>{actions.filter((a) => a.state === "pending").length}</span></button>
          <button>已批准 <span>{actions.filter((a) => a.state === "approved").length}</span></button>
          <button>执行历史 <span>18</span></button>
        </div>
        <div className="action-list">
          {actions.map((action) => (
            <article className="action-row" key={action.id}>
              <div className={`action-risk ${action.risk}`}>
                <Terminal size={20} />
              </div>
              <div className="action-main">
                <div className="action-title">
                  <div><StatusPill status={action.state} /><strong>{action.title}</strong></div>
                  <span>{action.requestedAt}</span>
                </div>
                <span className="action-asset">{action.assetName} · {action.kind}</span>
                <code>{action.command}</code>
                <div className="action-meta">
                  <span>身份 <strong>{action.identity}</strong></span>
                  <span>超时 <strong>{action.timeout}</strong></span>
                  <span>风险 <strong>{action.risk}</strong></span>
                  <span>申请人 <strong>{action.requestedBy}</strong></span>
                </div>
              </div>
              <div className="action-row-buttons">
                {action.state === "pending" ? (
                  <button className="button primary" onClick={() => onReview(action)}>
                    审核 <ChevronRight size={15} />
                  </button>
                ) : (
                  <button className="button secondary" onClick={() => onReview(action)}>
                    查看 <Eye size={15} />
                  </button>
                )}
              </div>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}

function NotificationsPage({ onToast }: { onToast: (message: string) => void }) {
  const icons: Record<string, typeof Activity> = {
    Feishu: MessageCircle,
    Telegram: Send,
    SMTP: Mail,
    Webhook,
    Bark: Smartphone,
  };
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="ROUTED DELIVERY"
        title="通知"
        description="按严重度路由、聚合、静默、恢复与重试，支持中英文模板"
        actions={
          <button className="button primary"><Plus size={16} /> 新建渠道</button>
        }
      />
      <section className="notification-health">
        <div><strong>5</strong><span>渠道</span></div>
        <div><strong>4</strong><span>健康</span></div>
        <div><strong>37</strong><span>今日送达</span></div>
        <div><strong>98.7%</strong><span>7 日成功率</span></div>
      </section>
      <section className="channel-grid">
        {notifications.map((notification) => {
          const Icon = icons[notification.kind];
          return (
            <article className="panel channel-card" key={notification.name}>
              <div className="channel-head">
                <div className="channel-icon"><Icon size={20} /></div>
                <div><strong>{notification.name}</strong><span>{notification.kind}</span></div>
                <span className={`state-dot ${notification.status === "healthy" ? "healthy" : "warning"}`} />
              </div>
              <div className="channel-routing">
                <span>路由</span><strong>{notification.route}</strong>
              </div>
              <div className="channel-foot">
                <small>{notification.lastTest}</small>
                <button onClick={() => onToast(`${notification.name} 测试消息已入队`)}>
                  <Play size={14} /> 发送测试
                </button>
              </div>
            </article>
          );
        })}
      </section>
    </div>
  );
}

function AuditPage() {
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="TAMPER-EVIDENT APPLICATION LOG"
        title="审计日志"
        description="追加记录与哈希链可检测应用层篡改；不夸大宿主机管理员威胁模型"
        actions={
          <button className="button secondary"><Download size={16} /> 导出</button>
        }
      />
      <section className="audit-chain-status">
        <div className="audit-seal"><ShieldCheck size={23} /></div>
        <div><strong>哈希链验证通过</strong><span>30,219 条记录 · 上次验证 14:35:42</span></div>
        <code>head 41d7c9a1…ea2c</code>
      </section>
      <section className="panel audit-panel">
        <div className="table-toolbar">
          <label className="table-search wide"><Search size={15} /><input placeholder="搜索 actor、动作或资源" /></label>
          <button className="button secondary small"><SlidersHorizontal size={15} /> 筛选</button>
        </div>
        <div className="audit-table">
          <div className="audit-row header">
            <span>ID / 时间</span><span>行为主体</span><span>动作</span><span>资源</span><span>结果</span><span>链哈希</span>
          </div>
          {auditRecords.map((record) => (
            <div className="audit-row" key={record.id}>
              <span><strong>#{record.id}</strong><small>{record.time}</small></span>
              <span>{record.actor}</span>
              <code>{record.action}</code>
              <span>{record.resource}</span>
              <span><StatusPill status={record.result} /></span>
              <code>{record.hash}</code>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

function SettingsPage({ onToast }: { onToast: (message: string) => void }) {
  const [diagnostics, setDiagnostics] = useState(false);
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="PLATFORM CONTROLS"
        title="设置"
        description="采集、保留、安全与智能能力的全局默认值"
      />
      <div className="settings-layout">
        <aside className="settings-nav">
          {["采集与保留", "安全与凭据", "Agent 与证书", "LLM Provider", "用户与 RBAC", "系统状态"].map((item, index) => (
            <button className={index === 0 ? "active" : ""} key={item}>{item}<ChevronRight size={15} /></button>
          ))}
        </aside>
        <section className="panel settings-panel">
          <h2>采集与保留</h2>
          <p>默认策略可被资产或传感器级配置覆盖。</p>
          {[
            ["本地硬件指标", "10 秒", "hwmon、GPU、NIC 性能"],
            ["健康与错误计数", "30 秒", "SMART 状态、ECC、链路错误"],
            ["BMC / SNMP / NUT", "30 秒", "远程设备与电源"],
            ["SMART 完整属性", "1 小时", "不得唤醒休眠磁盘"],
            ["资产重新发现", "15 分钟", "同时响应 udev 事件"],
          ].map(([label, value, detail]) => (
            <div className="setting-row" key={label}>
              <div><strong>{label}</strong><span>{detail}</span></div>
              <button>{value}<ChevronDown size={14} /></button>
            </div>
          ))}
          <div className="setting-divider" />
          <h3>智能诊断</h3>
          <div className="toggle-row">
            <div><strong>自动诊断</strong><span>按规则、严重度、频率和 token 预算运行</span></div>
            <button
              className={`switch ${diagnostics ? "on" : ""}`}
              onClick={() => setDiagnostics((value) => !value)}
              aria-pressed={diagnostics}
            ><span /></button>
          </div>
          <div className="settings-save">
            <span>所有硬件配置变更仍需审批。</span>
            <button className="button primary" onClick={() => onToast("设置已保存并写入审计")}>保存更改</button>
          </div>
        </section>
      </div>
    </div>
  );
}

function DeviceWorkspacePage({
  category,
  onAsset,
}: {
  category: "storage" | "gpu" | "network" | "bmc" | "ups" | "snmp";
  onAsset: (asset: Asset) => void;
}) {
  const config = {
    storage: {
      eyebrow: "TRUENAS · SMART · OPENZFS",
      title: "TrueNAS / 存储",
      description: "池、磁盘、容量、健康、温度与 standby-safe SMART 遥测",
      kinds: ["pool", "disk"],
      icon: HardDrive,
    },
    gpu: {
      eyebrow: "NVML · DCGM CAPABILITY",
      title: "GPU",
      description: "温度、功耗、利用率、显存、ECC、PCIe 与能力可用性",
      kinds: ["gpu"],
      icon: Cpu,
    },
    network: {
      eyebrow: "MELLANOX · RDMA · ROCE",
      title: "Mellanox / RDMA",
      description: "链路、FEC、物理错误、拥塞、光模块和 InfiniBand 计数",
      kinds: ["nic"],
      icon: Network,
    },
    bmc: {
      eyebrow: "REDFISH · IPMI 2.0",
      title: "BMC",
      description: "机箱、Thermal、Power、风扇、PSU 与 HealthRollup",
      kinds: ["bmc"],
      icon: Server,
    },
    ups: {
      eyebrow: "NETWORK UPS TOOLS",
      title: "UPS",
      description: "电量、负载、剩余时间、电压、频率、温度和状态",
      kinds: ["ups"],
      icon: Power,
    },
    snmp: {
      eyebrow: "SNMP V3 AUTHPRIV · PROFILE",
      title: "SNMP",
      description: "标准 MIB 与受约束 YAML profile 的实体和传感器映射",
      kinds: ["snmp"],
      icon: Radio,
    },
  }[category];
  const rows = assets.filter((asset) => config.kinds.includes(asset.kind));
  const Icon = config.icon;
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow={config.eyebrow}
        title={config.title}
        description={config.description}
        actions={
          <button className="button primary">
            <Plus size={16} /> 添加连接
          </button>
        }
      />
      <section className="device-summary-grid">
        <article className="panel device-summary-primary">
          <Icon size={24} />
          <div>
            <span>已发现设备</span>
            <strong>{rows.length}</strong>
          </div>
        </article>
        <article className="panel">
          <span>传感器</span>
          <strong>{formatNumber(rows.reduce((sum, row) => sum + row.sensors, 0))}</strong>
        </article>
        <article className="panel">
          <span>未知通道</span>
          <strong>{rows.reduce((sum, row) => sum + row.unknown, 0)}</strong>
        </article>
        <article className="panel">
          <span>只读边界</span>
          <strong>ENFORCED</strong>
        </article>
      </section>
      <section className="panel dedicated-device-list">
        <PanelHeader
          title={`${config.title} 资产`}
          subtitle="Demo fixture · 能力按设备探测"
          icon={Icon}
        />
        {rows.length > 0 ? (
          rows.map((asset) => (
            <button key={asset.id} onClick={() => onAsset(asset)}>
              <AssetIcon asset={asset} />
              <div>
                <strong>{asset.name}</strong>
                <span>{asset.host} · {asset.source}</span>
              </div>
              <div className="device-primary-metric">
                <strong>{asset.primaryMetric}</strong>
                <span>{asset.secondaryMetric}</span>
              </div>
              <HealthPill health={asset.health} />
              <ChevronRight size={16} />
            </button>
          ))
        ) : (
          <div className="empty-device-state">
            <Radio size={22} />
            <strong>暂无演示资产</strong>
            <span>添加连接后，发现结果会保留原始 OID、索引与稳定身份。</span>
          </div>
        )}
      </section>
      <section className="panel capability-workspace">
        <PanelHeader title="Capability matrix" subtitle="连接成功不等于具备全部采集权限" icon={ShieldCheck} />
        {collectorMatrix.slice(0, 6).map((collector) => (
          <div key={collector.label}>
            <span className={`state-dot ${collector.state.includes("采集") || collector.state.includes("增强") ? "healthy" : "warning"}`} />
            <strong>{collector.label}</strong>
            <span>{collector.detail}</span>
          </div>
        ))}
      </section>
    </div>
  );
}

function ControlPlanePage({
  kind,
  onToast,
}: {
  kind: "connections" | "rules" | "users";
  onToast: (message: string) => void;
}) {
  const content = {
    connections: {
      eyebrow: "ENCRYPTED CONNECTION INVENTORY",
      title: "连接",
      description: "只读凭据、执行凭据、TLS 验证和 Agent 路由彼此分离",
      rows: [
        ["TrueNAS SCALE · edge-01", "JSON-RPC WebSocket", "TLS verified", "healthy"],
        ["edge-01 BMC", "Redfish HTTPS", "read-only account", "healthy"],
        ["Rack A UPS", "NUT upsd", "remote read-only", "warning"],
        ["Core switch", "SNMPv3 authPriv", "AES privacy", "healthy"],
      ],
    },
    rules: {
      eyebrow: "DETERMINISTIC RULE ENGINE",
      title: "规则",
      description: "阈值、变化率、计数器增量、持续时间、恢复和维护窗口",
      rows: [
        ["磁盘温度过高", "threshold · 5m", "critical", "healthy"],
        ["UPS 电量偏低", "threshold · 2m", "warning", "warning"],
        ["RDMA 丢弃增长", "counter delta · 5m", "warning", "healthy"],
        ["Agent 遥测缺失", "missing data · 2m", "critical", "healthy"],
      ],
    },
    users: {
      eyebrow: "AUTHENTICATION · RBAC",
      title: "用户与 RBAC",
      description: "Viewer 只读、Operator 运行发现与规则、Admin 管理凭据与审批",
      rows: [
        ["admin@example.com", "Admin", "TOTP recommended", "healthy"],
        ["noc@example.com", "Operator", "Last login 12m", "healthy"],
        ["auditor@example.com", "Viewer", "Read-only", "healthy"],
      ],
    },
  }[kind];
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow={content.eyebrow}
        title={content.title}
        description={content.description}
        actions={
          <button className="button primary" onClick={() => onToast("Demo Mode：表单不会写入真实 Master")}>
            <Plus size={16} /> 新建
          </button>
        }
      />
      <section className="panel control-plane-list">
        {content.rows.map(([name, detail, policy, state]) => (
          <article key={name}>
            <span className={`state-dot ${state}`} />
            <div>
              <strong>{name}</strong>
              <span>{detail}</span>
            </div>
            <code>{policy}</code>
            <button className="button secondary" onClick={() => onToast(`已打开 ${name} 的演示详情`)}>
              查看
            </button>
          </article>
        ))}
      </section>
      <div className="safety-note">
        <LockKeyhole size={17} />
        <span>
          Demo Mode 不保存凭据、不发送通知、不创建用户；切换到 Live Mode 后由自部署 Master 执行 RBAC 与审计。
        </span>
      </div>
    </div>
  );
}

function LiveOverviewPage({
  overview,
  loading,
}: {
  overview: OverviewData;
  loading: boolean;
}) {
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="SELF-HOSTED MASTER · LIVE"
        title="设备总览"
        description="以下数据仅来自当前配置的自部署 Master。"
      />
      <section className="notification-health">
        <div><strong>{loading ? "…" : overview.assetCount}</strong><span>资产</span></div>
        <div><strong>{loading ? "…" : overview.sensorCount}</strong><span>传感器</span></div>
        <div><strong>{loading ? "…" : overview.firingAlertCount}</strong><span>活动告警</span></div>
        <div><strong>{loading ? "…" : `${overview.ingestRate}/s`}</strong><span>摄取速率</span></div>
      </section>
      <section className="panel live-overview-panel">
        <PanelHeader title="实时温度与功耗" subtitle="Master 聚合序列" icon={Activity} />
        {overview.trend.length > 0 ? (
          <TelemetryChart data={overview.trend} range="24h" />
        ) : (
          <div className="empty-device-state">
            <Activity size={22} />
            <strong>尚无趋势样本</strong>
            <span>注册 Agent 或设备连接后，数据会在这里出现。</span>
          </div>
        )}
      </section>
    </div>
  );
}

function LiveResourcePage({ page }: { page: Exclude<Page, "overview"> }) {
  const specification: Record<Exclude<Page, "overview">, { path: string; title: string; kinds?: string[] }> = {
    assets: { path: "/assets", title: "资产" },
    storage: { path: "/assets", title: "TrueNAS / 存储", kinds: ["pool", "disk"] },
    gpu: { path: "/assets", title: "GPU", kinds: ["gpu"] },
    network: { path: "/assets", title: "Mellanox / RDMA", kinds: ["nic"] },
    bmc: { path: "/assets", title: "BMC", kinds: ["bmc", "chassis"] },
    ups: { path: "/assets", title: "UPS", kinds: ["ups"] },
    snmp: { path: "/assets", title: "SNMP", kinds: ["snmp"] },
    discovery: { path: "/discovery/runs", title: "自动发现中心" },
    connections: { path: "/connections", title: "连接" },
    rules: { path: "/rules", title: "规则" },
    alerts: { path: "/alerts", title: "告警" },
    incidents: { path: "/incidents", title: "Incident" },
    diagnostics: { path: "/diagnoses", title: "诊断" },
    actions: { path: "/actions", title: "动作审批" },
    notifications: { path: "/notification-channels", title: "通知" },
    audit: { path: "/audit", title: "审计" },
    users: { path: "/users", title: "用户与 RBAC" },
    settings: { path: "/capabilities", title: "能力与设置" },
  };
  const selected = specification[page];
  const query = useQuery({
    queryKey: ["live-resource", page],
    queryFn: async () => {
      if (!apiBase) throw new Error("NEXT_PUBLIC_API_BASE is not configured");
      const response = await fetch(`${apiBase}${selected.path}`, { credentials: "include" });
      if (!response.ok) throw new Error(`Master returned ${response.status}`);
      const payload = (await response.json()) as { items?: Array<Record<string, unknown>> };
      const rows = payload.items ?? [];
      return selected.kinds
        ? rows.filter((row) => selected.kinds?.includes(String(row.kind)))
        : rows;
    },
  });
  return (
    <div className="page-stack">
      <PageHeading
        eyebrow="SELF-HOSTED MASTER · LIVE DATA"
        title={selected.title}
        description={`GET /api/v1${selected.path} · 不使用演示回退`}
      />
      <section className="panel live-resource-panel">
        {query.isLoading && <div className="empty-device-state"><RefreshCw className="spin" size={22} /><strong>正在读取 Master</strong></div>}
        {query.isError && (
          <div className="empty-device-state">
            <AlertTriangle size={22} />
            <strong>无法读取资源</strong>
            <span>{query.error instanceof Error ? query.error.message : "Unknown error"}</span>
            <button className="button secondary" onClick={() => void query.refetch()}>重试</button>
          </div>
        )}
        {query.isSuccess && query.data.length === 0 && (
          <div className="empty-device-state">
            <Radio size={22} />
            <strong>暂无数据</strong>
            <span>这是 Live Mode 的真实空状态，不是演示数据。</span>
          </div>
        )}
        {query.data?.map((row, index) => (
          <article className="live-resource-row" key={String(row.id ?? index)}>
            <div>
              <strong>{String(row.name ?? row.title ?? row.ruleName ?? row.email ?? row.id ?? "Resource")}</strong>
              <span>{String(row.kind ?? row.state ?? row.role ?? row.status ?? "")}</span>
            </div>
            <code>{JSON.stringify(row)}</code>
          </article>
        ))}
      </section>
    </div>
  );
}

function MetricCard({
  icon: Icon,
  iconTone,
  label,
  value,
  note,
  tone,
}: {
  icon: typeof Activity;
  iconTone: string;
  label: string;
  value: string;
  note: string;
  tone: string;
}) {
  return (
    <article className="metric-card">
      <div className={`metric-icon ${iconTone}`}><Icon size={19} /></div>
      <div className="metric-content">
        <span>{label}</span>
        <strong>{value}</strong>
        <small className={tone}>{note}</small>
      </div>
    </article>
  );
}

function HealthRing({ value }: { value: number }) {
  return (
    <div
      className="health-ring"
      style={{ "--ring-value": `${value * 3.6}deg` } as React.CSSProperties}
    >
      <span>{value}</span>
    </div>
  );
}

function PanelHeader({
  title,
  subtitle,
  icon: Icon,
  action,
}: {
  title: string;
  subtitle?: string;
  icon: typeof Activity;
  action?: React.ReactNode;
}) {
  return (
    <div className="panel-header">
      <div className="panel-title">
        <div className="panel-title-icon"><Icon size={17} /></div>
        <div><strong>{title}</strong>{subtitle && <span>{subtitle}</span>}</div>
      </div>
      {action}
    </div>
  );
}

function TelemetryChart({
  data,
  range,
}: {
  data: OverviewData["trend"];
  range: string;
}) {
  const visible = range === "6h" ? data.slice(-13) : data;
  const option = useMemo(
    () => ({
      animationDuration: 700,
      tooltip: {
        trigger: "axis",
        backgroundColor: "#111c22",
        borderColor: "#31434c",
        textStyle: { color: "#eef6f4", fontSize: 12 },
        axisPointer: { lineStyle: { color: "#50646e", type: "dashed" } },
      },
      grid: { left: 42, right: 45, top: 24, bottom: 28 },
      xAxis: {
        type: "category",
        boundaryGap: false,
        data: visible.map((point) =>
          new Date(point.at).toLocaleTimeString([], {
            hour: "2-digit",
            minute: "2-digit",
          }),
        ),
        axisLine: { lineStyle: { color: "#263940" } },
        axisTick: { show: false },
        axisLabel: { color: "#78909a", fontSize: 10, interval: 7 },
      },
      yAxis: [
        {
          type: "value",
          min: 20,
          max: 90,
          axisLabel: { color: "#78909a", fontSize: 10, formatter: "{value}°" },
          splitLine: { lineStyle: { color: "#1c2c32", type: "dashed" } },
        },
        {
          type: "value",
          min: 100,
          max: 450,
          axisLabel: { color: "#78909a", fontSize: 10, formatter: "{value}W" },
          splitLine: { show: false },
        },
      ],
      series: [
        {
          name: "平均温度",
          type: "line",
          smooth: 0.35,
          showSymbol: false,
          data: visible.map((point) => point.average),
          lineStyle: { color: "#5bd8b8", width: 2 },
          areaStyle: { color: "rgba(91, 216, 184, 0.08)" },
        },
        {
          name: "最高温度",
          type: "line",
          smooth: 0.35,
          showSymbol: false,
          data: visible.map((point) => point.maximum),
          lineStyle: { color: "#f0a34a", width: 2 },
        },
        {
          name: "总功耗",
          type: "line",
          yAxisIndex: 1,
          smooth: 0.35,
          showSymbol: false,
          data: visible.map((point) => point.powerW),
          lineStyle: { color: "#7e8ce0", width: 1.5, type: "dashed" },
        },
      ],
    }),
    [visible],
  );
  return (
    <ReactECharts
      option={option}
      style={{ height: 258, width: "100%" }}
      opts={{ renderer: "canvas" }}
      notMerge
    />
  );
}

function AssetTable({
  assets: rows,
  onAsset,
}: {
  assets: Asset[];
  onAsset: (asset: Asset) => void;
}) {
  return (
    <div className="asset-table">
      <div className="asset-row header">
        <span>设备</span><span>健康</span><span>关键指标</span><span>传感器</span><span>采集来源</span><span>最近数据</span><span />
      </div>
      {rows.map((asset) => (
        <button className="asset-row" key={asset.id} onClick={() => onAsset(asset)}>
          <span className="asset-name-cell">
            <AssetIcon asset={asset} />
            <span><strong>{asset.name}</strong><small>{asset.host}</small></span>
          </span>
          <span><HealthPill health={asset.health} /></span>
          <span className="key-metric"><strong>{asset.primaryMetric}</strong><small>{asset.secondaryMetric}</small></span>
          <span className="sensor-count"><strong>{formatNumber(asset.sensors)}</strong>{asset.unknown > 0 && <small>{asset.unknown} 未知</small>}</span>
          <span className="source-cell">{asset.source}</span>
          <span className="last-seen"><span className={`state-dot ${asset.connection === "online" ? "healthy" : "warning"}`} />{asset.lastSeenSeconds}s</span>
          <span><ChevronRight size={15} /></span>
        </button>
      ))}
      {rows.length === 0 && (
        <EmptyState icon={Search} title="没有匹配的资产" copy="调整设备类型或搜索关键词。" />
      )}
    </div>
  );
}

function AssetIcon({ asset }: { asset: Asset }) {
  const Icon =
    asset.kind === "gpu" ? Cpu :
    asset.kind === "nic" || asset.kind === "snmp" ? Network :
    asset.kind === "disk" || asset.kind === "pool" ? HardDrive :
    asset.kind === "ups" ? Zap :
    asset.kind === "bmc" ? Gauge : Server;
  return <div className={`asset-icon ${asset.kind}`}><Icon size={18} /></div>;
}

function HealthPill({ health }: { health: Health }) {
  const { t } = useTranslation();
  return (
    <span className={`health-pill ${health}`}>
      <span className="state-dot" /> {t(health)}
    </span>
  );
}

function StatusPill({ status }: { status: string }) {
  const normalized = status.toLowerCase().replace(/\s/g, "-");
  return <span className={`status-pill ${normalized}`}>{status}</span>;
}

function SeverityIcon({ severity }: { severity: "critical" | "warning" | "info" }) {
  return (
    <div className={`severity-icon ${severity}`}>
      {severity === "critical" ? <AlertTriangle size={17} /> : severity === "warning" ? <AlertTriangle size={17} /> : <Info size={17} />}
    </div>
  );
}

function Confidence({ value, large = false }: { value: number; large?: boolean }) {
  return (
    <span className={`confidence ${large ? "large" : ""}`}>
      <span style={{ "--confidence": `${Math.round(value * 100)}%` } as React.CSSProperties} />
      <strong>{Math.round(value * 100)}%</strong>
    </span>
  );
}

function PageHeading({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow: string;
  title: string;
  description: string;
  actions?: React.ReactNode;
}) {
  return (
    <section className="page-heading">
      <div><span>{eyebrow}</span><h1>{title}</h1><p>{description}</p></div>
      {actions && <div className="heading-actions">{actions}</div>}
    </section>
  );
}

function EmptyState({
  icon: Icon,
  title,
  copy,
}: {
  icon: typeof Activity;
  title: string;
  copy: string;
}) {
  return (
    <div className="empty-state">
      <div><Icon size={22} /></div><strong>{title}</strong><span>{copy}</span>
    </div>
  );
}

function DiscoveryModal({
  onClose,
  onStart,
}: {
  onClose: () => void;
  onStart: (target: string, collectors: string[]) => void;
}) {
  const [target, setTarget] = useState("edge-01");
  const [selected, setSelected] = useState(["hwmon", "SMART / NVMe", "ZFS"]);
  const options = ["hwmon", "PCI / USB", "SMART / NVMe", "ZFS", "NVML", "RDMA / ethtool", "Redfish", "SNMP", "NUT"];
  const toggle = (value: string) =>
    setSelected((items) =>
      items.includes(value) ? items.filter((item) => item !== value) : [...items, value],
    );
  return (
    <ModalFrame onClose={onClose} size="medium">
      <div className="modal-heading">
        <div className="modal-icon"><ScanSearch size={20} /></div>
        <div><span>只读任务</span><h2>新建发现任务</h2><p>选择明确目标与协议；任务全程限速并写入审计。</p></div>
      </div>
      <label className="form-field">
        <span>目标 Agent 或设备</span>
        <select value={target} onChange={(event) => setTarget(event.target.value)}>
          <option>edge-01</option><option>compute-02</option><option>10.20.2.1 · SNMPv3</option><option>10.20.1.11 · Redfish</option>
        </select>
      </label>
      <div className="form-field">
        <span>Collector</span>
        <div className="collector-picker">
          {options.map((option) => (
            <button
              key={option}
              className={selected.includes(option) ? "selected" : ""}
              onClick={() => toggle(option)}
            >
              <span>{selected.includes(option) && <Check size={13} />}</span>{option}
            </button>
          ))}
        </div>
      </div>
      <div className="safety-note">
        <ShieldCheck size={17} />
        <span>不会猜测凭据、加载驱动、唤醒休眠磁盘或修改设备配置。</span>
      </div>
      <div className="modal-actions">
        <button className="button secondary" onClick={onClose}>取消</button>
        <button className="button primary" disabled={selected.length === 0} onClick={() => onStart(target, selected)}>
          <Play size={15} /> 开始发现
        </button>
      </div>
    </ModalFrame>
  );
}

function AssetDrawer({ asset, onClose }: { asset: Asset; onClose: () => void }) {
  return (
    <Drawer onClose={onClose}>
      <div className="drawer-asset-head">
        <AssetIcon asset={asset} />
        <div><span>{asset.vendor} · {asset.kind.toUpperCase()}</span><h2>{asset.name}</h2><p>{asset.model}</p></div>
        <HealthPill health={asset.health} />
      </div>
      <div className="asset-identity">
        <span>稳定身份</span>
        <code>{asset.kind === "disk" ? "wwn:0x5000c500cafe" : `${asset.kind}:${asset.id}`}</code>
      </div>
      <div className="asset-kpi-grid">
        <div><span>关键指标</span><strong>{asset.primaryMetric}</strong></div>
        <div><span>辅助指标</span><strong>{asset.secondaryMetric}</strong></div>
        <div><span>传感器</span><strong>{formatNumber(asset.sensors)}</strong></div>
        <div><span>最近数据</span><strong>{asset.lastSeenSeconds}s</strong></div>
      </div>
      <h3 className="drawer-section-title">Capability matrix</h3>
      <div className="drawer-capabilities">
        {asset.capabilities.map((capability) => (
          <div key={capability.label}>
            <span className={`state-dot ${
              capability.state === "ready" || capability.state === "enhanced"
                ? "healthy"
                : capability.state === "permission"
                  ? "warning"
                  : "muted"
            }`} />
            <strong>{capability.label}</strong>
            <span>{{
              ready: "可采集",
              enhanced: "已增强",
              permission: "需权限",
              unavailable: "不支持 / 未安装",
            }[capability.state]}</span>
          </div>
        ))}
      </div>
      <h3 className="drawer-section-title">24 小时态势</h3>
      <div className="mini-telemetry">
        {overviewData.trend.slice(-24).map((point, index) => (
          <i key={point.at} style={{ height: `${22 + ((point.maximum - 55) / 30) * 62}%` }} className={index > 19 ? "hot" : ""} />
        ))}
      </div>
      <h3 className="drawer-section-title">原始上下文</h3>
      <div className="raw-context">
        <div><span>来源</span><code>{asset.source}</code></div>
        <div><span>拓扑路径</span><code>{asset.host}</code></div>
        <div><span>最近发现</span><code>2026-07-28 14:24:08 CST</code></div>
        <div><span>标签</span><span>{asset.tags.join(" · ")}</span></div>
      </div>
      <div className="drawer-actions">
        <button className="button secondary"><SlidersHorizontal size={15} /> 配置阈值</button>
        <button className="button primary"><Activity size={15} /> 查看全部指标</button>
      </div>
    </Drawer>
  );
}

function SuggestionDrawer({
  suggestion,
  onClose,
  onReview,
}: {
  suggestion: Suggestion;
  onClose: () => void;
  onReview: (suggestion: Suggestion, decision: "Accepted" | "Rejected") => void;
}) {
  return (
    <Drawer onClose={onClose}>
      <div className="modal-heading drawer-heading">
        <div className="modal-icon violet"><Sparkles size={20} /></div>
        <div><span>LLM 标注建议</span><h2>{suggestion.suggestedName}</h2><p>{suggestion.assetName} · {suggestion.currentValue}</p></div>
        <Confidence value={suggestion.confidence} large />
      </div>
      <div className="identity-warning">
        <LockKeyhole size={16} />
        <span>接受只会设置展示别名与类别。原始名称、单位、路径、换算和阈值不会被改写。</span>
      </div>
      <div className="comparison-grid">
        <div><span>原始名称</span><code>{suggestion.rawName}</code></div>
        <ChevronRight size={18} />
        <div><span>建议展示名</span><strong>{suggestion.suggestedName}</strong></div>
      </div>
      <div className="suggested-fields">
        <div><span>建议类别</span><strong>{suggestion.suggestedCategory}</strong></div>
        <div><span>当前状态</span><StatusPill status={suggestion.status} /></div>
      </div>
      <h3 className="drawer-section-title">可验证依据</h3>
      <ul className="evidence-list">
        {suggestion.evidence.map((evidence) => (
          <li key={evidence}><CheckCircle2 size={16} /><span>{evidence}</span></li>
        ))}
      </ul>
      <h3 className="drawer-section-title">输入上下文</h3>
      <div className="context-tags roomy">
        {suggestion.context.map((item) => <span key={item}>{item}</span>)}
      </div>
      <div className="path-block">
        <span>原始采集路径</span><code>{suggestion.sourcePath}</code>
      </div>
      {suggestion.status === "Pending" ? (
        <div className="drawer-actions split">
          <button className="button danger-soft" onClick={() => onReview(suggestion, "Rejected")}>
            <X size={15} /> 拒绝
          </button>
          <button className="button primary" onClick={() => onReview(suggestion, "Accepted")}>
            <Check size={15} /> 接受建议
          </button>
        </div>
      ) : (
        <div className="reviewed-banner"><CheckCircle2 size={17} /><span>该建议已完成审核：{suggestion.status}</span></div>
      )}
    </Drawer>
  );
}

function ActionModal({
  action,
  onClose,
  onReview,
}: {
  action: ActionRequest;
  onClose: () => void;
  onReview: (action: ActionRequest, decision: "approve" | "reject") => void;
}) {
  const [confirmation, setConfirmation] = useState("");
  const canApprove = confirmation === action.assetName;
  return (
    <ModalFrame onClose={onClose} size="large">
      <div className="modal-heading">
        <div className={`modal-icon risk-${action.risk}`}><Terminal size={20} /></div>
        <div><span>动作审批 · {action.risk.toUpperCase()} RISK</span><h2>{action.title}</h2><p>{action.assetName} · 申请人 {action.requestedBy}</p></div>
        <StatusPill status={action.state} />
      </div>
      <div className="action-review-grid">
        <div><span>完整命令</span><code>{action.command}</code></div>
        <div><span>工作目录</span><code>/</code></div>
        <div><span>执行身份</span><code>{action.identity}</code></div>
        <div><span>强制超时</span><code>{action.timeout}</code></div>
      </div>
      <div className="hash-block">
        <div><LockKeyhole size={16} /><span>不可变请求哈希</span></div>
        <code>{action.immutableHash}</code>
      </div>
      {action.state === "pending" ? (
        <>
          <label className="form-field confirmation-field">
            <span>输入目标名称 <strong>{action.assetName}</strong> 以确认绑定</span>
            <input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} placeholder={action.assetName} autoFocus />
          </label>
          <div className="safety-note amber">
            <AlertTriangle size={17} />
            <span>批准后将生成一次性令牌。执行仍受非交互、进程组终止与输出上限约束。</span>
          </div>
          <div className="modal-actions spaced">
            <button className="button danger-soft" onClick={() => onReview(action, "reject")}><X size={15} /> 拒绝请求</button>
            <div>
              <button className="button secondary" onClick={onClose}>取消</button>
              <button className="button primary" disabled={!canApprove} onClick={() => onReview(action, "approve")}><ShieldCheck size={15} /> 批准并签发令牌</button>
            </div>
          </div>
        </>
      ) : (
        <div className="reviewed-banner"><CheckCircle2 size={17} /><span>当前状态：{action.state}。命令哈希已冻结。</span></div>
      )}
    </ModalFrame>
  );
}

function CommandPalette({
  runtimeMode,
  onClose,
  onNavigate,
  onAsset,
}: {
  runtimeMode: RuntimeMode;
  onClose: () => void;
  onNavigate: (page: Page) => void;
  onAsset: (asset: Asset) => void;
}) {
  const [query, setQuery] = useState("");
  const pageItems: Array<[Page, string, typeof Activity]> = [
    ["overview", "设备总览", LayoutDashboard],
    ["assets", "资产拓扑", Boxes],
    ["discovery", "自动发现中心", ScanSearch],
    ["alerts", "活动告警", Bell],
    ["diagnostics", "智能诊断", Bot],
    ["actions", "动作审批", ClipboardCheck],
  ];
  const filteredAssets =
    runtimeMode === "demo"
      ? assets
          .filter((asset) =>
            `${asset.name} ${asset.model}`
              .toLowerCase()
              .includes(query.toLowerCase()),
          )
          .slice(0, 4)
      : [];
  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div className="command-palette" onMouseDown={(event) => event.stopPropagation()}>
        <label><Search size={19} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索页面、资产或传感器…" autoFocus /><kbd>ESC</kbd></label>
        <div className="command-results">
          {!query && (
            <div className="command-group">
              <span>快速跳转</span>
              {pageItems.map(([id, label, Icon]) => (
                <button key={id} onClick={() => onNavigate(id)}><Icon size={17} /><span>{label}</span><ChevronRight size={14} /></button>
              ))}
            </div>
          )}
          {runtimeMode === "demo" && (
            <div className="command-group">
              <span>{query ? "匹配资产" : "最近资产"}</span>
              {filteredAssets.map((asset) => (
                <button key={asset.id} onClick={() => onAsset(asset)}><AssetIcon asset={asset} /><span><strong>{asset.name}</strong><small>{asset.model}</small></span><HealthPill health={asset.health} /></button>
              ))}
            </div>
          )}
        </div>
        <div className="command-foot"><span><kbd>↑</kbd><kbd>↓</kbd> 选择</span><span><kbd>↵</kbd> 打开</span><span>原始名称与稳定 ID 均可搜索</span></div>
      </div>
    </div>
  );
}

function Drawer({
  children,
  onClose,
}: {
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <div className="drawer-backdrop" onMouseDown={onClose}>
      <aside className="drawer" onMouseDown={(event) => event.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="关闭"><X size={18} /></button>
        {children}
      </aside>
    </div>
  );
}

function ModalFrame({
  children,
  onClose,
  size,
}: {
  children: React.ReactNode;
  onClose: () => void;
  size: "medium" | "large";
}) {
  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <section className={`modal-card ${size}`} onMouseDown={(event) => event.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="关闭"><X size={18} /></button>
        {children}
      </section>
    </div>
  );
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}
