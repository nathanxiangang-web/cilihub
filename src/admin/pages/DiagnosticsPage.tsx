import { useState, type FormEvent } from "react";
import { diagnostics, type Diagnostics } from "../api";

function shorten(url: string, max = 72): string {
  if (!url) return "-";
  const clean = url.split("?")[0];
  return clean.length > max ? `${clean.slice(0, max)}…` : clean;
}

export default function DiagnosticsPage() {
  const [fileId, setFileId] = useState("");
  const [lastId, setLastId] = useState(0);
  const [data, setData] = useState<Diagnostics | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [showPlayer, setShowPlayer] = useState(false);
  const [playerError, setPlayerError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    const id = Number(fileId);
    if (!Number.isInteger(id) || id <= 0) {
      setError("请填写有效的 file_id");
      return;
    }
    setBusy(true);
    setError("");
    setPlayerError("");
    setShowPlayer(false);
    try {
      setData(await diagnostics(id));
      setLastId(id);
    } catch (err) {
      setData(null);
      setError(err instanceof Error ? err.message : "诊断失败");
    } finally {
      setBusy(false);
    }
  }

  const openlist = data?.openlist;
  const playback = data?.playback;

  return (
    <div className="cs-page">
      <div className="cs-page__head">
        <div>
          <h2>播放诊断</h2>
          <p className="cs-muted">区分取链失败、站点 302 与浏览器播放失败</p>
        </div>
      </div>

      <form className="cs-card cs-inline-form" onSubmit={submit}>
        <input
          className="cs-input"
          placeholder="输入 file_id"
          inputMode="numeric"
          value={fileId}
          onChange={(e) => setFileId(e.target.value)}
        />
        <button type="submit" className="cs-btn cs-btn--primary" disabled={busy}>
          {busy ? "诊断中…" : "开始诊断"}
        </button>
      </form>

      {error ? <div className="cs-alert cs-alert--error">{error}</div> : null}

      {data ? (
        <div className="cs-diag">
          <section className="cs-card">
            <div className="cs-card__head"><h3>目录快照</h3></div>
            <div className="cs-diag__line">
              <span className={`cs-badge ${data.catalog.in_snapshot ? "cs-badge--ok" : "cs-badge--muted"}`}>
                {data.catalog.in_snapshot ? "在库中" : "不在快照"}
              </span>
            </div>
          </section>

          <section className="cs-card">
            <div className="cs-card__head"><h3>OpenList 取链</h3></div>
            <dl className="cs-kv">
              <div><dt>结果</dt><dd><span className={`cs-badge ${openlist?.ok ? "cs-badge--ok" : "cs-badge--danger"}`}>{openlist?.ok ? "成功" : "失败"}</span></dd></div>
              <div><dt>HTTP</dt><dd>{openlist?.status || "-"}</dd></div>
              <div><dt>耗时</dt><dd>{openlist ? `${openlist.elapsed_ms} ms` : "-"}</dd></div>
              <div><dt>原因</dt><dd>{openlist?.reason || "-"}</dd></div>
            </dl>
          </section>

          <section className="cs-card">
            <div className="cs-card__head"><h3>站点播放</h3></div>
            <dl className="cs-kv">
              <div>
                <dt>模式</dt>
                <dd><span className={`cs-badge ${playback?.mode === "302" ? "cs-badge--ok" : "cs-badge--danger"}`}>{playback?.mode || "-"}</span></dd>
              </div>
              <div><dt>地址</dt><dd className="cs-mono" title="已隐藏签名参数">{shorten(playback?.url ?? "")}</dd></div>
            </dl>
            <div className="cs-actions">
              <button type="button" className="cs-btn" onClick={() => { setShowPlayer((v) => !v); setPlayerError(""); }}>
                {showPlayer ? "收起播放测试" : "浏览器播放测试"}
              </button>
              <a className="cs-btn" href={`/media/library/${lastId}`} target="_blank" rel="noreferrer">
                新窗口打开
              </a>
            </div>
            {showPlayer ? (
              <div className="cs-player cs-player--diag">
                <video
                  key={lastId}
                  src={`/media/library/${lastId}`}
                  controls
                  playsInline
                  onError={() => setPlayerError("浏览器无法解码或加载该视频流")}
                />
                {playerError ? <div className="cs-player__tip">{playerError}</div> : null}
              </div>
            ) : null}
          </section>
        </div>
      ) : null}
    </div>
  );
}