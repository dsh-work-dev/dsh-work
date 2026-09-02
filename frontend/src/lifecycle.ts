export type LifecycleState = "Starting" | "Ready" | "Stopping" | "Stopped" | "Failed";
export type LifecyclePhase = "idle" | "configuration" | "runtime" | "worker" | "readiness" | "workspace" | "stopping" | "failed";

export interface LifecycleFailure {
  code: string;
  summary: string;
  retryable: boolean;
  effectOccurred: boolean;
  correlationId: string;
  detail?: string;
}

export interface LifecycleStatus {
  state: LifecycleState;
  phase: LifecyclePhase;
  generationId?: string;
  workspaceUrl?: string;
  error?: LifecycleFailure;
  canRetry: boolean;
  canCancel: boolean;
  correlationId?: string;
}

export interface LifecycleViewModel {
  label: string;
  message: string;
  detail: string;
  tone: "progress" | "ready" | "failed" | "quiet";
  showCancel: boolean;
  showRetry: boolean;
  workspace: string;
}

export type LifecycleTranslator = (key: string, fallback: string) => string;

const phaseLabels: Record<LifecyclePhase, string> = {
  idle: "Idle",
  configuration: "Preparing",
  runtime: "Checking DSH",
  worker: "Starting DSH",
  readiness: "Checking readiness",
  workspace: "Workspace ready",
  stopping: "Stopping",
  failed: "Needs attention"
};

const phaseLabelKeys: Record<LifecyclePhase, string> = {
  idle: "status.idle",
  configuration: "status.preparing",
  runtime: "status.checkingDsh",
  worker: "status.startingDsh",
  readiness: "status.checkingReadiness",
  workspace: "status.workspaceReady",
  stopping: "status.stopping",
  failed: "status.needsAttention"
};

export function viewModel(status: LifecycleStatus, translate?: LifecycleTranslator): LifecycleViewModel {
	const copy = (key: string, fallback: string) => translate?.(key, fallback) ?? fallback;
	const failure = status.error;
	if (status.state === "Failed") {
		return {
			label: copy("status.failed", "Failed"),
			message: failure?.summary ?? copy("status.failureFallback", "Work could not start the local workspace."),
			detail: failure?.detail ?? failure?.code ?? "UNKNOWN_FAILURE",
			tone: "failed",
			showCancel: false,
			showRetry: status.canRetry,
			workspace: copy("status.noWorkspace", "No workspace is available.")
		};
	}
	if (status.state === "Ready") {
		return {
			label: copy("status.ready", "Ready"),
			message: copy("status.readyMessage", "The DSH workspace is ready."),
			detail: copy("status.openingWorkspace", "Opening the DSH workspace."),
			tone: "ready",
			showCancel: true,
			showRetry: false,
			workspace: status.workspaceUrl ?? copy("status.loopbackOrigin", "Loopback origin accepted.")
		};
	}
	if (status.state === "Stopping") {
		return {
			label: copy("status.stopping", "Stopping"),
			message: copy("status.stoppingMessage", "Stopping DSH."),
			detail: copy("status.closingWorkspace", "Closing the workspace."),
			tone: "progress",
			showCancel: false,
			showRetry: false,
			workspace: copy("status.workspaceClosing", "Workspace is closing.")
		};
	}
	if (status.state === "Stopped") {
		return {
			label: copy("status.stopped", "Stopped"),
			message: copy("status.stoppedMessage", "The DSH workspace is stopped."),
			detail: copy("status.startAgain", "Start again when you are ready."),
			tone: "quiet",
			showCancel: false,
			showRetry: true,
			workspace: copy("status.noActiveWorkspace", "No active workspace.")
		};
	}
	return {
		label: copy(phaseLabelKeys[status.phase], phaseLabels[status.phase]),
		message: copy("status.startupMessage", "Work is starting your DSH workspace."),
		detail: copy("status.waitingDsh", "Waiting for DSH to respond."),
		tone: "progress",
		showCancel: status.canCancel,
		showRetry: false,
		workspace: copy("status.waitingWorkspace", "Waiting for the DSH workspace.")
	};
}
