import type {BrowserProfilesSnapshot} from "../bindings/github.com/local/dsh-work/internal/automation";

export type BrowserSetupStage = "install" | "confirm" | "choose" | "offline" | "conflict" | "update" | "connected";
// Presentation only: identities, connection state and selection all come from Go.
export function browserSetupStage(profiles: BrowserProfilesSnapshot | undefined, now = Date.now()): BrowserSetupStage {
 if (profiles?.pending?.some(candidate => Date.parse(candidate.expires) > now)) return "confirm";
 const bindings = profiles?.bindings ?? [];
 if (!bindings.length) return "install";
 const selected = bindings.find(binding => binding.id === profiles?.selected);
 if (!selected) return "choose";
 if(selected.status==="connected"&&selected.expectedSource&&selected.loadedSource!==selected.expectedSource)return "update";
 return selected.status === "connected" ? "connected" : selected.status === "conflict" ? "conflict" : "offline";
}
