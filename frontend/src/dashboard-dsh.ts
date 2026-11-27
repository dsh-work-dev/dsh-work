import type {DashboardData} from "./dashboard-model";

export interface DashboardAvailability {state: "ready" | "partial" | "unavailable"; reason?: string; failedSessions?: number;}
export interface DashboardDSHSnapshot {schemaVersion: 1; collectedAt: string; availability: Record<string, DashboardAvailability>; data: Partial<DashboardData>;}
type Validator = (value: unknown) => unknown;
const object = (value: unknown): Record<string, unknown> | undefined => value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
const string: Validator = v => typeof v === "string" ? v.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 4096) : undefined;
const positive: Validator = v => v === null ? null : typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : undefined;
const amount: Validator = v => v === null ? null : typeof v === "number" && Number.isFinite(v) ? v : undefined;
const fraction: Validator = v => v === null ? null : typeof v === "number" && Number.isFinite(v) && v >= 0 && v <= 1 ? v : undefined;
const delta: Validator = v => typeof v === "number" && Number.isFinite(v) && v >= -1 && v <= 1 ? v : undefined;
const date: Validator = v => typeof v === "string" && Number.isFinite(Date.parse(v)) ? new Date(v).toISOString() : undefined;
const enumeration = (...values: string[]): Validator => v => typeof v === "string" && values.includes(v) ? v : undefined;
const shape = (fields: Record<string, Validator>): Validator => value => {
  const input = object(value); if (!input) return undefined;
  const output: Record<string, unknown> = {};
  for (const [key, validate] of Object.entries(fields)) { const v = validate(input[key]); if (v !== undefined) output[key] = v; }
  return output;
};
const list = (validate: Validator, required: string[] = []): Validator => value => Array.isArray(value) ? value.slice(0, 100000).map(validate).filter(v => {
  const row = object(v); return row && required.every(key => row[key] !== undefined && row[key] !== null);
}) : undefined;
const state = enumeration("running", "waiting", "completed", "interrupted", "scheduled");
const sessionsShape = shape({items: list(shape({id: string, title: string, state, summary: string, workspace: string, updatedAt: date, nextRunAt: date, turns: positive, tokens: positive}), ["id", "title", "state"])});
const validateData = shape({
  now: date, period: enumeration("today", "week", "month"), locale: enumeration("zh-CN", "en", "ja-JP"),
  sessions: value => Array.isArray(object(value)?.items) ? sessionsShape(value) : undefined,
  output: shape({completed: positive, agentSeconds: positive, changedFiles: positive, addedLines: positive, removedLines: positive,
    toolCalls: list(shape({name: string, count: positive}), ["name", "count"]), toolFailureRate: fraction}),
  cost: shape({currency: enumeration("CNY", "USD"), spent: positive, tokens: positive, balance: amount, runwaySeconds: positive, consumedFraction: fraction}),
  hourlyTokens: list(shape({at: date, cacheHit: positive, cacheMiss: positive, output: positive}), ["at", "cacheHit", "cacheMiss", "output"]),
  models: list(shape({name: string, tokens: positive, cost: positive, currency: enumeration("CNY", "USD")}), ["name", "tokens"]),
  cacheHitRate: fraction, cacheDelta: delta,
  events: list(shape({id: string, at: date, title: string, description: string, source: enumeration("DSH", "DSH Work"), state: enumeration("running", "waiting", "completed", "interrupted", "scheduled", "info"), sessionId: string}), ["id", "at", "title", "source"]),
  runtime: shape({plugins: positive, disabledPlugins: positive, pluginUpdates: positive, skills: positive, scheduled: positive, nextScheduledAt: date,
    workspaces: list(shape({id: string, name: string, branch: string, changedFiles: positive}), ["id", "name"])}),
  health: list(shape({id: enumeration("privacy"), state: enumeration("ok", "warning", "error", "unknown"), summary: string}), ["id", "state", "summary"]),
});

/** Parse bounded display data. The trusted Go owner isolates and persists snapshots by RunContext. */
export function parseDashboardDSHSnapshot(payload: string): DashboardDSHSnapshot | undefined {
  if (payload.length > 8 * 1024 * 1024) return undefined;
  try {
    const value = object(JSON.parse(payload));
    if (value?.schemaVersion !== 1 || date(value.collectedAt) === undefined || !object(value.data)) return undefined;
    const availability: Record<string, DashboardAvailability> = {};
    for (const [name, item] of Object.entries(object(value.availability) ?? {}).slice(0, 32)) {
      const valid = shape({state: enumeration("ready", "partial", "unavailable"), reason: string, failedSessions: positive})(item) as DashboardAvailability | undefined;
      if (valid?.state) availability[name] = valid;
    }
    return {schemaVersion: 1, collectedAt: date(value.collectedAt) as string, availability, data: validateData(value.data) as Partial<DashboardData>};
  } catch { return undefined; }
}

/** One projection into the renderer's contract; no browser-side data cache. */
export function parseDashboardDSH(payload: string, staleAt?: string): Partial<DashboardData> {
  const snapshot = parseDashboardDSHSnapshot(payload);
  if (!snapshot) return {};
  const data = snapshot.data;
  const source = snapshot.availability;
  const map = (...keys: string[]): DashboardAvailability => {
    const values = keys.map(key => source[key]).filter(Boolean);
    const state = !values.length || values.every(v => v.state === "unavailable") ? "unavailable" : values.some(v => v.state !== "ready") ? "partial" : "ready";
    return {state};
  };
  const sessions = map("sessions");
  if (data.sessions) data.sessions.listCoverage = sessions.state;
  data.dshStaleAt = date(staleAt) as string | undefined;
  data.availability = {orbit: map("sessions", "schedules"), sessions, output: map("sessions", "changes"), events: map("sessions", "schedules"),
    cost: map("usage", "balance", "billing"), hourlyTokens: map("usage"), models: map("usage"), runtime: map("workspaces", "git", "skills", "schedules")};
  if (data.dshStaleAt) for (const module of Object.values(data.availability)) module.state = "unavailable";
  if (data.dshStaleAt && data.health) data.health = data.health.map(item => ({...item, state: "unknown", summary: {
    "zh-CN": "遥测与上报状态暂不可用", en: "Reporting status unavailable", "ja-JP": "テレメトリーと送信の状態を確認できません",
  }[data.locale ?? "zh-CN"]}));
  data.dshSampledAt = snapshot.collectedAt;
  return data;
}
