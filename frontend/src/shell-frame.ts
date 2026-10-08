import {dshMenuCommands, type DshCommand} from "./shell-menu-model";

export type FrameMessage =
  | {type: "catalog"; commands: Map<string, DshCommand>}
  | {type: "menu-key"}
  | {type: "surface"; background: string; color: string};

const strings = (value: unknown): value is string[] => Array.isArray(value) && value.every(item => typeof item === "string");

/** Validate a message from the DSH frame; the caller has already checked source and origin. */
export function parseFrameMessage(data: unknown): FrameMessage | null {
  if (!data || typeof data !== "object") return null;
  const message = data as Record<string, unknown>;
  if (message.type === "dsh-work/catalog" && Array.isArray(message.commands)) {
    const commands = new Map<string, DshCommand>();
    for (const raw of message.commands as Record<string, unknown>[]) {
      if (typeof raw?.id !== "string" || !dshMenuCommands.includes(raw.id) || !strings(raw.keys) || typeof raw.bound !== "boolean") continue;
      commands.set(raw.id, {id: raw.id, keys: raw.keys, bound: raw.bound});
    }
    return {type: "catalog", commands};
  }
  if (message.type === "dsh-work/menu-key" && (message.key === "Alt" || message.key === "F10")) return {type: "menu-key"};
  if (message.type === "dsh-work/surface" && typeof message.background === "string" && typeof message.color === "string") {
    return {type: "surface", background: message.background, color: message.color};
  }
  return null;
}
