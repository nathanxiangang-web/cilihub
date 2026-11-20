import { useCallback, useEffect, useState } from "react";
import {
  deleteShare,
  restoreShare,
  revokeShare,
  shares as fetchShares,
  type Pagination,
  type ShareRow,
} from "../api";
import { AdminTable, type AdminColumn } from "../components/AdminTable";
import AdminToast, { type ToastKind, type ToastState } from "../components/AdminToast";

const PAGE_SIZE = 20;

function shareState(row: ShareRow): "active" | "revoked" | "expired" {
  if (row.status === "revoked") return "revoked";
  if (row.expires_at && Date.parse(row.expires_at) <= Date.now()) return "expired";
  return "active";
}

function shareLink(code: string): string {
  return `${window.location.origin}/s/${code}`;
}

export default function SharesPage() {
  const [q, setQ] = useState("");
  const [appliedQ, setAppliedQ] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<ShareRow[]>([]);
  const [pagination, setPagination] = useState<Pagination>({ page: 1, size: PAGE_SIZE, pages: 1, total: 0 });
  const [toast, setToast] = useState<ToastState | null>(null);
  const notify = useCallback((message: string, kind: ToastKind = "info") => {
    setToast({ message, kind });
  }, []);

  function load(nextPage = page) {
    fetchShares({ q: appliedQ, status, page: nextPage, size: PAGE_SIZE })
      .then((data) => {
        setRows(data.items ?? []);
        setPagination(data.pagination ?? { page: nextPage, size: PAGE_SIZE, pages: 1, total: 0 });
      })
      .catch((err) => notify(err instanceof Error ? err.message : "加载分享失败", "error"));
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

  async function copyLink(row: ShareRow) {
    try {
      await navigator.clipboard.writeText(shareLink(row.code));
      notify("分享链接已复制", "ok");
    } catch {
      notify(`复制失败，请手动复制：${shareLink(row.code)}`, "error");
    }
  }

  async function revoke(row: ShareRow) {
    if (!window.confirm(`确定取消分享「${row.code}」？取消后该链接立即失效。`)) return;
    try {
      await revokeShare(row.id);
      notify("分享已取消", "ok");
      load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "取消分享失败", "error");
    }
  }

  async function restore(row: ShareRow) {
    try {
      await restoreShare(row.id);
      notify("分享已恢复", "ok");
      load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "恢复分享失败", "error");
    }
  }

  async function remove(row: ShareRow) {
    if (!window.confirm(`确定删除分享「${row.code}」？删除后记录与链接都将不存在。`)) return;
    try {
      await deleteShare(row.id);
      notify("分享已删除", "ok");
      if (rows.length === 1 && page > 1) setPage(page - 1);
      else load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "删除分享失败", "error");
    }
  }

  const columns: AdminColumn<ShareRow>[] = [
    { key: "code", title: "分享码", width: "130px", render: (row) => row.code },
    { key: "work_title", title: "作品", render: (row) => row.work_title || "-" },
    { key: "creator", title: "创建人", width: "120px", render: (row) => row.creator || "管理员" },
    {
      key: "status",
      title: "状态",
      width: "90px",
      render: (row) => {
        const state = shareState(row);
        const cls = state === "active" ? "cs-badge--ok" : state === "revoked" ? "cs-badge--muted" : "cs-badge--warn";
        return <span className={`cs-badge ${cls}`}>{state === "active" ? "有效" : state === "revoked" ? "已取消" : "已过期"}</span>;
      },
    },
    { key: "expires_at", title: "过期时间", width: "170px", render: (row) => row.expires_at || "永久有效" },
    { key: "views", title: "访问次数", width: "100px", align: "right", render: (row) => row.views ?? 0 },
    { key: "created_at", title: "创建时间", width: "170px", render: (row) => row.created_at || "-" },
    {
      key: "ops",
      title: "操作",
      width: "240px",
      render: (row) => (
        <div className="cs-cat-row__ops">
          <button type="button" className="cs-btn cs-btn--tiny" onClick={() => copyLink(row)}>
            复制链接
          </button>
          {row.status === "revoked" ? (
            <button type="button" className="cs-btn cs-btn--tiny" onClick={() => restore(row)}>
              恢复
            </button>
          ) : (
            <button type="button" className="cs-btn cs-btn--tiny" onClick={() => revoke(row)}>
              取消分享
            </button>
          )}
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
          placeholder="搜索分享码或作品标题…"
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
          <option value="active">有效</option>
          <option value="revoked">已取消</option>
          <option value="expired">已过期</option>
        </select>
        <button type="button" className="cs-btn cs-btn--primary" onClick={search}>
          查询
        </button>
        <button type="button" className="cs-btn" onClick={resetFilters}>
          重置
        </button>
      </div>

      <section className="cs-card cs-card--table">
        <div className="cs-card__head">
          <h3>分享列表</h3>
          <span className="cs-muted">共 {pagination.total} 条分享</span>
        </div>
        <AdminTable columns={columns} rows={rows} rowKey={(row) => row.id} empty="没有匹配的分享" />
        <div className="cs-pager cs-pager--inside">
          <span className="cs-muted">
            第 {pagination.page} / {pagination.pages} 页
          </span>
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