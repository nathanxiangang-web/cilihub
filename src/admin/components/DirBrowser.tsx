import { useCallback, useEffect, useState } from "react";
import { browseOpenList } from "../api";
import { AdminTable, type AdminColumn } from "./AdminTable";

type DirItem = { name: string; path: string };

function crumbPath(fullPath: string, depth: number): string {
  const segments = fullPath.split("/").filter(Boolean);
  return `/${segments.slice(0, depth).join("/")}`;
}

/** 目录选择器：只读浏览 OpenList 子目录，点「使用此目录」回填后关闭。 */
export default function DirBrowser({ initialPath, onPick, onClose }: {
  initialPath: string;
  onPick: (path: string) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState(initialPath || "/");
  const [items, setItems] = useState<DirItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback((target: string) => {
    setLoading(true);
    setError("");
    browseOpenList(target.trim() || undefined)
      .then((res) => {
        setPath(res.path || "/");
        setItems(res.items ?? []);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "读取目录失败"))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    load(initialPath);
  }, [initialPath, load]);

  function handlePick() {
    onPick(path);
    onClose();
  }

  const columns: AdminColumn<DirItem>[] = [
    { key: "name", title: "目录", render: (row) => <span className="cs-cell-title">{row.name}</span> },
  ];

  const segments = path.split("/").filter(Boolean);

  return (
    <>
      <div className="cs-drawer__overlay" onClick={onClose} />
      <aside className="cs-drawer" role="dialog" aria-label="选择目录">
        <header className="cs-drawer__head">
          <div>
            <h2>选择目录</h2>
            <span className="cs-muted cs-mono">{path || "/"}</span>
          </div>
          <button type="button" className="cs-iconbtn" onClick={onClose} aria-label="关闭">×</button>
        </header>

        <div className="cs-drawer__body">
          <nav className="cs-actions" aria-label="路径">
            <button type="button" className="cs-btn cs-btn--ghost cs-btn--tiny" onClick={() => load("/")}>根目录</button>
            {segments.map((segment, index) => (
              <span key={`${segment}-${index}`} className="cs-actions">
                <span className="cs-muted">/</span>
                <button
                  type="button"
                  className="cs-btn cs-btn--ghost cs-btn--tiny"
                  onClick={() => load(crumbPath(path, index + 1))}
                >
                  {segment}
                </button>
              </span>
            ))}
          </nav>

          {error ? <div className="cs-alert cs-alert--error">{error}</div> : null}

          {loading ? (
            <div className="cs-empty">加载中…</div>
          ) : (
            <AdminTable
              columns={columns}
              rows={items}
              rowKey={(row) => row.path}
              empty="该目录下没有子目录"
              onRowClick={(row) => load(row.path)}
            />
          )}

          <div className="cs-actions">
            <button type="button" className="cs-btn cs-btn--primary" disabled={loading} onClick={handlePick}>
              使用此目录
            </button>
            <button type="button" className="cs-btn" onClick={onClose}>关闭</button>
          </div>
        </div>
      </aside>
    </>
  );
}