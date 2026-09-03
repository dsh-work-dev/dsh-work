import type {HomeInfo, LaunchSelection, RuntimeInfo, Snapshot} from "../bindings/github.com/local/work/internal/dshmanager";

export type OverviewLane = {
  selection: LaunchSelection | null;
  runtime: RuntimeInfo | null;
  home: HomeInfo | null;
};

export type OverviewState = "active" | "restart-required" | "not-running" | "no-target";

export type OverviewModel = {
  current: OverviewLane;
  next: OverviewLane | null;
  state: OverviewState;
};

export function sameLaunchSelection(left: LaunchSelection | null | undefined, right: LaunchSelection | null | undefined): boolean {
  if (!left || !right) {
    return left === right;
  }
  return left.runtimeId === right.runtimeId &&
    left.profile.homeId === right.profile.homeId &&
    left.profile.name === right.profile.name &&
    left.workspace === right.workspace;
}

export function buildOverviewModel(snapshot: Snapshot): OverviewModel {
  const active = snapshot.active ?? null;
  const desired = snapshot.desired ?? null;
  const current = laneFor(snapshot, active);
  const next = desired && (!active || !sameLaunchSelection(active, desired))
    ? laneFor(snapshot, desired)
    : null;

  let state: OverviewState = "no-target";
  if (active) {
    state = next ? "restart-required" : "active";
  } else if (desired) {
    state = "not-running";
  }

  return {current, next, state};
}

function laneFor(snapshot: Snapshot, selection: LaunchSelection | null): OverviewLane {
  if (!selection) {
    return {selection: null, runtime: null, home: null};
  }
  return {
    selection,
    runtime: (snapshot.runtimes ?? []).find((item) => item.id === selection.runtimeId) ?? null,
    home: (snapshot.homes ?? []).find((item) => item.id === selection.profile.homeId) ?? null
  };
}
