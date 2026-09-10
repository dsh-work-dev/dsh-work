import type {OperationStatus} from "../bindings/github.com/local/dsh-work/internal/acquisition/models";
import type {RuntimePreparation} from "./lifecycle";
import {t} from "./i18n";

export function runtimePreparationText(preparation: RuntimePreparation): string {
  if (preparation.state === "failed" && preparation.error?.summary) {
    return preparation.error.summary;
  }
  switch (preparation.state) {
    case "acquiring-node":
      return t("runtimes.downloadingNode");
    case "acquiring-dsh":
      return t("runtimes.downloadingDsh");
    case "verifying":
      return t("runtimes.verifying");
    case "installed":
      return t(preparation.artifactKind === "node" ? "runtimes.nodePrepared" : "runtimes.prepared");
    case "cancelled":
      return t("runtimes.cancelled");
    case "failed":
      return t("runtimes.failed");
  }
  switch (preparation.operation) {
    case "detect-pnpm":
      return t("runtimes.checkingTools");
    case "detect-npm":
      return t("runtimes.checkingTools");
    case "download-node":
      return t("runtimes.downloadingNode");
    case "install-dsh":
      return t("runtimes.downloadingDsh");
    case "verify":
      return t("runtimes.verifying");
    default:
      return t("runtimes.preparing");
  }
}

export function acquisitionPreparation(status: OperationStatus): RuntimePreparation {
  const state = status.state === "succeeded" ? "installed" : status.state === "failed" ? "failed" : status.state === "canceled" ? "cancelled" :
    status.step === "verify" ? "verifying" : status.step.startsWith("detect-") ? "resolving-toolchain" :
    status.artifact.kind === "node" ? "acquiring-node" : "acquiring-dsh";
  const resultRoute = status.result?.route || undefined;
  return {
    state,
    operation: (status.step || "none") as RuntimePreparation["operation"],
    targetVersion: status.artifact.version,
    source: (status.route || resultRoute || "none") as RuntimePreparation["source"],
    receivedBytes: status.receivedBytes,
    totalBytes: status.totalBytes,
    hasTotal: status.hasTotal,
    canCancel: status.canCancel,
    operationId: status.operationId,
    artifactKind: status.artifact.kind as RuntimePreparation["artifactKind"],
    attempt: status.attempt,
    error: status.result?.failure ? {
      code: status.result.failure.kind,
      summary: status.result.failure.summary,
      retryable: status.result.failure.retryable,
      effectOccurred: false,
      correlationId: status.operationId || ""
    } : undefined,
    result: status.result ? {
      succeeded: status.result.succeeded,
      route: resultRoute as "official" | "mirror" | "local" | undefined,
      failure: status.result.failure ? {
        kind: status.result.failure.kind,
        retryable: status.result.failure.retryable,
      } : undefined,
    } : undefined,
  };
}

export function formatRuntimeBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) {
    return "0 B";
  }
  if (value < 1024) {
    return `${Math.round(value)} B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(1)} KB`;
  }
  if (value < 1024 * 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(1)} MB`;
  }
  return `${(value / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}
