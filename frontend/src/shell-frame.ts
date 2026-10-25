import {builtinComponentIds, type BuiltinComponentId} from "./builtin-components";
import {dshMenuCommands, keyedWindowActions, type DshCommand, type DshCommandState, type WindowAction} from "./shell-menu-model";

export type FrameMessage =
  | {type: "catalog"; commands: Map<string, DshCommand>}
  | {type: "command-result"; id: string; handled: boolean}
  | {type: "component-ready"; id: BuiltinComponentId}
  | {type: "open-session-result"; id: string; error: string}
  | {type: "interaction"; report: InteractionReport}
  | {type: "interaction-reset"; instance: string}
  | {type: "sidebar-state"; open: boolean}
  | {type: "window-key"; action: WindowAction}
  | {type: "export-result"; result: ExportResult}
  | {type: "account-signout"; state: SignOutState}
  | {type: "menu-key"}
  | {type: "modal-mask"; active: boolean; background: string; blur: string};

export type ExportResult = "saved" | "cancelled" | "error";
export type SignOutState = "pending" | "success" | "error";
export type InteractionReport = {
  instance: string;
  sessionId: string;
  kind: string;
  key: string;
  toolName: string;
  current: string;
  visible: boolean;
};
const signOutStates: readonly SignOutState[] = ["pending", "success", "error"];
const exportResults: readonly ExportResult[] = ["saved", "cancelled", "error"];
const commandStates: readonly DshCommandState[] = ["ready", "unbound", "unavailable"];
const interactionKinds = ["", "approval", "question", "plan-review"] as const;
const strings = (value: unknown): value is string[] => Array.isArray(value) && value.every(item => typeof item === "string");
const stringLength = (value: string) => Array.from(value).length;

/** Validate a message from the DSH frame; the caller has already checked source and origin. */
export function parseFrameMessage(data: unknown): FrameMessage | null {
  if (!data || typeof data !== "object") return null;
  const message = data as Record<string, unknown>;
  if (message.type === "dsh-work/catalog" && Array.isArray(message.commands)) {
    const commands = new Map<string, DshCommand>();
    for (const raw of message.commands as Record<string, unknown>[]) {
      if (typeof raw?.id !== "string" || !dshMenuCommands.includes(raw.id) || !strings(raw.keys) || !commandStates.includes(raw.state as DshCommandState)) continue;
      commands.set(raw.id, {id: raw.id, keys: raw.keys, state: raw.state as DshCommandState});
    }
    return {type: "catalog", commands};
  }
  if (message.type === "dsh-work/command-result" && typeof message.id === "string" && dshMenuCommands.includes(message.id) && typeof message.handled === "boolean") {
    return {type: "command-result", id: message.id, handled: message.handled};
  }
  if (message.type === "dsh-work/sidebar-state" && typeof message.open === "boolean") return {type: "sidebar-state", open: message.open};
  if (message.type === "dsh-work/component-ready" && typeof message.id === "string" && builtinComponentIds.includes(message.id as BuiltinComponentId)) return {type: "component-ready", id: message.id as BuiltinComponentId};
  if (message.type === "dsh-work/open-session-result" && typeof message.id === "string" && message.id.length > 0 && message.id.length <= 256 && typeof message.error === "string" && message.error.length <= 160) {
    return {type: "open-session-result", id: message.id, error: message.error};
  }
  if (message.version === 1 && message.type === "dsh-work/interaction" &&
      typeof message.instance === "string" && message.instance.length > 0 && stringLength(message.instance) <= 256 &&
      typeof message.sessionId === "string" && stringLength(message.sessionId) <= 256 &&
      typeof message.kind === "string" && interactionKinds.includes(message.kind as typeof interactionKinds[number]) &&
      typeof message.key === "string" && stringLength(message.key) <= 256 && (message.kind === "" || message.key !== "") &&
      typeof message.toolName === "string" && stringLength(message.toolName) <= 120 &&
      typeof message.current === "string" && stringLength(message.current) <= 256 && typeof message.visible === "boolean") {
    return {type: "interaction", report: {
      instance: message.instance,
      sessionId: message.sessionId,
      kind: message.kind,
      key: message.key,
      toolName: message.toolName,
      current: message.current,
      visible: message.visible,
    }};
  }
  if (message.version === 1 && message.type === "dsh-work/interaction-reset" &&
      typeof message.instance === "string" && message.instance.length > 0 && stringLength(message.instance) <= 256) {
    return {type: "interaction-reset", instance: message.instance};
  }
  if (message.type === "dsh-work/window-key" && keyedWindowActions.includes(message.action as WindowAction)) {
    return {type: "window-key", action: message.action as WindowAction};
  }
  if (message.type === "dsh-work/export-result" && exportResults.includes(message.result as ExportResult)) {
    return {type: "export-result", result: message.result as ExportResult};
  }
  if (message.type === "dsh-work/account-signout" && signOutStates.includes(message.state as SignOutState)) {
    return {type: "account-signout", state: message.state as SignOutState};
  }
  if (message.type === "dsh-work/menu-key" && (message.key === "Alt" || message.key === "F10")) return {type: "menu-key"};
  if (message.type === "dsh-work/modal-mask" && typeof message.active === "boolean" && typeof message.background === "string" && typeof message.blur === "string") {
    return {type: "modal-mask", active: message.active, background: message.background, blur: message.blur};
  }
  return null;
}
