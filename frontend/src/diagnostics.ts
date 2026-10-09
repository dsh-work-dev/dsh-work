import type {Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";

/** Diagnostics users paste into a report: version, update and lifecycle state, and the current environment. */
export function diagnosticsReport(input: {update: unknown; state?: string; snapshot?: Snapshot}): string {
  return JSON.stringify({version: __APP_VERSION__, update: input.update, state: input.state, current: input.snapshot?.current, failure: input.snapshot?.lastSwitchAttempt}, null, 2);
}
