import {mountSettingSwitches} from "./ui/setting-switch";

import {mountHost} from "./startup";

import {applyLocale, defaultLocale, mountLocale} from "./i18n";
import {mountManager} from "./manager";
import {mountPetOverlay} from "./pet_overlay";
import {mountTheme} from "./theme";

const surface = new URLSearchParams(window.location.search).get("surface");
document.documentElement.dataset.surface = surface ?? "host";

if (surface === "settings") mountSettingSwitches(document);

applyLocale(defaultLocale);
mountLocale();
document.title = surface === "settings" ? "设置" : surface === "pet" ? "dsh-work Pet" : "dsh-work";

if (surface === "settings") {
  document.getElementById("host-surface")?.setAttribute("hidden", "true");
  document.getElementById("pet-surface")?.setAttribute("hidden", "true");
  document.getElementById("manager-surface")?.removeAttribute("hidden");
  mountTheme("system");
  mountManager();
} else if (surface === "pet") {
  document.getElementById("host-surface")?.setAttribute("hidden", "true");
  document.getElementById("manager-surface")?.setAttribute("hidden", "true");
  document.getElementById("pet-surface")?.removeAttribute("hidden");
  mountPetOverlay();
} else {
  mountTheme("system");
  mountHost();
}
