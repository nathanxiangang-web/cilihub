export const cloudEndpoint = "/api/cloud-downloads";

export async function cloudRequest<T>(url: string, options: RequestInit): Promise<T> {
  const response = await fetch(url, { credentials: "same-origin", cache: "no-store", ...options });
  if (response.status === 401) throw new Error("请重新登录");
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || "云下载请求失败，请稍后刷新");
  return data as T;
}

export function queueCloudDownload(magnet: string, title: string | undefined, signal: AbortSignal) {
  return cloudRequest<{ created: boolean }>(cloudEndpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ magnet, title }),
    signal,
  });
}
