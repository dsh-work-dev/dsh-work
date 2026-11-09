/** Overview groups and the tiles each one holds, in default order. Ids are stable storage keys. */
export const dashboardGroups = {
  conversations: ["orbit", "output", "results", "timeline"],
  usage: ["cost", "tokens", "models"],
  environment: ["host", "runtime", "computer", "health"],
} as const;

export type DashboardGroup = keyof typeof dashboardGroups;
export type DashboardTile = (typeof dashboardGroups)[DashboardGroup][number];

export interface DashboardLayout {
  groups: DashboardGroup[];
  tiles: Record<DashboardGroup, DashboardTile[]>;
}

export const dashboardLayoutKey = "dsh-work-overview-layout";

const groupIds = Object.keys(dashboardGroups) as DashboardGroup[];

export function defaultDashboardLayout(): DashboardLayout {
  return {
    groups: [...groupIds],
    tiles: Object.fromEntries(groupIds.map((group) => [group, [...dashboardGroups[group]]])) as DashboardLayout["tiles"],
  };
}

/** Keeps each known id once in its saved position, appends ids the save does not mention, and drops cross-group moves. */
function ordered<T extends string>(saved: unknown, allowed: readonly T[]): T[] {
  const kept = Array.isArray(saved) ? saved.filter((id): id is T => typeof id === "string" && allowed.includes(id as T)) : [];
  return Array.from(new Set([...kept, ...allowed]));
}

export function normalizeDashboardLayout(value: unknown): DashboardLayout {
  if (!value || typeof value !== "object") return defaultDashboardLayout();
  const raw = value as {groups?: unknown; tiles?: unknown};
  const savedTiles = raw.tiles && typeof raw.tiles === "object" ? (raw.tiles as Record<string, unknown>) : {};
  return {
    groups: ordered(raw.groups, groupIds),
    tiles: Object.fromEntries(groupIds.map((group) => [group, ordered(savedTiles[group], dashboardGroups[group])])) as DashboardLayout["tiles"],
  };
}
