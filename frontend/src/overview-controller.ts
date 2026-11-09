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
import {buildDashboardData, targetOf} from "./overview-data";

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
  privacy: "profiles",
};

const environment = (action: "switch" | "save" | "restore") => {
  window.dispatchEvent(new CustomEvent("dsh-work:environment", {detail: action}));
};
const settled = <T>(result: PromiseSettledResult<T>) => (result.status === "fulfilled" ? result.value : undefined);

export interface OverviewController {
  /** Collects while the overview is on screen; nothing is polled while it is hidden. */
  setVisible(visible: boolean): void;
  refresh(): Promise<void>;
  destroy(): void;
}

/** Host-owned overview. It reads Host services directly, so it works whether or not DSH is running. */
export function mountOverview(container: HTMLElement, navigation: {openSession(id: string): void}): OverviewController {
  let period: DashboardPeriod = "today";
  let visible = false;
  let disposed = false;
  let busy = false;
  let queued = false;
  let timer: number | undefined;
  let manager: ManagerSnapshot | undefined;
  // ListPlugins takes the manager's exclusive operation gate, so it runs at most once a minute,
  // only for a Ready current context, and backs off after a failure.
  let plugins: {key: string; at: number; items: PluginInfo[]} | undefined;
  let pluginsLoading = false;
  let pluginsRetryAt = 0;

  const dashboard = mountDashboard(container, {
    onSwitchEnvironment: () => environment("switch"),
    onSaveRestorePoint: () => environment("save"),
    onRestore: () => environment("restore"),
    onDisablePluginAndRetry: async (id) => {
      if (!window.confirm(t("fault.confirmDisable", {plugin: id}))) return;
      await HostService.DisableFaultPlugin(id);
      await HostService.Start();
      await refresh();
    },
    onUpgradePlugins: async () => {
      const target = targetOf(manager);
      if (!target) return;
      const list = await ManagerService.ListPlugins({target: {profile: target.profile}});
      const packages = (list ?? []).filter((p) => p.updateCheck === "available" && !p.incompatible).map((p) => p.package);
      if (packages.length) await ManagerService.UpgradePlugins({target: {profile: target.profile}, packages});
      else await ShellService.OpenSettings("plugins");
      plugins = undefined;
      await refresh();
    },
    onOpenSession: (id) => navigation.openSession(id),
    onSettings: (section) => ShellService.OpenSettings(settingsSection[section]),
    onPeriodChange: async (next) => {
      period = next;
      await refresh();
    },
  });

  const profileKey = () => {
    const target = targetOf(manager);
    return target ? `${target.profile.dataDirectoryId}/${target.profile.name}` : "";
  };

  function loadPlugins(ready: boolean) {
    const target = targetOf(manager);
    if (!target || !ready || !manager?.current || pluginsLoading || Date.now() < pluginsRetryAt) return;
    const key = profileKey();
    if (plugins?.key === key && Date.now() - plugins.at < PLUGIN_TTL_MS) return;
    pluginsLoading = true;
    ManagerService.ListPlugins({target: {profile: target.profile}})
      .then((items) => {
        plugins = {key, at: Date.now(), items: items ?? []};
        if (!disposed && visible) void refresh();
      })
      .catch(() => {
        pluginsRetryAt = Date.now() + PLUGIN_TTL_MS;
      })
      .finally(() => {
        pluginsLoading = false;
      });
  }

  async function refresh() {
    if (disposed || !visible) return;
    if (busy) {
      queued = true;
      return;
    }
    busy = true;
    const selected = period;
    const locale = currentLocale();
    try {
      const [host, managerResult, source, automation, preferences, update, problems, pet] = await Promise.allSettled([
        HostService.GetWorkspaceStatus(),
        ManagerService.GetSnapshot(),
        OverviewService.GetSnapshot(selected, locale, -new Date().getTimezoneOffset() * 60),
        AutomationService.GetAutomation(),
        SettingsService.GetSettings(),
        HostService.GetUpdateState(),
        DiagnosticsService.GetProblems(),
        ShellService.GetPet(),
      ]);
      // A period or language change while collecting starts a fresh collection instead.
      if (disposed || selected !== period || locale !== currentLocale()) return;
      manager = settled(managerResult);
      loadPlugins(settled(host)?.state === "Ready");
      const key = profileKey();
      dashboard.update(
        buildDashboardData({
          period: selected,
          locale,
          now: new Date(),
          host: settled(host),
          manager,
          source: settled(source),
          automation: settled(automation),
          preferences: settled(preferences),
          update: settled(update),
          problems: settled(problems),
          pet: settled(pet),
          plugins: plugins?.key === key ? plugins.items : undefined,
        }),
      );
    } finally {
      busy = false;
      if (queued) {
        queued = false;
        void refresh();
      }
    }
  }

  const unsubscribe = [
    Events.On("lifecycle", () => void refresh()),
    Events.On("automation", () => void refresh()),
    Events.On("settings", () => void refresh()),
    subscribeLocale(() => void refresh()),
  ];

  return {
    setVisible(next) {
      if (next === visible || disposed) return;
      visible = next;
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
      window.clearInterval(timer);
      unsubscribe.forEach((off) => off());
      dashboard.destroy();
    },
  };
}
