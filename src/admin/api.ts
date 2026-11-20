/** 后台接口客户端：统一走 /admin/api，解析 __admin_error，401 回登录表单。 */

const BASE = "/admin/api";

export type AdminUser = { id: number; username: string };
export type Pagination = { page: number; size: number; pages: number; total: number };

export type WorkRow = {
  id: number;
  title: string;
  category_id: number;
  category_name: string;
  visible: boolean;
  pinned: boolean;
  sort_index: number;
  episode_count: number;
  missing_count: number;
  updated_at: string;
};

export type WorksPage = { items: WorkRow[]; pagination: Pagination };

export type WorkFile = {
  file_id: number;
  file_name: string;
  episode_no: number;
  present: boolean;
  size_bytes: number;
  missing_since: string;
};

export type WorkDetail = { work: WorkRow; files: WorkFile[] };

export type Category = {
  id: number;
  slug: string;
  name: string;
  sort_index: number;
  visible: boolean;
  work_count: number;
};

export type SyncRun = {
  id: number;
  started_at: string;
  finished_at: string;
  status: string;
  added: number;
  missing: number;
  failed: number;
  message: string;
};

export type SyncEvent = {
  id: number;
  run_id: number;
  kind: string;
  file_path: string;
  message: string;
  at: string;
};

export type SyncState = { last: SyncRun | null; runs: SyncRun[]; events: SyncEvent[] };

export type Overview = {
  works: number;
  visible: number;
  missing: number;
  last_sync: { status: string; at: string; added: number; missing: number } | null;
};

export type Diagnostics = {
  catalog: { in_snapshot: boolean };
  openlist: { ok: boolean; status: number; elapsed_ms: number; reason: string };
  playback: { url: string; mode: string };
};

export type AuditRow = {
  at: string;
  admin: string;
  action: string;
  target_type: string;
  target_id: string;
  detail: string;
};

export type AuditPage = { items: AuditRow[]; pagination: Pagination };
export type HomeOrder = { pinned: number[]; order: number[] };

export type WorkPatch = { title?: string; category_id?: number; visible?: boolean; pinned?: boolean };

type RequestOptions = {
  method?: "GET" | "POST" | "DELETE";
  body?: unknown;
  query?: Record<string, string | number | undefined>;
};

let unauthorizedHandler: (() => void) | null = null;

/** 注册 401 处理器（AdminApp 用它清空登录态并回到登录表单）。 */
export function onUnauthorized(handler: (() => void) | null): void {
  unauthorizedHandler = handler;
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  let url = `${BASE}${path}`;
  if (opts.query) {
    const sp = new URLSearchParams();
    for (const [k, v] of Object.entries(opts.query)) {
      if (v !== undefined && v !== "") sp.set(k, String(v));
    }
    const qs = sp.toString();
    if (qs) url += `?${qs}`;
  }
  const hasBody = opts.body !== undefined;
  let res: Response;
  try {
    res = await fetch(url, {
      method: opts.method ?? (hasBody ? "POST" : "GET"),
      credentials: "same-origin",
      headers: hasBody ? { "Content-Type": "application/json" } : undefined,
      body: hasBody ? JSON.stringify(opts.body) : undefined,
    });
  } catch {
    throw new Error("请求失败，请检查网络");
  }
  const data = (await res.json().catch(() => null)) as Record<string, unknown> | null;
  const msg = data && typeof data.__admin_error === "string" ? data.__admin_error : "";
  if (res.status === 401) {
    unauthorizedHandler?.();
    throw new Error(msg || "请先登录");
  }
  if (msg) throw new Error(msg);
  if (!res.ok) throw new Error(res.status === 404 ? "接口或数据不存在" : `请求失败（HTTP ${res.status}）`);
  return data as unknown as T;
}

export async function login(username: string, password: string): Promise<AdminUser> {
  const res = await request<{ user: AdminUser }>("/login", { body: { username, password } });
  return res.user;
}

export async function logout(): Promise<void> {
  await request<{ ok: boolean }>("/logout", { body: {} });
}

export async function session(): Promise<AdminUser | null> {
  const res = await request<{ user: AdminUser | null }>("/session");
  return res.user ?? null;
}

export async function changePassword(old_password: string, new_password: string): Promise<void> {
  await request<{ ok: boolean }>("/password", { body: { old_password, new_password } });
}

export function overview(): Promise<Overview> {
  return request<Overview>("/overview");
}

export function works(params: {
  q?: string;
  category?: string;
  status?: string;
  page?: number;
  size?: number;
}): Promise<WorksPage> {
  return request<WorksPage>("/works", {
    query: {
      q: params.q,
      category: params.category,
      status: params.status,
      page: params.page,
      size: params.size,
    },
  });
}

