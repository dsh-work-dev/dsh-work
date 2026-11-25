/** E-68 presentation contract. Null/absent means uncollected, never zero. All sizes are bytes, durations seconds, timestamps ISO-8601. */
export type DashboardPeriod = "today" | "week" | "month";
export type DashboardLocale = "zh-CN" | "en" | "ja-JP";
export type DashboardState = "running" | "waiting" | "completed" | "interrupted" | "scheduled";
export interface DashboardSession {id: string; title: string; state: DashboardState; summary?: string; workspace?: string; updatedAt?: string; nextRunAt?: string; turns?: number; tokens?: number;}
export interface DashboardEvent {id: string; at: string; title: string; description?: string; source: "DSH" | "DSH Work"; state?: DashboardState | "info"; sessionId?: string;}
export interface DashboardOutput {completed?: number | null; agentSeconds?: number | null; changedFiles?: number | null; addedLines?: number | null; removedLines?: number | null; toolCalls?: {name: string; count: number}[]; toolFailureRate?: number | null;}
export interface DashboardCost {currency: string; spent?: number | null; tokens?: number | null; balance?: number | null; runwaySeconds?: number | null; consumedFraction?: number | null;}
export interface DashboardTokenBucket {at: string; cacheHit: number; cacheMiss: number; output: number;}
export interface DashboardModelUsage {name: string; tokens: number; cost?: number | null; currency?: string;}
export interface DashboardCapability {id: "browser" | "computer" | "pet" | "notification"; label?: string; state: "ready" | "disabled" | "error" | "unknown"; detail?: string;}
export interface DashboardHost {state: "running" | "starting" | "stopped" | "failed" | "unknown"; uptimeSeconds?: number | null; recoveredCount?: number | null; recoveryTrackingSince?: string; dshVersion?: string; profile?: string; nodeVersion?: string; restorePoint?: {id: string; label: string; at?: string; unavailable?: string}; canSaveRestorePoint?: boolean; environmentBusy?: boolean; failure?: string; capabilities?: DashboardCapability[]; suspectPluginId?: string;}
export interface DashboardRuntime {plugins?: number | null; disabledPlugins?: number | null; pluginUpdates?: number | null; skills?: number | null; scheduled?: number | null; nextScheduledAt?: string; workspaces?: {id: string; name: string; branch?: string; changedFiles?: number | null}[];}
export interface DashboardComputer {sampledAt?: string; cpuPercent?: number | null; agentCpuPercent?: number | null; memoryUsedBytes?: number | null; memoryTotalBytes?: number | null; agentMemoryBytes?: number | null; diskUsedBytes?: number | null; diskTotalBytes?: number | null; diskLabel?: string; cpuHistory?: {at: string; percent: number}[]; processes?: {pid: number; name: string; role?: string; cpuPercent?: number | null; memoryBytes?: number | null}[];}
export interface DashboardHealth {id: "diagnostics" | "updates" | "storage" | "browser" | "privacy"; state: "ok" | "warning" | "error" | "unknown"; summary: string;}
export interface DashboardSessions {
  items: DashboardSession[];
  /** Conversation coverage at sample time, independent of schedules and current connectivity. */
  listCoverage?: "ready" | "partial" | "unavailable";
}
export interface DashboardData {availability?: Record<string, {state: "ready" | "partial" | "unavailable"; reason?: string; failedSessions?: number}>; dshSampledAt?: string; dshStaleAt?: string; sourceError?: string; locale?: DashboardLocale; now?: string; period?: DashboardPeriod; sessions?: DashboardSessions; output?: DashboardOutput; events?: DashboardEvent[]; cost?: DashboardCost; hourlyTokens?: DashboardTokenBucket[]; models?: DashboardModelUsage[]; cacheHitRate?: number | null; cacheDelta?: number | null; host?: DashboardHost; runtime?: DashboardRuntime; computer?: DashboardComputer; health?: DashboardHealth[];}
export type DashboardSettingsSection = "runtime" | "computer" | "pets" | "notifications" | "plugins" | "diagnostics" | "updates" | "storage" | "browser";
export interface DashboardCallbacks {onSwitchEnvironment?: () => void | Promise<void>; onSaveRestorePoint?: () => void | Promise<void>; onRestore?: (restorePointId: string) => void | Promise<void>; onDisablePluginAndRetry?: (pluginId: string) => void | Promise<void>; onUpgradePlugins?: () => void | Promise<void>; onOpenSession?: (sessionId: string) => void | Promise<void>; onSettings?: (section: DashboardSettingsSection) => void | Promise<void>; onPeriodChange?: (period: DashboardPeriod) => void | Promise<void>;}
export interface DashboardHandle {update(data: DashboardData): void; destroy(): void;}
