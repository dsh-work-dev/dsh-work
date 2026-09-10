import {Events} from "@wailsio/runtime";

import {SettingsService} from "../bindings/github.com/local/dsh-work/internal/app";
import type {Values} from "../bindings/github.com/local/dsh-work/internal/settings";
import {applyLocale, normalizeLocale, subscribeLocale, t} from "./i18n";
import {beginControlUpdate} from "./ui/pending-control";

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
  const rollbackToggle = document.getElementById("settings-automatic-runtime-rollback") as HTMLInputElement;
  const localeSelect = document.getElementById("settings-locale") as HTMLSelectElement;
  const status = document.getElementById("settings-close-to-tray-status") as HTMLParagraphElement;
  const controls = [toggle, rollbackToggle, localeSelect];
  controls.forEach(control => control.disabled = true);
  let savedLocale = normalizeLocale(localeSelect.value);

  const form = status.closest("section")!.querySelector<HTMLElement>(".settings-list")!;
  form.hidden = true; status.className = "manager-note"; status.textContent = t("view.loading");

  function render(values: Values) {
    form.hidden = false;
    controls.forEach(control => control.disabled = false);
    toggle.checked = values.closeToTray;
    rollbackToggle.checked = values.automaticRuntimeRollback;
    localeSelect.value = normalizeLocale(values.locale);
    savedLocale = normalizeLocale(values.locale);
    status.textContent = "";
  }

  subscribeLocale((locale) => {
    localeSelect.value = locale;
    savedLocale = locale;
  });

  const retry = document.createElement("button");
  retry.type = "button"; retry.className = "button button-secondary"; retry.textContent = t("common.retry"); retry.hidden = true;
  status.before(retry);
  subscribeLocale(() => { retry.textContent = t("common.retry"); });
  retry.addEventListener("click", () => void refresh());
  async function refresh(): Promise<boolean> {
    retry.disabled = true;
    try {
      const values = await SettingsService.GetSettings();
      applyLocale(values.locale);
      render(values);
      if (!retry.hidden) setFeedback("");
      retry.hidden = true;
      return true;
    } catch (error) {
      status.textContent = "";
      setFeedback(settingsErrorMessage(error, t("error.loadSettings")), "error");

      console.error("Could not read dsh-work settings", error);
      retry.hidden = false;
      return false;
    } finally { retry.disabled = false; }
  }

  toggle.addEventListener("change", () => void (async () => {
    const previous = !toggle.checked;
    const finishUpdate = beginControlUpdate(toggle);
    try {
      render(await SettingsService.SetCloseToTray(toggle.checked));
      setFeedback("");
    } catch (error) {
      toggle.checked = previous;
      setFeedback(settingsErrorMessage(error, t("error.saveSettings")), "error");

      console.error("Could not update dsh-work close behavior", error);
    } finally {
      finishUpdate();
    }
  })());

  rollbackToggle.addEventListener("change", () => void (async () => {
	const previous = !rollbackToggle.checked;
	const finishUpdate = beginControlUpdate(rollbackToggle);
	try {
		render(await SettingsService.SetAutomaticRuntimeRollback(rollbackToggle.checked));
		setFeedback("");
	} catch (error) {
		rollbackToggle.checked = previous;
		setFeedback(settingsErrorMessage(error, t("error.saveSettings")), "error");
	} finally {
		finishUpdate();
	}
  })());

  localeSelect.addEventListener("change", () => void (async () => {
    const previous = savedLocale;
    const finishUpdate = beginControlUpdate(localeSelect);
    try {
      const values = await SettingsService.SetLocale(normalizeLocale(localeSelect.value));
      applyLocale(values.locale);
      render(values);
      setFeedback("");
    } catch (error) {
      localeSelect.value = previous;
      setFeedback(settingsErrorMessage(error, t("error.saveSettings")), "error");
      console.error("Could not update dsh-work language", error);
    } finally {
      finishUpdate();
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

  Object.values(toggles).forEach(control => control.disabled = true);

  const form = status.closest("section")!.querySelector<HTMLElement>(".settings-list")!;
  form.hidden = true; status.className = "manager-note"; status.textContent = t("view.loading");

  function render(values: Values) {
    form.hidden = false;
    const preferences = values.notifications;
    for (const [key, control] of Object.entries(toggles)) control.disabled = key !== "enabled" && !preferences.enabled;
    toggles.enabled.checked = preferences.enabled;
    toggles.completed.checked = preferences.completed;
    toggles.interactionRequired.checked = preferences.interactionRequired;
    toggles.errors.checked = preferences.errors;
    toggles.lifecycle.checked = preferences.lifecycle;
    status.textContent = "";
  }

  Events.On("notification-failure", () => {
    const message = t("error.notificationsUnavailable");

    setFeedback(message, "error");
  });

  const retry = document.createElement("button");
  retry.type = "button"; retry.className = "button button-secondary"; retry.textContent = t("common.retry"); retry.hidden = true;
  status.before(retry);
  subscribeLocale(() => { retry.textContent = t("common.retry"); });
  retry.addEventListener("click", () => void refresh());
  async function refresh(): Promise<boolean> {
    retry.disabled = true;
    try {
      render(await SettingsService.GetSettings());
      if (!retry.hidden) setFeedback("");
      retry.hidden = true;
      return true;
    } catch (error) {
      status.textContent = "";
      const message = settingsErrorMessage(error, t("error.loadSettings"));

      setFeedback(message, "error");
      console.error("Could not read dsh-work notification settings", error);
      retry.hidden = false;
      return false;
    } finally { retry.disabled = false; }
  }

  for (const [key, toggle] of Object.entries(toggles) as Array<[NotificationPreferenceKey, HTMLInputElement]>) {
    toggle.addEventListener("change", () => void (async () => {
      const previous = !toggle.checked;
      const finishUpdate = beginControlUpdate(toggle);
      try {
        render(await SettingsService.SetNotificationPreference(key, toggle.checked));
        setFeedback("");
      } catch (error) {
        toggle.checked = previous;
        const message = settingsErrorMessage(error, t("error.saveSettings"));

        setFeedback(message, "error");
        console.error("Could not update dsh-work notification settings", error);
      } finally {
        finishUpdate();
      }
    })());
  }

  return {refresh};
}
