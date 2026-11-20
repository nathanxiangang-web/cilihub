import { useCallback, useEffect, useRef, useState } from "react";
import { runSync, syncState, type SyncEvent, type SyncRun, type SyncState } from "../api";
import { AdminTable, type AdminColumn } from "../components/AdminTable";
import AdminToast, { type ToastKind, type ToastState } from "../components/AdminToast";
import OpenListPanel from "../components/OpenListPanel";

function statusText(status: string): string {
  if (status === "ok") return "成功";
  if (status === "failed") return "失败";
  if (status === "running") return "进行中";
  return status || "-";
}

function statusClass(status: string): string {
  if (status === "ok") return "cs-badge--ok";
  if (status === "failed") return "cs-badge--danger";
  if (status === "running") return "cs-badge--warn";
  return "cs-badge--muted";
}

export default function SyncPage() {
  const [data, setData] = useState<SyncState | null>(null);
  const [polling, setPolling] = useState(false);
  const [triggering, setTriggering] = useState(false);
  const [toast, setToast] = useState<ToastState | null>(null);
  const targetRunRef = useRef(0);

  const notify = useCallback((message: string, kind: ToastKind = "info") => {
    setToast({ message, kind });
  }, []);

  function load() {
    syncState()
      .then((state) => {
        setData(state);
        if (state.last && state.last.id >= targetRunRef.current && state.last.status !== "running") {
          setPolling(false);
        }
      })
      .catch((err) => notify(err instanceof Error ? err.message : "加载同步状态失败", "error"));
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!polling) return;
    let ticks = 0;
    const timer = window.setInterval(() => {
      ticks += 1;
      load();
      if (ticks >= 40) setPolling(false);
    }, 3000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [polling]);

  async function trigger() {
    if (triggering) return;
    setTriggering(true);
    try {
      const runId = await runSync();
      targetRunRef.current = runId;
      notify(`已触发扫描（run #${runId}），结果稍后刷新`, "ok");
      setPolling(true);
      load();
    } catch (err) {
      notify(err instanceof Error ? err.message : "触发同步失败", "error");
    } finally {
      setTriggering(false);
    }
  }

  const last = data?.last ?? null;

  const runColumns: AdminColumn<SyncRun>[] = [
    { key: "id", title: "序号", width: "70px", render: (row) => `#${row.id}` },
    { key: "started", title: "开始时间", width: "170px", render: (row) => row.started_at || "-" },
    { key: "finished", title: "结束时间", width: "170px", render: (row) => row.finished_at || "-" },
    {
      key: "status",
      title: "状态",
      width: "90px",
      render: (row) => <span className={`cs-badge ${statusClass(row.status)}`}>{statusText(row.status)}</span>,
    },
    { key: "added", title: "新增", width: "70px", align: "right", render: (row) => row.added },
    { key: "missing", title: "缺失", width: "70px", align: "right", render: (row) => row.missing },
    { key: "failed", title: "失败", width: "70px", align: "right", render: (row) => row.failed },
    { key: "message", title: "说明", render: (row) => <span className="cs-muted">{row.message || "-"}</span> },
  ];

  const eventColumns: AdminColumn<SyncEvent>[] = [
    { key: "at", title: "时间", width: "170px", render: (row) => row.at },
    { key: "kind", title: "类型", width: "90px", render: (row) => row.kind },
    { key: "path", title: "文件", render: (row) => <span className="cs-mono">{row.file_path}</span> },
    { key: "message", title: "说明", render: (row) => <span className="cs-muted">{row.message || "-"}</span> },
  ];

  return (
    <div className="cs-page">
      <div className="cs-page__head">
        <div>
          <h2>OpenList 同步</h2>
          <p className="cs-muted">OpenList 只读；扫描结果合并进本地库，运营字段不被覆盖</p>
        </div>
        <div className="cs-actions">
          <button type="button" className="cs-btn" onClick={load}>刷新</button>
          <button type="button" className="cs-btn cs-btn--primary" disabled={triggering} onClick={trigger}>
            {triggering ? "触发中…" : "立即扫描"}
          </button>
        </div>
      </div>

      <OpenListPanel onNotify={notify} />

      <div className="cs-card">
        <div className="cs-card__head">
          <h3>最近一次结果</h3>
          {last ? <span className={`cs-badge ${statusClass(last.status)}`}>{statusText(last.status)}</span> : null}
        </div>
        {last ? (
          <dl className="cs-kv">
            <div><dt>开始</dt><dd>{last.started_at || "-"}</dd></div>
            <div><dt>结束</dt><dd>{last.finished_at || "-"}</dd></div>
            <div><dt>新增</dt><dd>{last.added}</dd></div>
            <div><dt>缺失</dt><dd>{last.missing}</dd></div>
            <div><dt>失败</dt><dd>{last.failed}</dd></div>
            <div><dt>说明</dt><dd>{last.message || "-"}</dd></div>
          </dl>
        ) : (
          <div className="cs-empty">尚未执行同步</div>
        )}
      </div>

      <section className="cs-card cs-card--table">
        <div className="cs-card__head">
          <h3>扫描记录</h3>
          {polling ? <span className="cs-muted">正在等待本轮结果…</span> : null}
        </div>
        <AdminTable columns={runColumns} rows={data?.runs ?? []} rowKey={(row) => row.id} empty="暂无扫描记录" />
      </section>

      <section className="cs-card cs-card--table">
        <div className="cs-card__head">
          <h3>事件</h3>
        </div>
        <AdminTable columns={eventColumns} rows={data?.events ?? []} rowKey={(row) => row.id} empty="暂无同步事件" />
      </section>

      <AdminToast toast={toast} onClose={() => setToast(null)} />
    </div>
  );
}