export type LifecycleState = "Starting" | "Ready" | "Stopping" | "Stopped" | "Failed";
export type LifecyclePhase = "idle" | "configuration" | "runtime" | "node" | "profile" | "worker" | "readiness" | "checkpoint" | "workspace" | "stopping" | "failed";

export interface LifecycleFailure {
  code: string;
  summary: string;
  retryable: boolean;
  effectOccurred: boolean;
  correlationId: string;
  detail?: string;
}

export interface WorkspaceContext {
  generationId: string;
  state: "selected" | "selection-required";
  id?: string;
  path?: string;
  title?: string;
}

export interface RuntimePreparation {
  state: "idle" | "resolving-toolchain" | "acquiring-node" | "acquiring-dsh" | "verifying" | "installed" | "cancelled" | "failed";
  operation: "none" | "detect-node" | "detect-pnpm" | "detect-npm" | "download-node" | "install-dsh" | "verify" | "cleanup";
  targetVersion?: string;
  toolchain?: string;
  source: "none" | "local" | "official" | "mirror";
  receivedBytes?: number;
  totalBytes?: number;
  hasTotal: boolean;
  canCancel: boolean;
  error?: LifecycleFailure;
  operationId?: string;
  artifactKind?: "node" | "dsh" | "plugin";
  attempt?: number;
  result?: {
    succeeded: boolean;
    route?: "official" | "mirror" | "local";
    failure?: {
      kind: string;
      retryable: boolean;
    };
  };
}

export interface LifecycleStatus {
  launchSelection?: {runtimeId: string; nodeId: string; profileName?: string; runtimeVersion?: string; runtimePath?: string; nodeVersion?: string; nodePath?: string; dataDirectoryPath?: string};
  state: LifecycleState;
  phase: LifecyclePhase;
  generationId?: string;
  workspaceUrl?: string;
  workspace?: WorkspaceContext;
  runtimePreparation?: RuntimePreparation;
  error?: LifecycleFailure;
  pluginFault?: {plugins: string[]};
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
  node: "Checking Node.js",
  profile: "Checking profile and plugins",
  worker: "Starting DSH",
  readiness: "Checking readiness",
  checkpoint: "Saving recovery environment",
  workspace: "Workspace ready",
  stopping: "Stopping",
  failed: "Needs attention"
};

const phaseLabelKeys: Record<LifecyclePhase, string> = {
  idle: "status.idle",
  configuration: "status.preparing",
  runtime: "status.checkingDsh",
  node: "startup.checkNode",
  profile: "startup.checkProfile",
  worker: "status.startingDsh",
  readiness: "status.checkingReadiness",
  checkpoint: "status.savingEnvironment",
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
			message: failure?.summary ?? copy("status.failureFallback", "dsh-work could not start the local workspace."),
			detail: failure?.detail ?? copy("status.failureFallback", "dsh-work could not start the local workspace."),
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
			workspace: status.workspace?.path ?? copy("status.workspaceReady", "Workspace ready.")
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
	const preparation = status.runtimePreparation;
	const preparationDetail = preparation
	  ? preparation.state === "acquiring-node"
	    ? copy("status.downloadingNode", "Downloading the Node.js runtime.")
	    : preparation.state === "acquiring-dsh"
	      ? copy("status.downloadingDsh", "Downloading the DSH runtime.")
	      : preparation.state === "verifying"
	        ? copy("status.verifyingRuntime", "Verifying the DSH runtime.")
	        : preparation.state === "installed"
	          ? copy("status.runtimePrepared", "DSH runtime prepared.")
	          : preparation.state === "cancelled"
	            ? copy("status.runtimePreparationCancelled", "Runtime preparation cancelled.")
	            : preparation.operation === "detect-pnpm"
	              ? copy("status.detectingPnpm", "Checking for pnpm.")
	              : preparation.operation === "detect-npm"
	                ? copy("status.detectingNpm", "Checking for npm.")
	                : copy("status.preparingRuntime", "Preparing the DSH runtime.")
	  : copy(phaseLabelKeys[status.phase], phaseLabels[status.phase]);
	return {
		label: copy(phaseLabelKeys[status.phase], phaseLabels[status.phase]),
		message: copy("status.startupMessage", "dsh-work is starting your DSH workspace."),
		detail: preparationDetail,
		tone: "progress",
		showCancel: status.canCancel,
		showRetry: false,
		workspace: copy("status.waitingWorkspace", "Waiting for the DSH workspace.")
	};
}

// The persisted selection is the last healthy context, not a switch candidate.
export function startupProfileName(status: LifecycleStatus, configured?: string): string | undefined {
  return status.launchSelection?.profileName || configured;
}

export function runningLaunchSelection(status: LifecycleStatus) {
  return ["Starting", "Stopping", "Ready"].includes(status.state) ? status.launchSelection : undefined;
}
