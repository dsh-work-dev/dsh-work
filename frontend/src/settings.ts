import {SettingsService} from "../bindings/github.com/local/work/internal/app";
import type {Values} from "../bindings/github.com/local/work/internal/settings";
import {applyLocale, normalizeLocale, subscribeLocale, t} from "./i18n";

type FeedbackTone = "neutral" | "success" | "error";
type Feedback = (message: string, tone?: FeedbackTone) => void;

function settingsErrorMessage(error: unknown, fallback: string): string {
  let message = "";
  if (error instanceof Error) {
    message = error.message;
  } else if (typeof error === "string") {
    message = error;
  } else if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string") {
    message = error.message;
  }
  message = message.replace(/[\r\n]+/g, " ").trim();
  return message.length > 0 && message.length <= 240 ? message : fallback;
}

export function mountSettings(setFeedback: Feedback) {
  const toggle = document.getElementById("settings-close-to-tray") as HTMLInputElement;
  const localeSelect = document.getElementById("settings-locale") as HTMLSelectElement;
  const status = document.getElementById("settings-close-to-tray-status") as HTMLParagraphElement;
  let savedLocale = normalizeLocale(localeSelect.value);

  function render(values: Values, announce = false) {
    toggle.checked = values.closeToTray;
    localeSelect.value = normalizeLocale(values.locale);
    savedLocale = normalizeLocale(values.locale);
    status.textContent = announce ? t("feedback.saved") : "";
  }

  subscribeLocale((locale) => {
    localeSelect.value = locale;
    savedLocale = locale;
  });

  async function refresh(): Promise<boolean> {
    try {
      const values = await SettingsService.GetSettings();
      applyLocale(values.locale);
      render(values);
      return true;
    } catch (error) {
      status.textContent = settingsErrorMessage(error, t("error.loadSettings"));
      setFeedback(status.textContent, "error");
      console.error("Could not read Work settings", error);
      return false;
    }
  }

  toggle.addEventListener("change", () => void (async () => {
    const previous = !toggle.checked;
    toggle.disabled = true;
    try {
      render(await SettingsService.SetCloseToTray(toggle.checked), true);
      setFeedback("");
    } catch (error) {
      toggle.checked = previous;
      status.textContent = settingsErrorMessage(error, t("error.saveSettings"));
      setFeedback(status.textContent, "error");
      console.error("Could not update Work close behavior", error);
    } finally {
      toggle.disabled = false;
    }
  })());

  localeSelect.addEventListener("change", () => void (async () => {
    const previous = savedLocale;
    localeSelect.disabled = true;
    try {
      const values = await SettingsService.SetLocale(normalizeLocale(localeSelect.value));
      applyLocale(values.locale);
      render(values, true);
      setFeedback("");
    } catch (error) {
      localeSelect.value = previous;
      setFeedback(settingsErrorMessage(error, t("error.saveSettings")), "error");
      console.error("Could not update Work language", error);
    } finally {
      localeSelect.disabled = false;
    }
  })());

  return {refresh};
}
