export const builtinComponentIds = ["shell", "account", "activity"] as const;
export type BuiltinComponentId = typeof builtinComponentIds[number];
export type BuiltinComponentState = "inactive" | "loading" | "loaded";

const definitions: readonly {id: BuiltinComponentId; titleKey: string; descriptionKey: string}[] = [
  {id: "shell", titleKey: "components.shell", descriptionKey: "components.shellDescription"},
  {id: "account", titleKey: "components.account", descriptionKey: "components.accountDescription"},
  {id: "activity", titleKey: "components.activity", descriptionKey: "components.activityDescription"},
];
const stateKeys: Record<BuiltinComponentState, string> = {
  inactive: "components.status.inactive",
  loading: "components.status.loading",
  loaded: "components.status.loaded",
};
const validStates: readonly string[] = ["inactive", "loading", "loaded"];

export interface BuiltinComponentView {
  id: BuiltinComponentId;
  titleKey: string;
  descriptionKey: string;
  state: BuiltinComponentState;
  stateKey: string;
}

/** Return every application-owned component in stable order, ignoring unknown host data. */
export function buildBuiltinComponentViews(value: unknown): BuiltinComponentView[] {
  const states = new Map<BuiltinComponentId, BuiltinComponentState>();
  if (Array.isArray(value)) {
    for (const item of value as Record<string, unknown>[]) {
      if (typeof item?.id !== "string" || !builtinComponentIds.includes(item.id as BuiltinComponentId)) continue;
      if (typeof item.state !== "string" || !validStates.includes(item.state)) continue;
      states.set(item.id as BuiltinComponentId, item.state as BuiltinComponentState);
    }
  }
  return definitions.map(definition => {
    const state = states.get(definition.id) ?? "inactive";
    return {...definition, state, stateKey: stateKeys[state]};
  });
}
