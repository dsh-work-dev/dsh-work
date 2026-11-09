import './origin.js';

declare global {
 var __DSH_WORK_ORIGINS__: {
  origin(raw: string | URL, base?: string): string | undefined;
  same(raw: string | URL, base: string): boolean;
  gateway(raw: string | URL, base: string): boolean;
 };
}

export const bridgeOrigin = globalThis.__DSH_WORK_ORIGINS__.origin;
export const sameBridgeOrigin = globalThis.__DSH_WORK_ORIGINS__.same;
export const gatewayBridgeURL = globalThis.__DSH_WORK_ORIGINS__.gateway;
