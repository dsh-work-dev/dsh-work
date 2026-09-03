import type {DataDirectoryInfo, LaunchTarget, RuntimeInfo, Snapshot} from "../bindings/github.com/local/work/internal/dshmanager";

export type OverviewLane = {
  target: LaunchTarget | null;
  runtime: RuntimeInfo | null;
  dataDirectory: DataDirectoryInfo | null;
};

export type OverviewState = "active" | "restart-required" | "not-running" | "no-target";

export type OverviewModel = {
  current: OverviewLane;
  next: OverviewLane | null;
  state: OverviewState;
};

export function sameLaunchTarget(left: LaunchTarget | null | undefined, right: LaunchTarget | null | undefined): boolean {
  if (!left || !right) {
    return left === right;
  }
  return left.runtimeId === right.runtimeId &&
    left.profile.dataDirectoryId === right.profile.dataDirectoryId &&
    left.profile.name === right.profile.name;
}

export function buildOverviewModel(snapshot: Snapshot): OverviewModel {
  const active = snapshot.active ?? null;
  const desired = snapshot.desired ?? null;
  const current = laneFor(snapshot, active);
  const next = desired && (!active || !sameLaunchTarget(active, desired))
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

function laneFor(snapshot: Snapshot, target: LaunchTarget | null): OverviewLane {
  if (!target) {
    return {target: null, runtime: null, dataDirectory: null};
  }
  return {
    target,
    runtime: (snapshot.runtimes ?? []).find((item) => item.id === target.runtimeId) ?? null,
    dataDirectory: (snapshot.dataDirectories ?? []).find((item) => item.id === target.profile.dataDirectoryId) ?? null
  };
}
