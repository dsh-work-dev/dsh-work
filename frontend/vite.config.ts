import { defineConfig } from "vite";
import {readFileSync} from "node:fs";
import wails from "@wailsio/runtime/plugins/vite";

const version = readFileSync(new URL("../build/config.yml", import.meta.url), "utf8").match(/version: "([^"]+)"/)?.[1] ?? "development";

export default defineConfig({
  define: {__APP_VERSION__: JSON.stringify(version)},
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true
  },
  plugins: [wails("./bindings")]
});
