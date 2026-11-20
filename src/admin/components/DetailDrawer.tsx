import { useEffect, useRef, useState } from "react";
import Hls from "hls.js";
import {
  assignFiles,
  patchWork,
  reorderEpisodes,
  work,
  type Category,
  type WorkDetail,
  type WorkFile,
} from "../api";
import type { ToastKind } from "./AdminToast";

function formatSize(bytes: number): string {
  if (!bytes) return "-";
  const mb = bytes / (1024 * 1024);
  return mb >= 1024 ? `${(mb / 1024).toFixed(2)} GB` : `${mb.toFixed(1)} MB`;
}

function PreviewPlayer({ fileId }: { fileId: number | null }) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const hlsRef = useRef<Hls | null>(null);
  const [mode, setMode] = useState<"direct" | "hls" | "failed">("direct");

  function destroyHls() {
    hlsRef.current?.destroy();
    hlsRef.current = null;
  }

  useEffect(() => {
    destroyHls();
    setMode("direct");
  }, [fileId]);

  useEffect(() => destroyHls, []);

  function fallbackToHls() {
    if (!fileId || mode !== "direct") {
      setMode("failed");
      return;
    }
    const video = videoRef.current;
    if (!video || !Hls.isSupported()) {
      setMode("failed");
      return;
    }
    destroyHls();
    setMode("hls");
    const hls = new Hls();
    hlsRef.current = hls;
    hls.on(Hls.Events.ERROR, (_event, data) => {
      if (data.fatal) setMode("failed");
    });
    hls.loadSource(`/media/library/${fileId}`);
    hls.attachMedia(video);
  }

  if (!fileId) return <div className="cs-player cs-player--empty">暂无可预览的集</div>;
  return (
    <div className="cs-player">
      <video
        key={fileId}
        ref={videoRef}
        src={`/media/library/${fileId}`}
        controls
        playsInline
        onError={fallbackToHls}
      />
      {mode === "failed" ? (
        <div className="cs-player__tip">浏览器无法直接播放该链接，请确认源文件可用。</div>
      ) : null}
    </div>
  );
}

