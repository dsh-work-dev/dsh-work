import {PetSettingsService} from "../bindings/github.com/local/dsh-work/internal/app";
import {mountPetActivity} from "./pet_activity";

const refreshIntervalMs = 96;

export function mountPetOverlay() {
	const updateActivity = mountPetActivity();
  const image = document.getElementById("pet-overlay-image") as HTMLImageElement;
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  let timer: number | undefined;
  let refreshInFlight = false;

  function syncReducedMotion() {
    void PetSettingsService.SetPetReducedMotion(reducedMotion.matches).catch(() => undefined);
  }

  async function refresh() {
    if (refreshInFlight) {
      return;
    }
    refreshInFlight = true;
    try {
      const state = await PetSettingsService.GetPetOverlay();
	  updateActivity(state.activity);
      const dataUrl = state.dataUrl;
      const visible = state.runtime?.effectiveVisibility === "visible" && !!dataUrl;
      if (visible) {
        if (image.src !== dataUrl) {
          image.src = dataUrl;
        }
        image.hidden = false;
      } else {
        image.hidden = true;
        image.removeAttribute("src");
      }
    } catch (error) {
      image.hidden = true;
      console.error("Could not render the desktop pet", error);
    } finally {
      refreshInFlight = false;
    }
  }

  function stop() {
    if (timer !== undefined) {
      window.clearInterval(timer);
      timer = undefined;
    }
  }

  function start() {
    if (timer === undefined && document.visibilityState === "visible") {
      timer = window.setInterval(() => void refresh(), refreshIntervalMs);
    }
    void refresh();
  }

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      start();
    } else {
      stop();
    }
  });
  reducedMotion.addEventListener("change", syncReducedMotion);
  window.addEventListener("unload", stop);
  window.addEventListener("unload", () => reducedMotion.removeEventListener("change", syncReducedMotion));
  syncReducedMotion();
  start();
}
