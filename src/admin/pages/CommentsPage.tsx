import { useCallback, useEffect, useState } from "react";
import {
  comments as fetchComments,
  deleteComment,
  updateComment,
  type CommentPage,
  type CommentRow,
  type Pagination,
} from "../api";
import { AdminTable, type AdminColumn } from "../components/AdminTable";
import AdminToast, { type ToastKind, type ToastState } from "../components/AdminToast";

const PAGE_SIZE = 20;

function excerpt(text: string, max = 60): string {
  const flat = (text ?? "").replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max)}…` : flat;
}

export default function CommentsPage() {
  const [q, setQ] = useState("");
  const [appliedQ, setAppliedQ] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<CommentRow[]>([]);
  const [pagination, setPagination] = useState<Pagination>({ page: 1, size: PAGE_SIZE, pages: 1, total: 0 });
  const [toast, setToast] = useState<ToastState | null>(null);
  const notify = useCallback((message: string, kind: ToastKind = "info") => {
    setToast({ message, kind });
  }, []);

  function load(nextPage = page) {
    fetchComments({ q: appliedQ, status, page: nextPage, size: PAGE_SIZE })
      .then((data: CommentPage) => {
        setRows(data.items ?? []);
        setPagination(data.pagination ?? { page: nextPage, size: PAGE_SIZE, pages: 1, total: 0 });
      })
      .catch((err) => notify(err instanceof Error ? err.message : "加载评论失败", "error"));
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

  async function setHidden(row: CommentRow, hidden: boolean) {
    try {
      await updateComment(row.id, hidden ? "hidden" : "visible");
      notify(hidden ? "评论已隐藏" : "评论已恢复可见", "ok");
      load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "操作失败", "error");
    }
  }

  async function remove(row: CommentRow) {
    if (!window.confirm(`确定删除这条评论？删除后无法恢复。`)) return;
    try {
      await deleteComment(row.id);
      notify("评论已删除", "ok");
      if (rows.length === 1 && page > 1) setPage(page - 1);
      else load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "删除评论失败", "error");
    }
  }

  const columns: AdminColumn<CommentRow>[] = [
    { key: "work_title", title: "作品", render: (row) => row.work_title || `#${row.work_id}` },
    { key: "user", title: "用户", width: "120px", render: (row) => row.user || "-" },
    { key: "episode_no", title: "集", width: "70px", align: "right", render: (row) => row.episode_no || "-" },
    {
      key: "content",
      title: "内容摘要",
      render: (row) => (
        <span
          title={row.content}
          style={{ display: "inline-block", maxWidth: 360, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
        >
          {excerpt(row.content) || "-"}
        </span>
      ),
    },
    {
      key: "status",
      title: "状态",
      width: "90px",
      render: (row) =>
        row.status === "hidden" ? (
          <span className="cs-badge cs-badge--muted">已隐藏</span>
        ) : (
          <span className="cs-badge cs-badge--ok">可见</span>
        ),
    },
    { key: "created_at", title: "时间", width: "170px", render: (row) => row.created_at || "-" },
    {
      key: "ops",
      title: "操作",
      width: "160px",
      render: (row) => (
        <div className="cs-cat-row__ops">
          {row.status === "hidden" ? (
            <button type="button" className="cs-btn cs-btn--tiny" onClick={() => setHidden(row, false)}>
              显示
            </button>
          ) : (
            <button type="button" className="cs-btn cs-btn--tiny" onClick={() => setHidden(row, true)}>
              隐藏
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
          placeholder="搜索作品、用户或评论内容…"
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
          <option value="visible">可见</option>
          <option value="hidden">已隐藏</option>
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
          <h3>评论列表</h3>
          <span className="cs-muted">共 {pagination.total} 条评论</span>
        </div>
        <AdminTable columns={columns} rows={rows} rowKey={(row) => row.id} empty="没有匹配的评论" />
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