import { useEffect, useState } from "react";
import { overview, type Overview } from "../api";

function statusText(status: string): string {
  if (status === "ok") return "成功";
  if (status === "failed") return "失败";
  if (status === "running") return "进行中";
  return status || "-";
}

export default function OverviewPage() {
  const [data, setData] = useState<Overview | null>(null);
  const [error, setError] = useState("");

  function load() {
    setError("");
    overview()
      .then(setData)
      .catch((err) => setError(err instanceof Error ? err.message : "加载概览失败"));
  }

  useEffect(() => {
    load();
  }, []);

  const last = data?.last_sync ?? null;

  return (
    <div className="cs-page">
      <div className="cs-page__head">
        <div>
          <h2>运行概览</h2>
          <p className="cs-muted">作品、上架、缺失与最近一次同步结果</p>
        </div>
        <button type="button" className="cs-btn" onClick={load}>刷新</button>
      </div>

      {error ? <div className="cs-alert cs-alert--error">{error}</div> : null}

      <div className="cs-stats">
        <div className="cs-stat">
          <span className="cs-stat__label">作品总数</span>
          <strong className="cs-stat__value">{data?.works ?? "-"}</strong>
        </div>
        <div className="cs-stat">
          <span className="cs-stat__label">已上架</span>
          <strong className="cs-stat__value">{data?.visible ?? "-"}</strong>
        </div>
        <div className="cs-stat">
          <span className="cs-stat__label">缺失文件</span>
          <strong className={`cs-stat__value${data && data.missing > 0 ? " is-danger" : ""}`}>
            {data?.missing ?? "-"}
          </strong>
        </div>
      </div>

      <div className="cs-card">
        <div className="cs-card__head">
          <h3>最近同步</h3>
          {last ? <span className={`cs-badge ${last.status === "ok" ? "cs-badge--ok" : last.status === "failed" ? "cs-badge--danger" : "cs-badge--muted"}`}>{statusText(last.status)}</span> : null}
        </div>
        {last ? (
          <dl className="cs-kv">
            <div><dt>时间</dt><dd>{last.at || "-"}</dd></div>
            <div><dt>新增</dt><dd>{last.added}</dd></div>
            <div><dt>缺失</dt><dd>{last.missing}</dd></div>
          </dl>
        ) : (
          <div className="cs-empty">尚未执行同步</div>
        )}
      </div>
    </div>
  );
}