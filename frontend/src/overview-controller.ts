import {Events} from "@wailsio/runtime";
import {
  AutomationService,
  DiagnosticsService,
  HostService,
  ManagerService,
  OverviewService,
  SettingsService,
  ShellService,
} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Snapshot as ManagerSnapshot, PluginInfo} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import {mountDashboard} from "./dashboard";
import type {DashboardPeriod, DashboardSettingsSection} from "./dashboard-model";
import {currentLocale, subscribeLocale, t} from "./i18n";
import {buildDashboardData, contextKey, availablePluginUpgrades} from "./overview-data";
import type {OverviewSources} from "./overview-data";

const REFRESH_MS = 15_000;
const PLUGIN_TTL_MS = 60_000;

const settingsSection: Record<DashboardSettingsSection, string> = {
  runtime: "runtimes",
  computer: "computer",
  pets: "pets",
  notifications: "notifications",
  plugins: "plugins",
  diagnostics: "diagnostics",
  updates: "about",
  storage: "data-directories",
  browser: "browser",
};

export type EnvironmentAction = {action: "switch"} | {action: "save"} | {action: "restore"; pointId: string};
const environment = (detail: EnvironmentAction) => {
  window.dispatchEvent(new CustomEvent("dsh-work:environment", {detail}));
};
const settled = <T>(result: PromiseSettledResult<T>) => (result.status === "fulfilled" ? result.value : undefined);

export interface OverviewController {
  /** Collects while the overview is on screen; nothing is polled while it is hidden. */
  setVisible(visible: boolean): void;
  refresh(): Promise<void>;
  destroy(): void;
}

/** Host-owned overview. It reads Host services directly, so it works whether or not DSH is running. */
export function mountOverview(container: HTMLElement, navigation: {openSession(id: string): Promise<void>}): OverviewController {
  let period: DashboardPeriod = "today";
  let visible = false;
  let disposed = false;
  let active: ReturnType<typeof OverviewService.GetSnapshot> | undefined;
  let queued = false;
  let timer: number | undefined;
  let manager: ManagerSnapshot | undefined;
  let host: OverviewSources["host"];
  // ListPlugins takes the manager's exclusive operation gate, so it runs at most once a minute,
  // only for a Ready current context, and backs off after a failure.
  let plugins: {key: string; at: number; items: PluginInfo[]} | undefined;
  let pluginsLoading: string | undefined;
  let pluginsRetry: {key: string; at: number} | undefined;

  const dashboard = mountDashboard(container, {
    onSwitchEnvironment: () => environment({action: "switch"}),
    onSaveRestorePoint: () => environment({action: "save"}),
    onRestore: (pointId) => environment({action: "restore", pointId}),
    onDisablePluginAndRetry: async (id) => {
      const generation = host?.state === "Failed" ? host.generationId : undefined;
      if (!generation) throw new Error(t("overview.plugins.changed"));
      if (!window.confirm(t("fault.confirmDisable", {plugin: id}))) return;
      try {
        await HostService.DisableFaultPluginAndRetry(generation, id);
      } finally {
        await refresh();
      }
    },
    onUpgradePlugins: async () => {
      const target = manager?.current;
      const generation = host?.generationId;
      const packages = availablePluginUpgrades(currentPlugins());
      if (!target || !generation || !packages.length) {
        await refresh();
        throw new Error(t("overview.plugins.changed"));
      }
      if (!window.confirm(t("overview.plugins.confirmUpgrade", {count: packages.length}))) return;
      try {
        await ManagerService.UpgradePlugins({target: {profile: target.profile}, packages, expectedGeneration: generation});
      } finally {
        plugins = undefined;
        await refresh();
      }
    },
    onOpenSession: (id) => navigation.openSession(id),
    onSettings: (section) => ShellService.OpenSettings(settingsSection[section]),
    onPeriodChange: async (next) => {
      period = next;
      cancelRefresh();
      await refresh();
    },
  });

  const pluginKey = () => {
    const selection = contextKey(manager?.current);
    return host?.state === "Ready" && host.generationId && selection
      ? JSON.stringify([host.generationId, selection])
      : "";
  };

  function currentPlugins() {
    return plugins?.key === pluginKey() && Date.now() - plugins.at < PLUGIN_TTL_MS ? plugins.items : undefined;
  }

  function loadPlugins() {
    const target = manager?.current;
    const key = pluginKey();
    if (!target || !key || pluginsLoading || currentPlugins() || (pluginsRetry?.key === key && Date.now() < pluginsRetry.at)) return;
    pluginsLoading = key;
    ManagerService.ListPlugins({target: {profile: target.profile}})
      .then((items) => {
        if (disposed || pluginKey() !== key) return;
        plugins = {key, at: Date.now(), items: items ?? []};
      })
      .catch(() => {
        pluginsRetry = {key, at: Date.now() + PLUGIN_TTL_MS};
      })
      .finally(() => {
        pluginsLoading = undefined;
        if (visible && !disposed && (plugins?.key === key || pluginKey() !== key)) void refresh();
      });
  }

  function cancelRefresh() {
    const previous = active;
    active = undefined;
    queued = false;
    // Superseded reads no longer own the view, even if native cleanup is still running.
    void previous?.cancel();
  }

  async function refresh() {
    if (disposed || !visible) return;
    if (active) {
      queued = true;
      return;
    }
    const selected = period;
    const locale = currentLocale();
    const read = OverviewService.GetSnapshot(selected, locale, -new Date().getTimezoneOffset() * 60);
    active = read;
    try {
      const [hostResult, managerResult, source, automation, preferences, update, problems, pet] = await Promise.allSettled([
        HostService.GetWorkspaceStatus(),
        ManagerService.GetSnapshot(),
        read,
        AutomationService.GetAutomation(),
        SettingsService.GetSettings(),
        HostService.GetUpdateState(),
        DiagnosticsService.GetProblems(),
        ShellService.GetPet(),
      ] as const);
      if (active !== read) return;
      manager = settled(managerResult);
      host = settled(hostResult);
      loadPlugins();
      dashboard.update(
        buildDashboardData({
          period: selected,
          locale,
          now: new Date(),
          host,
          manager,
          source: settled(source),
          automation: settled(automation),
          preferences: settled(preferences),
          update: settled(update),
          problems: settled(problems),
          pet: settled(pet),
          plugins: currentPlugins(),
        }),
      );
    } finally {
      if (active === read) {
        active = undefined;
        if (queued) {
          queued = false;
          void refresh();
        }
      }
    }
  }

  const unsubscribe = [
    (() => {
      const changed = () => void refresh();
      window.addEventListener("dsh-work:environment-changed", changed);
      return () => window.removeEventListener("dsh-work:environment-changed", changed);
    })(),
    Events.On("lifecycle", () => void refresh()),
    Events.On("automation", () => void refresh()),
    Events.On("settings", () => void refresh()),
    subscribeLocale(() => {
      cancelRefresh();
      void refresh();
    }),
  ];

  return {
    setVisible(next) {
      if (next === visible || disposed) return;
      visible = next;
      cancelRefresh();
      window.clearInterval(timer);
      timer = undefined;
      if (visible) {
        timer = window.setInterval(() => void refresh(), REFRESH_MS);
        void refresh();
      }
    },
    refresh,
    destroy() {
      disposed = true;
      cancelRefresh();
      window.clearInterval(timer);
      unsubscribe.forEach((off) => off());
      dashboard.destroy();
    },
  };
}
