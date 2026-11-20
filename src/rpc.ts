import { invoke as tauriInvoke } from "@tauri-apps/api/core";

/** Web 构建（VITE_TARGET=web）走 fetch + /rpc，其余构建仍走 Tauri IPC。 */
export const isWeb: boolean = import.meta.env.VITE_TARGET === "web";

async function fetchInvoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`/rpc/${encodeURIComponent(cmd)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(args ?? {}),
      credentials: "same-origin",
    });
  } catch {
    throw new Error("请求失败，请检查网络");
  }
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    throw new Error("请求失败，请检查网络");
  }
  if (body && typeof body === "object" && "__hg_error" in body) {
    throw new Error(String((body as { __hg_error: unknown }).__hg_error));
  }
  return body as T;
}

export function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  return isWeb ? fetchInvoke<T>(cmd, args) : tauriInvoke<T>(cmd, args);
}

export function exitApp(): void {
  if (isWeb) return;
  tauriInvoke("plugin:app|exit", { code: 0 }).catch(() => {});
}

export function setOrientation(landscape: boolean): void {
  if (isWeb) return;
  tauriInvoke("set_orientation", { landscape }).catch(() => {});
}