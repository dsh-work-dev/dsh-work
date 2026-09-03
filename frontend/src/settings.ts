import {Events} from "@wailsio/runtime";

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

  function render(values: Values) {
    toggle.checked = values.closeToTray;
    localeSelect.value = normalizeLocale(values.locale);
    savedLocale = normalizeLocale(values.locale);
    status.textContent = "";
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
      render(await SettingsService.SetCloseToTray(toggle.checked));
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
      render(values);
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

type NotificationPreferenceKey = "enabled" | "completed" | "interactionRequired" | "errors" | "lifecycle";

const notificationPreferenceIds: Record<NotificationPreferenceKey, string> = {
  enabled: "settings-notifications-enabled",
  completed: "settings-notifications-completed",
  interactionRequired: "settings-notifications-interaction-required",
  errors: "settings-notifications-errors",
  lifecycle: "settings-notifications-lifecycle"
};

export function mountNotifications(setFeedback: Feedback) {
  const status = document.getElementById("settings-notifications-status") as HTMLParagraphElement;
  const toggles = Object.fromEntries(
    (Object.entries(notificationPreferenceIds) as Array<[NotificationPreferenceKey, string]>).map(([key, id]) => [
      key,
      document.getElementById(id) as HTMLInputElement
    ])
  ) as Record<NotificationPreferenceKey, HTMLInputElement>;

  function render(values: Values) {
    const preferences = values.notifications;
    toggles.enabled.checked = preferences.enabled;
    toggles.completed.checked = preferences.completed;
    toggles.interactionRequired.checked = preferences.interactionRequired;
    toggles.errors.checked = preferences.errors;
    toggles.lifecycle.checked = preferences.lifecycle;
    status.textContent = "";
  }

  Events.On("notification-failure", () => {
    const message = t("error.notificationsUnavailable");
    status.textContent = message;
    setFeedback(message, "error");
  });

  async function refresh(): Promise<boolean> {
    try {
      render(await SettingsService.GetSettings());
      return true;
    } catch (error) {
      const message = settingsErrorMessage(error, t("error.loadSettings"));
      status.textContent = message;
      setFeedback(message, "error");
      console.error("Could not read Work notification settings", error);
      return false;
    }
  }

  for (const [key, toggle] of Object.entries(toggles) as Array<[NotificationPreferenceKey, HTMLInputElement]>) {
    toggle.addEventListener("change", () => void (async () => {
      const previous = !toggle.checked;
      toggle.disabled = true;
      try {
        render(await SettingsService.SetNotificationPreference(key, toggle.checked));
        setFeedback("");
      } catch (error) {
        toggle.checked = previous;
        const message = settingsErrorMessage(error, t("error.saveSettings"));
        status.textContent = message;
        setFeedback(message, "error");
        console.error("Could not update Work notification settings", error);
      } finally {
        toggle.disabled = false;
      }
    })());
  }

  return {refresh};
}
