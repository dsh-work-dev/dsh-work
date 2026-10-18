import {mountSettingSwitches} from "./ui/setting-switch";
import {hydrateIcons} from "./ui/icons";

import {mountHost} from "./startup";
import {mountShell} from "./shell";

import {applyLocale, defaultLocale, mountLocale} from "./i18n";
import {mountManager} from "./manager";
import {mountPetOverlay} from "./pet_overlay";
import {mountAppearance} from "./theme";
import {appLog} from "./app-log";
import {installGlobalErrorLogging} from "./log";
import {HostService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";

const surface = new URLSearchParams(window.location.search).get("surface");
document.documentElement.dataset.surface = surface ?? "host";

hydrateIcons(document);
applyLocale(defaultLocale);
mountLocale();
document.title = surface === "pet" ? "dsh-work Pet" : "dsh-work";

if (surface === "pet") {
  document.getElementById("host-surface")?.setAttribute("hidden", "true");
  document.getElementById("manager-surface")?.setAttribute("hidden", "true");
  document.getElementById("pet-surface")?.removeAttribute("hidden");
  mountPetOverlay();
} else {
  installGlobalErrorLogging(appLog);
  mountAppearance(() => HostService.GetAppearance());
  mountHost();
  if (new URLSearchParams(window.location.search).get("shell") === "1") {
    mountSettingSwitches(document);
    mountManager();
    mountShell();
  }
}
