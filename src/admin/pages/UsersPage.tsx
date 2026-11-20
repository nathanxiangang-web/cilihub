import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  createUser,
  deleteUser,
  updateUser,
  users as fetchUsers,
  type Pagination,
  type UserRow,
} from "../api";
import { AdminTable, type AdminColumn } from "../components/AdminTable";
import AdminToast, { type ToastKind, type ToastState } from "../components/AdminToast";

const PAGE_SIZE = 20;

type FormMode = "create" | "edit" | "reset";

type UserForm = {
  mode: FormMode;
  id: number;
  username: string;
  password: string;
  status: string;
  note: string;
};

const BLANK_FORM: UserForm = {
  mode: "create",
  id: 0,
  username: "",
  password: "",
  status: "active",
  note: "",
};

export default function UsersPage() {
  const [q, setQ] = useState("");
  const [appliedQ, setAppliedQ] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<UserRow[]>([]);
  const [pagination, setPagination] = useState<Pagination>({ page: 1, size: PAGE_SIZE, pages: 1, total: 0 });
  const [form, setForm] = useState<UserForm | null>(null);
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<ToastState | null>(null);
  const notify = useCallback((message: string, kind: ToastKind = "info") => {
    setToast({ message, kind });
  }, []);

  function load(nextPage = page) {
    fetchUsers(appliedQ, status, nextPage, PAGE_SIZE)
      .then((data) => {
        setRows(data.items ?? []);
        setPagination(data.pagination ?? { page: nextPage, size: PAGE_SIZE, pages: 1, total: 0 });
      })
      .catch((err) => notify(err instanceof Error ? err.message : "加载用户失败", "error"));
  }

  useEffect(() => {
    load(page);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appliedQ, status, page]);

  function search() {
    setAppliedQ(q.trim());
    setPage(1);
  }

  function resetFilters() {
    setQ("");
    setAppliedQ("");
    setStatus("");
    setPage(1);
  }

  function openCreate() {
    setForm({ ...BLANK_FORM });
  }

  function openEdit(row: UserRow) {
    setForm({
      mode: "edit",
      id: row.id,
      username: row.username,
      password: "",
      status: row.status === "disabled" ? "disabled" : "active",
      note: row.note ?? "",
    });
  }

  function openReset(row: UserRow) {
    setForm({ mode: "reset", id: row.id, username: row.username, password: "", status: row.status, note: "" });
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!form || busy) return;
    if (form.mode !== "reset" && !form.username.trim()) {
      notify("请填写用户名", "error");
      return;
    }
    if (form.mode === "create" && !form.password) {
      notify("请设置登录密码", "error");
      return;
    }
    if (form.mode === "reset" && !form.password) {
      notify("请填写新密码", "error");
      return;
    }
    setBusy(true);
    try {
      if (form.mode === "create") {
        await createUser({
          username: form.username.trim(),
          password: form.password,
          note: form.note.trim(),
        });
        notify("用户已创建", "ok");
      } else if (form.mode === "edit") {
        await updateUser(form.id, {
          username: form.username.trim(),
          status: form.status,
          note: form.note.trim(),
          ...(form.password ? { password: form.password } : {}),
        });
        notify(form.password ? "用户已更新，密码已重置" : "用户已更新", "ok");
      } else {
        await updateUser(form.id, { password: form.password });
        notify("密码已重置，该用户已有会话全部失效", "ok");
      }
      setForm(null);
      load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "保存失败", "error");
    } finally {
      setBusy(false);
    }
  }

  async function remove(row: UserRow) {
    if (!window.confirm(`确定删除用户「${row.username}」？删除后该账号将无法登录。`)) return;
    try {
      await deleteUser(row.id);
      notify("用户已删除", "ok");
      if (rows.length === 1 && page > 1) setPage(page - 1);
      else load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "删除用户失败", "error");
    }
  }

  const columns: AdminColumn<UserRow>[] = [
    { key: "username", title: "用户名", width: "200px", render: (row) => row.username },
    {
      key: "status",
      title: "状态",
      width: "90px",
      render: (row) => (
        <span className={`cs-badge ${row.status === "disabled" ? "cs-badge--muted" : "cs-badge--ok"}`}>
          {row.status === "disabled" ? "已禁用" : "正常"}
        </span>
      ),
    },
    { key: "note", title: "备注", render: (row) => row.note || "-" },
    { key: "created_at", title: "创建时间", width: "170px", render: (row) => row.created_at || "-" },
    { key: "last_login_at", title: "最近登录", width: "170px", render: (row) => row.last_login_at || "从未登录" },
    {
      key: "ops",
      title: "操作",
      width: "200px",
      render: (row) => (
        <div className="cs-cat-row__ops">
          <button type="button" className="cs-btn cs-btn--tiny" onClick={() => openEdit(row)}>
            编辑
          </button>
          <button type="button" className="cs-btn cs-btn--tiny" onClick={() => openReset(row)}>
            重置密码
          </button>
          <button type="button" className="cs-btn cs-btn--tiny" onClick={() => remove(row)}>
            删除
          </button>
        </div>
      ),
    },
  ];

  return (
    <div className="cs-page">
      <div className="cs-filter">
        <input
          className="cs-input cs-filter__q"
          placeholder="搜索用户名…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") search();
          }}
        />
        <select
          className="cs-input"
          value={status}
          onChange={(e) => {
            setStatus(e.target.value);
            setPage(1);
          }}
        >
          <option value="">全部状态</option>
          <option value="active">正常</option>
          <option value="disabled">已禁用</option>
        </select>
        <button type="button" className="cs-btn cs-btn--primary" onClick={search}>
          查询
        </button>
        <button type="button" className="cs-btn" onClick={resetFilters}>
          重置
        </button>
        <span className="cs-filter__spacer" />
        <button type="button" className="cs-btn cs-btn--primary" onClick={openCreate}>
          新建用户
        </button>
      </div>

      {form ? (
        <section className="cs-card">
          <div className="cs-card__head">
            <h3>
              {form.mode === "create"
                ? "新建用户"
                : form.mode === "edit"
                  ? `编辑用户：${form.username}`
                  : `重置密码：${form.username}`}
            </h3>
            <span className="cs-muted">
              {form.mode === "reset" ? "重置后该用户已有会话会全部失效" : "密码留空表示不修改"}
            </span>
          </div>
          <form className="cs-form-grid" onSubmit={submit}>
            {form.mode !== "reset" ? (
              <>
                <label className="cs-field">
                  <span>用户名</span>
                  <input
                    autoComplete="off"
                    value={form.username}
                    onChange={(e) => setForm({ ...form, username: e.target.value })}
                  />
                </label>
                <label className="cs-field">
                  <span>{form.mode === "create" ? "登录密码" : "新密码（留空不改）"}</span>
                  <input
                    type="password"
                    autoComplete="new-password"
                    value={form.password}
                    onChange={(e) => setForm({ ...form, password: e.target.value })}
                  />
                </label>
                <label className="cs-field">
                  <span>状态</span>
                  <select
                    value={form.status}
                    onChange={(e) => setForm({ ...form, status: e.target.value })}
                  >
                    <option value="active">正常</option>
                    <option value="disabled">禁用</option>
                  </select>
                </label>
                <label className="cs-field">
                  <span>备注</span>
                  <input value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} />
                </label>
              </>
            ) : (
              <label className="cs-field">
                <span>新密码</span>
                <input
                  type="password"
                  autoComplete="new-password"
                  value={form.password}
                  onChange={(e) => setForm({ ...form, password: e.target.value })}
                />
              </label>
            )}
            <div className="cs-form-grid__action">
              <button type="submit" className="cs-btn cs-btn--primary" disabled={busy}>
                {busy ? "保存中…" : "保存"}
              </button>
              <button type="button" className="cs-btn" onClick={() => setForm(null)}>
                取消
              </button>
            </div>
          </form>
        </section>
      ) : null}

      <section className="cs-card cs-card--table">
        <div className="cs-card__head">
          <h3>用户列表</h3>
          <span className="cs-muted">共 {pagination.total} 个账号</span>
        </div>
        <AdminTable
          columns={columns}
          rows={rows}
          rowKey={(row) => row.id}
          empty="没有匹配的用户"
        />
        <div className="cs-pager cs-pager--inside">
          <span className="cs-muted">第 {pagination.page} / {pagination.pages} 页</span>
          <button type="button" className="cs-btn cs-btn--tiny" disabled={page <= 1} onClick={() => setPage(page - 1)}>
            上一页
          </button>
          <button
            type="button"
            className="cs-btn cs-btn--tiny"
            disabled={page >= pagination.pages}
            onClick={() => setPage(page + 1)}
          >
            下一页
          </button>
        </div>
      </section>

      <AdminToast toast={toast} onClose={() => setToast(null)} />
    </div>
  );
}