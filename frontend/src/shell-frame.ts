import {builtinComponentIds, type BuiltinComponentId} from "./builtin-components";
import {dshMenuCommands, keyedWindowActions, type DshCommand, type DshCommandState, type WindowAction} from "./shell-menu-model";

export type FrameMessage =
  | {type: "catalog"; commands: Map<string, DshCommand>}
  | {type: "command-result"; id: string; handled: boolean}
  | {type: "component-ready"; id: BuiltinComponentId}
  | {type: "sidebar-state"; open: boolean}
  | {type: "window-key"; action: WindowAction}
  | {type: "export-result"; result: ExportResult}
  | {type: "menu-key"}
  | {type: "surface"; background: string; color: string};

export type ExportResult = "saved" | "cancelled" | "error";
const exportResults: readonly ExportResult[] = ["saved", "cancelled", "error"];
const commandStates: readonly DshCommandState[] = ["ready", "unbound", "unavailable"];
const strings = (value: unknown): value is string[] => Array.isArray(value) && value.every(item => typeof item === "string");

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
  if (message.type === "dsh-work/window-key" && keyedWindowActions.includes(message.action as WindowAction)) {
    return {type: "window-key", action: message.action as WindowAction};
  }
  if (message.type === "dsh-work/export-result" && exportResults.includes(message.result as ExportResult)) {
    return {type: "export-result", result: message.result as ExportResult};
  }
  if (message.type === "dsh-work/menu-key" && (message.key === "Alt" || message.key === "F10")) return {type: "menu-key"};
  if (message.type === "dsh-work/surface" && typeof message.background === "string" && typeof message.color === "string") {
    return {type: "surface", background: message.background, color: message.color};
  }
  return null;
}