export function work(id: number): Promise<WorkDetail> {
  return request<WorkDetail>(`/works/${id}`);
}

export async function patchWork(id: number, patch: WorkPatch): Promise<WorkRow> {
  const res = await request<{ work: WorkRow }>(`/works/${id}`, { body: patch });
  return res.work;
}

export function setVisibility(ids: number[], visible: boolean): Promise<{ ok: boolean; count: number }> {
  return request<{ ok: boolean; count: number }>("/works/visibility", { body: { ids, visible } });
}

export function reorderEpisodes(id: number, order: number[]): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/works/${id}/episodes`, { body: { order } });
}

export function assignFiles(work_id: number, file_ids: number[]): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>("/works/assign", { body: { work_id, file_ids } });
}

export async function categories(): Promise<Category[]> {
  const res = await request<{ items: Category[] }>("/categories");
  return res.items ?? [];
}

export async function createCategory(name: string, slug: string): Promise<Category> {
  const res = await request<{ category: Category }>("/categories", { body: { name, slug } });
  return res.category;
}

export function updateCategory(id: number, name: string, visible: boolean): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/categories/${id}`, { body: { name, visible } });
}

export function reorderCategories(ids: number[]): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>("/categories/reorder", { body: { ids } });
}

export function home(): Promise<HomeOrder> {
  return request<HomeOrder>("/home");
}

export function setHome(pinned: number[], order: number[]): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>("/home", { body: { pinned, order } });
}

export function syncState(): Promise<SyncState> {
  return request<SyncState>("/sync");
}

export async function runSync(): Promise<number> {
  const res = await request<{ run_id: number }>("/sync/run", { body: {} });
  return res.run_id;
}

export function diagnostics(file_id: number): Promise<Diagnostics> {
  return request<Diagnostics>("/diagnostics", { query: { file_id } });
}

export function audit(page = 1, size = 20): Promise<AuditPage> {
  return request<AuditPage>("/audit", { query: { page, size } });
}
export type OpenListState = {
  configured: boolean;
  origin: string;
  folder: string;
  cdn_host: string;
  username: string;
  password_set: boolean;
  updated_at: string;
};

export type OpenListTest = { ok: boolean; status: number; elapsed_ms: number; entries: number; message: string };

export type OpenListTestInput = { origin: string; username: string; password: string; folder: string };

export type OpenListSaveInput = {
  origin: string;
  username: string;
  password?: string;
  folder: string;
  cdn_host: string;
};

export function openlistSettings(): Promise<OpenListState> {
  return request<OpenListState>("/openlist");
}

export function testOpenList(input: OpenListTestInput): Promise<OpenListTest> {
  return request<OpenListTest>("/openlist/test", { body: input });
}

export function saveOpenList(input: OpenListSaveInput): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>("/openlist", { body: input });
}
export type OpenListDirEntry = { name: string; path: string };

export type OpenListBrowse = { path: string; items: OpenListDirEntry[] };

export function browseOpenList(path?: string): Promise<OpenListBrowse> {
  return request<OpenListBrowse>("/openlist/browse", { query: { path } });
}
export type UserRow = {
  id: number;
  username: string;
  status: string;
  note: string;
  created_at: string;
  last_login_at: string;
};

export type UsersPage = { items: UserRow[]; pagination: Pagination };

export type UserCreateInput = { username: string; password: string; note?: string };

export type UserUpdateInput = {
  username?: string;
  status?: string;
  note?: string;
  /** 留空表示不修改密码 */
  password?: string;
};

export function users(q: string, status: string, page = 1, size = 20): Promise<UsersPage> {
  return request<UsersPage>("/users", { query: { q, status, page, size } });
}

export async function createUser(input: UserCreateInput): Promise<UserRow> {
  const res = await request<{ user: UserRow }>("/users", { body: input });
  return res.user;
}

export function updateUser(id: number, input: UserUpdateInput): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/users/${id}`, { body: input });
}

export function deleteUser(id: number): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/users/${id}`, { method: "DELETE" });
}
export type ShareRow = {
  id: number;
  code: string;
  work_id: number;
  work_title: string;
  creator: string;
  status: string;
  expires_at: string;
  views: number;
  created_at: string;
};

export type SharesPage = { items: ShareRow[]; pagination: Pagination };

export function shares(params: {
  q?: string;
  status?: string;
  page?: number;
  size?: number;
}): Promise<SharesPage> {
  return request<SharesPage>("/shares", {
    query: { q: params.q, status: params.status, page: params.page, size: params.size },
  });
}

export function revokeShare(id: number): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/shares/${id}/revoke`, { body: {} });
}

export function restoreShare(id: number): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/shares/${id}/restore`, { body: {} });
}

export function deleteShare(id: number): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/shares/${id}`, { method: "DELETE" });
}