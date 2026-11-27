export interface RevealDeps {
  finish(): Promise<void>;   // splash closing animation (bounded by the controller)
  fadeOut(): Promise<void>;  // startup surface fade
  hide(): void;              // hide the startup surface
  show(): void;              // show the startup surface
  reset(): void;             // splash back to waiting
  dispose(): void;           // release the splash
  mount(): void;             // load the splash again
}

/** Sequences the startup surface's exit to DSH and its return when the Worker drops. */
export function createReveal(deps: RevealDeps) {
  let revision = 0;
  let state: "shown" | "revealing" | "hidden" = "shown";
  return {
    reveal() {
      if (state !== "shown") return;
      state = "revealing";
      const current = ++revision;
      void deps.finish().then(async () => {
        if (current !== revision) return;
        await deps.fadeOut();
        if (current !== revision) return;
        deps.hide();
        deps.dispose();
        state = "hidden";
      });
    },
    restore() {
      revision++;
      deps.show();
      if (state === "revealing") deps.reset();
      else if (state === "hidden") deps.mount();
      state = "shown";
    },
  };
}
