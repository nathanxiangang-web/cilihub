import { useCallback, useEffect, useRef, useState, type UIEvent } from "react";
import {
  categories as fetchCategories,
  setVisibility,
  works as fetchWorks,
  type Category,
  type Pagination,
  type WorkRow,
} from "../api";
import { AdminTable, type AdminColumn } from "../components/AdminTable";
import AdminToast, { type ToastKind, type ToastState } from "../components/AdminToast";
import DetailDrawer from "../components/DetailDrawer";

const STORAGE_KEY = "cilihub.admin.library";
const PAGE_SIZE = 20;

type SavedLibrary = { q: string; category: string; status: string; page: number; scrollTop: number };

function readSaved(): SavedLibrary | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const data = JSON.parse(raw) as Partial<SavedLibrary>;
    return {
      q: typeof data.q === "string" ? data.q : "",
      category: typeof data.category === "string" ? data.category : "",
      status: typeof data.status === "string" ? data.status : "",
      page: typeof data.page === "number" && data.page > 0 ? data.page : 1,
      scrollTop: typeof data.scrollTop === "number" ? data.scrollTop : 0,
    };
  } catch {
    return null;
  }
}

export default function LibraryPage() {
  const [initial] = useState<SavedLibrary | null>(() => readSaved());
  const [q, setQ] = useState(initial?.q ?? "");
  const [appliedQ, setAppliedQ] = useState(initial?.q ?? "");
  const [category, setCategory] = useState(initial?.category ?? "");
  const [status, setStatus] = useState(initial?.status ?? "");
  const [page, setPage] = useState(initial?.page ?? 1);
  const [rows, setRows] = useState<WorkRow[]>([]);
  const [pagination, setPagination] = useState<Pagination>({ page: 1, size: PAGE_SIZE, pages: 1, total: 0 });
  const [loading, setLoading] = useState(true);
  const [loadedOnce, setLoadedOnce] = useState(false);
  const [error, setError] = useState("");
  const [cats, setCats] = useState<Category[]>([]);
  const [workOptions, setWorkOptions] = useState<{ id: number; title: string }[]>([]);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [drawerId, setDrawerId] = useState<number | null>(null);
  const [toast, setToast] = useState<ToastState | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const scrollTopRef = useRef(initial?.scrollTop ?? 0);
  const restoredRef = useRef(false);
  const scrollTick = useRef(0);
  const filterRef = useRef({ q: appliedQ, category, status, page });
  filterRef.current = { q: appliedQ, category, status, page };

  const notify = useCallback((message: string, kind: ToastKind = "info") => {
    setToast({ message, kind });
  }, []);

  function persist(scrollTop?: number) {
    try {
      const value: SavedLibrary = {
        ...filterRef.current,
        scrollTop: scrollTop ?? scrollRef.current?.scrollTop ?? scrollTopRef.current,
      };
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(value));
    } catch {
      /* 隐私模式下 sessionStorage 可能不可用，不影响使用 */
    }
  }

  function load() {
    setLoading(true);
    setError("");
    fetchWorks({ q: appliedQ, category, status, page, size: PAGE_SIZE })
      .then((data) => {
        setRows(data.items ?? []);
        setPagination(data.pagination ?? { page, size: PAGE_SIZE, pages: 1, total: 0 });
        setLoadedOnce(true);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "加载影视库失败"))
      .finally(() => setLoading(false));
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appliedQ, category, status, page]);

  useEffect(() => {
    fetchCategories().then(setCats).catch(() => setCats([]));
    fetchWorks({ size: 200 })
      .then((data) => setWorkOptions((data.items ?? []).map((w) => ({ id: w.id, title: w.title }))))
      .catch(() => setWorkOptions([]));
  }, []);

  useEffect(() => {
    persist();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, category, status, page]);

  useEffect(() => () => persist(), []);

  useEffect(() => {
    if (!loadedOnce || restoredRef.current) return;
    const el = scrollRef.current;
    const top = initial?.scrollTop ?? 0;
    if (el) el.scrollTop = top;
    scrollTopRef.current = top;
    restoredRef.current = true;
  }, [loadedOnce, initial]);

  function onScroll(e: UIEvent<HTMLDivElement>) {
    const top = e.currentTarget.scrollTop;
    scrollTopRef.current = top;
    window.cancelAnimationFrame(scrollTick.current);
    scrollTick.current = window.requestAnimationFrame(() => persist(top));
  }

  function toggle(id: number) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleAll(checked: boolean) {
    setSelected((prev) => {
      const next = new Set(prev);
      for (const row of rows) {
        if (checked) next.add(row.id);
        else next.delete(row.id);
      }
      return next;
    });
  }

  async function bulk(nextVisible: boolean) {
    const ids = [...selected];
    if (!ids.length) {
      notify("请先选择作品", "info");
      return;
    }
    try {
      const res = await setVisibility(ids, nextVisible);
      setSelected(new Set<number>());
      load();
      notify(`已${nextVisible ? "上架" : "下架"} ${res.count} 部作品`, "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "批量操作失败", "error");
    }
  }

  const columns: AdminColumn<WorkRow>[] = [
    {
      key: "title",
      title: "标题",
      render: (row) => (
        <span className="cs-cell-title">
          {row.title}
          {row.pinned ? <span className="cs-badge cs-badge--pin">置顶</span> : null}
        </span>
      ),
    },
    { key: "category", title: "分类", width: "120px", render: (row) => row.category_name },
    { key: "episodes", title: "集数", width: "80px", align: "right", render: (row) => row.episode_count },
    {
      key: "missing",
      title: "缺失",
      width: "80px",
      align: "right",
      render: (row) => (
        <span className={row.missing_count > 0 ? "cs-danger-text" : "cs-muted"}>{row.missing_count}</span>
      ),
    },
    {
      key: "visible",
      title: "状态",
      width: "96px",
      render: (row) => (
        <span className={`cs-badge ${row.visible ? "cs-badge--ok" : "cs-badge--muted"}`}>
          {row.visible ? "已上架" : "未上架"}
        </span>
      ),
    },
    { key: "updated", title: "更新时间", width: "170px", render: (row) => <span className="cs-muted">{row.updated_at}</span> },
  ];

  const allChecked = rows.length > 0 && rows.every((row) => selected.has(row.id));

  return (
    <div className="cs-page cs-library">
      <div className="cs-filter">
        <input
          className="cs-input cs-filter__q"
          placeholder="搜索标题…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              setAppliedQ(q.trim());
              setPage(1);
            }
          }}
        />
        <select
          className="cs-input"
          value={category}
          onChange={(e) => {
            setCategory(e.target.value);
            setPage(1);
          }}
        >
          <option value="">全部分类</option>
          {cats.map((c) => (
            <option key={c.id} value={c.slug}>{c.name}</option>
          ))}
        </select>
        <select
          className="cs-input"
          value={status}
          onChange={(e) => {
            setStatus(e.target.value);
            setPage(1);
          }}
        >
          <option value="">全部状态</option>
          <option value="visible">已上架</option>
          <option value="hidden">未上架</option>
        </select>
        <button
          type="button"
          className="cs-btn cs-btn--primary"
          onClick={() => {
            setAppliedQ(q.trim());
            setPage(1);
          }}
        >
          查询
        </button>
        <button
          type="button"
          className="cs-btn"
          onClick={() => {
            setQ("");
            setAppliedQ("");
            setCategory("");
            setStatus("");
            setPage(1);
            setSelected(new Set<number>());
            if (scrollRef.current) scrollRef.current.scrollTop = 0;
            scrollTopRef.current = 0;
          }}
        >
          重置
        </button>
        <span className="cs-filter__spacer" />
        <button type="button" className="cs-btn" onClick={() => bulk(true)}>批量上架</button>
        <button type="button" className="cs-btn" onClick={() => bulk(false)}>批量下架</button>
      </div>

      {error ? <div className="cs-alert cs-alert--error">{error}</div> : null}

      <div className="cs-library__scroll" ref={scrollRef} onScroll={onScroll}>
        <div className="cs-card cs-card--table">
          <AdminTable
            columns={columns}
            rows={rows}
            rowKey={(row) => row.id}
            empty={loading ? "加载中…" : "没有匹配的作品"}
            onRowClick={(row) => setDrawerId(row.id)}
            selection={{ selected, getId: (row) => row.id, onToggle: toggle, onToggleAll: toggleAll, allChecked }}
          />
          {loading && rows.length > 0 ? <div className="cs-loading-bar">加载中…</div> : null}
        </div>

        <div className="cs-pager">
          <span className="cs-muted">共 {pagination.total} 部</span>
          <div className="cs-pager__nav">
            <button type="button" className="cs-btn cs-btn--tiny" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              上一页
            </button>
            <span className="cs-muted">第 {pagination.page} / {pagination.pages} 页</span>
            <button
              type="button"
              className="cs-btn cs-btn--tiny"
              disabled={page >= pagination.pages}
              onClick={() => setPage(page + 1)}
            >
              下一页
            </button>
          </div>
        </div>
      </div>

      <DetailDrawer
        workId={drawerId}
        open={drawerId != null}
        categories={cats}
        workOptions={workOptions}
        onClose={() => setDrawerId(null)}
        onChanged={() => {
          load();
          fetchWorks({ size: 200 })
            .then((data) => setWorkOptions((data.items ?? []).map((w) => ({ id: w.id, title: w.title }))))
            .catch(() => {});
        }}
        notify={notify}
      />

      <AdminToast toast={toast} onClose={() => setToast(null)} />
    </div>
  );
}