export default function DetailDrawer({
  workId,
  open,
  categories,
  workOptions,
  onClose,
  onChanged,
  notify,
}: {
  workId: number | null;
  open: boolean;
  categories: Category[];
  workOptions: { id: number; title: string }[];
  onClose: () => void;
  onChanged: () => void;
  notify: (message: string, kind: ToastKind) => void;
}) {
  const [detail, setDetail] = useState<WorkDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [title, setTitle] = useState("");
  const [categoryId, setCategoryId] = useState(0);
  const [visible, setVisible] = useState(true);
  const [pinned, setPinned] = useState(false);
  const [selectedFile, setSelectedFile] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const notifyRef = useRef(notify);
  notifyRef.current = notify;

  useEffect(() => {
    if (!open || workId == null) return;
    let alive = true;
    setLoading(true);
    work(workId)
      .then((data) => {
        if (!alive) return;
        setDetail(data);
        setTitle(data.work.title);
        setCategoryId(data.work.category_id);
        setVisible(data.work.visible);
        setPinned(data.work.pinned);
        const first = data.files.find((f) => f.present) ?? data.files[0];
        setSelectedFile(first ? first.file_id : null);
      })
      .catch((err) => {
        if (alive) notifyRef.current(err instanceof Error ? err.message : "加载作品失败", "error");
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [open, workId]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  async function refresh(id: number) {
    const data = await work(id);
    setDetail(data);
    setTitle(data.work.title);
    setCategoryId(data.work.category_id);
    setVisible(data.work.visible);
    setPinned(data.work.pinned);
    setSelectedFile((prev) => (data.files.some((f) => f.file_id === prev) ? prev : (data.files[0]?.file_id ?? null)));
  }

  async function save() {
    if (workId == null || busy) return;
    const nextTitle = title.trim();
    if (!nextTitle) {
      notify("标题不能为空", "error");
      return;
    }
    setBusy(true);
    try {
      const updated = await patchWork(workId, {
        title: nextTitle,
        category_id: categoryId,
        visible,
        pinned,
      });
      setDetail((prev) => (prev ? { ...prev, work: updated } : prev));
      onChanged();
      notify("已保存", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "保存失败", "error");
    } finally {
      setBusy(false);
    }
  }

  function orderedFiles(): WorkFile[] {
    return detail ? [...detail.files].sort((a, b) => a.episode_no - b.episode_no) : [];
  }

  async function moveFile(index: number, dir: -1 | 1) {
    if (workId == null) return;
    const files = orderedFiles();
    const target = index + dir;
    if (target < 0 || target >= files.length) return;
    const ids = files.map((f) => f.file_id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    try {
      await reorderEpisodes(workId, ids);
      await refresh(workId);
      onChanged();
      notify("集顺序已更新", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "调整集序失败", "error");
    }
  }

  async function moveToWork(fileId: number, targetWorkId: number) {
    if (!targetWorkId || targetWorkId === workId) return;
    try {
      await assignFiles(targetWorkId, [fileId]);
      if (workId != null) await refresh(workId);
      onChanged();
      notify("已移动到目标作品", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "移动失败", "error");
    }
  }

  if (!open) return null;

  const files = orderedFiles();
  const activeFile = files.find((f) => f.file_id === selectedFile) ?? files[0] ?? null;

  return (
    <>
      <div className="cs-drawer__overlay" onClick={onClose} />
      <aside className="cs-drawer" role="dialog" aria-label="作品详情">
        <header className="cs-drawer__head">
          <div>
            <h2>{detail?.work.title ?? "作品详情"}</h2>
            <span className="cs-muted">ID {workId ?? "-"}</span>
          </div>
          <button type="button" className="cs-iconbtn" onClick={onClose} aria-label="关闭">×</button>
        </header>

        <div className="cs-drawer__body">
          {loading && !detail ? <div className="cs-empty">加载中…</div> : null}

          {detail ? (
            <>
              <section className="cs-section">
                <h3>预览</h3>
                <PreviewPlayer fileId={activeFile ? activeFile.file_id : null} />
                <div className="cs-filemeta">
                  <span className="cs-mono">{activeFile ? activeFile.file_name : "未选择集"}</span>
                  <span className="cs-muted">
                    {activeFile ? `第 ${activeFile.episode_no} 集 · ${formatSize(activeFile.size_bytes)}` : ""}
                  </span>
                </div>
              </section>

              <section className="cs-section">
                <h3>编辑</h3>
                <div className="cs-form-grid">
                  <label className="cs-field">
                    <span>标题</span>
                    <input value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} />
                  </label>
                  <label className="cs-field">
                    <span>分类</span>
                    <select value={categoryId} onChange={(e) => setCategoryId(Number(e.target.value))}>
                      {categories.map((c) => (
                        <option key={c.id} value={c.id}>{c.name}</option>
                      ))}
                    </select>
                  </label>
                  <label className="cs-check">
                    <input type="checkbox" checked={visible} onChange={(e) => setVisible(e.target.checked)} />
                    <span>上架</span>
                  </label>
                  <label className="cs-check">
                    <input type="checkbox" checked={pinned} onChange={(e) => setPinned(e.target.checked)} />
                    <span>首页置顶</span>
                  </label>
                </div>
                <div className="cs-actions">
                  <button type="button" className="cs-btn cs-btn--primary" disabled={busy} onClick={save}>
                    {busy ? "保存中…" : "保存修改"}
                  </button>
                </div>
              </section>

              <section className="cs-section">
                <h3>集列表（{files.length}）</h3>
                {files.length ? (
                  <div className="cs-episodes">
                    {files.map((file, index) => (
                      <div
                        key={file.file_id}
                        className={`cs-episode${file.file_id === activeFile?.file_id ? " is-active" : ""}`}
                      >
                        <button
                          type="button"
                          className="cs-episode__main"
                          onClick={() => setSelectedFile(file.file_id)}
                        >
                          <span className="cs-episode__no">第 {file.episode_no} 集</span>
                          <span className="cs-episode__name">{file.file_name}</span>
                          <span className={`cs-badge ${file.present ? "cs-badge--ok" : "cs-badge--danger"}`}>
                            {file.present ? "正常" : "缺失"}
                          </span>
                        </button>
                        <div className="cs-episode__ops">
                          <button type="button" className="cs-btn cs-btn--tiny" disabled={index === 0} onClick={() => moveFile(index, -1)}>上移</button>
                          <button type="button" className="cs-btn cs-btn--tiny" disabled={index === files.length - 1} onClick={() => moveFile(index, 1)}>下移</button>
                          <select
                            className="cs-select--tiny"
                            value=""
                            onChange={(e) => {
                              const target = Number(e.target.value);
                              if (target) void moveToWork(file.file_id, target);
                            }}
                          >
                            <option value="">移动至…</option>
                            {workOptions
                              .filter((w) => w.id !== workId)
                              .map((w) => (
                                <option key={w.id} value={w.id}>{w.title}</option>
                              ))}
                          </select>
                        </div>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="cs-empty">该作品暂无文件</div>
                )}
              </section>
            </>
          ) : null}
        </div>
      </aside>
    </>
  );
}