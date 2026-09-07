import type {DataDirectoryInfo, RunContext, RuntimeInfo, Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";

export type OverviewLane = {
  target: RunContext | null;
  runtime: RuntimeInfo | null;
  dataDirectory: DataDirectoryInfo | null;
};

export type OverviewState = "active" | "not-running" | "no-context";

export type OverviewModel = {
  current: OverviewLane;
  configured: OverviewLane | null;
  knownGood: OverviewLane | null;
  state: OverviewState;
};

export function sameRunContext(left: RunContext | null | undefined, right: RunContext | null | undefined): boolean {
  if (!left || !right) {
    return left === right;
  }
  return left.runtimeId === right.runtimeId &&
    left.node.kind === right.node.kind &&
    (left.node.installationId ?? "") === (right.node.installationId ?? "") &&
    left.profile.dataDirectoryId === right.profile.dataDirectoryId &&
    left.profile.name === right.profile.name;
}

export function buildOverviewModel(snapshot: Snapshot): OverviewModel {
  const current = snapshot.current ?? null;
  const configured = snapshot.configured ?? null;
  const knownGood = snapshot.knownGood ?? null;
  const state: OverviewState = current ? "active" : configured ? "not-running" : "no-context";

  return {
    current: laneFor(snapshot, current),
    configured: configured ? laneFor(snapshot, configured) : null,
    knownGood: knownGood ? laneFor(snapshot, knownGood) : null,
    state
  };
}

function laneFor(snapshot: Snapshot, target: RunContext | null): OverviewLane {
  if (!target) {
    return {target: null, runtime: null, dataDirectory: null};
  }
  return {
    target,
    runtime: (snapshot.runtimes ?? []).find((item) => item.id === target.runtimeId) ?? null,
    dataDirectory: (snapshot.dataDirectories ?? []).find((item) => item.id === target.profile.dataDirectoryId) ?? null
  };
}
