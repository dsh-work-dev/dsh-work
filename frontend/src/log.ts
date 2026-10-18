export type LogLevel = "debug" | "info" | "warn" | "error";
export type LogModule = "frontend" | "shell" | "settings" | "plugin";
export interface LogEntry { level: LogLevel; module: LogModule; message: string; detail?: string; code?: string }

export interface FrontendLogOptions {
  write: (entries: LogEntry[]) => Promise<unknown>;
  now?: () => number;
  schedule?: (callback: () => void, delayMs: number) => unknown;
  cancel?: (handle: unknown) => void;
}

export interface FrontendLog {
  debug(module: LogModule, message: string, detail?: string): void;
  info(module: LogModule, message: string, detail?: string): void;
  warn(module: LogModule, message: string, detail?: string): void;
  error(module: LogModule, message: string, detail?: string, code?: string): void;
  flush(): Promise<void>;
}

const maxEntriesPerMinute = 50;
const minuteMs = 60_000;
const flushDelayMs = 2_000;

export function createFrontendLog(options: FrontendLogOptions): FrontendLog {
  const now = options.now ?? Date.now;
  const schedule = options.schedule ?? ((callback, delayMs) => window.setTimeout(callback, delayMs));
  const cancel = options.cancel ?? ((handle) => window.clearTimeout(handle as number));

  let windowStartedAt = now();
  let accepted = 0;
  let dropped = 0;
  let queue: LogEntry[] = [];
  let timerHandle: unknown;
  let timerPending = false;

  const flush = async (): Promise<void> => {
    if (timerPending) {
      cancel(timerHandle);
      timerPending = false;
      timerHandle = undefined;
    }
    const batch = queue;
    queue = [];
    if (batch.length === 0) return;
    try {
      await options.write(batch);
    } catch {
      // A failed log write must not produce another log entry.
    }
  };

  const scheduleFlush = (): void => {
    if (timerPending) return;
    timerPending = true;
    timerHandle = schedule(() => { void flush(); }, flushDelayMs);
  };

  const record = (level: LogLevel, module: LogModule, message: string, detail?: string, code?: string): void => {
    const time = now();
    if (time - windowStartedAt > minuteMs) {
      windowStartedAt = time;
      accepted = 0;
      if (dropped > 0) {
        queue.push({
          level: "warn",
          module: "frontend",
          message: "frontend log entries dropped",
          detail: String(dropped)
        });
        accepted++;
        dropped = 0;
      }
    }

    if (accepted >= maxEntriesPerMinute) {
      dropped++;
      return;
    }

    accepted++;
    const entry: LogEntry = {level, module, message};
    if (detail !== undefined) entry.detail = detail;
    if (code !== undefined) entry.code = code;
    queue.push(entry);

    if (level === "error") {
      void flush();
    } else {
      scheduleFlush();
    }
  };

  return {
    debug: (module, message, detail) => record("debug", module, message, detail),
    info: (module, message, detail) => record("info", module, message, detail),
    warn: (module, message, detail) => record("warn", module, message, detail),
    error: (module, message, detail, code) => record("error", module, message, detail, code),
    flush
  };
}

export function describeError(value: unknown): string {
  const description = value instanceof Error
    ? value.name + ": " + value.message + (value.stack ? "\n" + value.stack : "")
    : String(value);
  return description.slice(0, 4000);
}

export function installGlobalErrorLogging(log: FrontendLog, target: Window = window): void {
  target.addEventListener("error", (event) => {
    log.error(
      "frontend",
      "uncaught error",
      describeError(event.error ?? event.message) + (event.filename ? "\n" + event.filename + ":" + event.lineno + ":" + event.colno : ""),
      "UI_SCRIPT_ERROR"
    );
  });
  target.addEventListener("unhandledrejection", (event) => {
    log.error("frontend", "unhandled promise rejection", describeError(event.reason), "UI_SCRIPT_ERROR");
  });
  target.addEventListener("pagehide", () => { void log.flush(); });
}
