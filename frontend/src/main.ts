import {mountSettingSwitches} from "./ui/setting-switch";
import {hydrateIcons} from "./ui/icons";

import {mountHost} from "./startup";

import {applyLocale, defaultLocale, mountLocale} from "./i18n";
import {mountManager} from "./manager";
import {mountPetOverlay} from "./pet_overlay";
import {mountAppearance} from "./theme";
import {HostService, SettingsService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";

const surface = new URLSearchParams(window.location.search).get("surface");
document.documentElement.dataset.surface = surface ?? "host";

if (surface === "settings") mountSettingSwitches(document);

hydrateIcons(document);
applyLocale(defaultLocale);
mountLocale();
document.title = surface === "settings" ? "设置" : surface === "pet" ? "dsh-work Pet" : "dsh-work";

if (surface === "settings") {
  document.getElementById("host-surface")?.setAttribute("hidden", "true");
  document.getElementById("pet-surface")?.setAttribute("hidden", "true");
  document.getElementById("manager-surface")?.removeAttribute("hidden");
  mountAppearance(async () => (await SettingsService.GetSettings()).appearance);
  mountManager();
} else if (surface === "pet") {
  document.getElementById("host-surface")?.setAttribute("hidden", "true");
  document.getElementById("manager-surface")?.setAttribute("hidden", "true");
  document.getElementById("pet-surface")?.removeAttribute("hidden");
  mountPetOverlay();
} else {
  mountAppearance(() => HostService.GetAppearance());
  mountHost();
}
