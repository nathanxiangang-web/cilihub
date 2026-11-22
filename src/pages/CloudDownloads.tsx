import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { IClose, ICloudDownload } from "../icons";
import { useStore } from "../store";
import { cloudEndpoint as endpoint, cloudRequest as request, queueCloudDownload } from "../cloudDownloadsApi";

type CloudFile = { name: string; size: number; url: string };
type CloudTask = { id: number; title: string; state: string; message: string; progress: number | null; files: CloudFile[] };
type CloudList = { tasks: CloudTask[]; enabled: boolean };

const terminal = new Set(["ready", "failed", "no_mp4"]);
const button = "rounded-(--r-ctl) bg-s3 px-4 py-2 text-sm font-semibold text-on pressable disabled:opacity-50";

function PrivateDownloads({ active }: { active: boolean }) {
  const [data, setData] = useState<CloudList>();
  const [cursor, setCursor] = useState(0);
  const [magnet, setMagnet] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [listError, setListError] = useState("");
  const [file, setFile] = useState<CloudFile>();
  const [videoError, setVideoError] = useState(false);
  const listing = useRef<AbortController | null>(null);
  const submitting = useRef<AbortController | null>(null);

  const refresh = useCallback(async (before: number) => {
    listing.current?.abort();
    const job = new AbortController();
    listing.current = job;
    try {
      const list = await request<CloudList>(`${endpoint}?before=${before}`, { signal: job.signal });
      if (!job.signal.aborted) { setData(list); setListError(""); }
    } catch (error) {
      if (!job.signal.aborted) setListError(error instanceof Error ? error.message : "任务读取失败");
    }
  }, []);

  useEffect(() => {
    if (!active) return;
    void refresh(cursor);
    const timer = window.setInterval(() => void refresh(cursor), 12000);
    return () => { window.clearInterval(timer); listing.current?.abort(); };
  }, [active, cursor, refresh]);
  useEffect(() => () => submitting.current?.abort(), []);
  useEffect(() => { if (!active) setFile(undefined); }, [active]);

  async function enqueue(link: string, title?: string) {
    if (!data?.enabled || submitting.current || !link.trim()) return false;
    const job = new AbortController();
    submitting.current = job;
    setBusy(true);
    setNotice("");
    try {
      const result = await queueCloudDownload(link, title, job.signal);
      if (job.signal.aborted) return false;
      setNotice(result.created ? "已登记任务，115 下载需要时间，过几分钟后再来刷新。" : "这条磁力链接已有任务，进度可在列表查看。");
      if (cursor === 0) void refresh(0);
      else setCursor(0);
      return true;
    } catch (error) {
      if (!job.signal.aborted) setNotice(error instanceof Error ? error.message : "提交失败");
      return false;
    } finally {
      if (submitting.current === job) submitting.current = null;
      if (!job.signal.aborted) setBusy(false);
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (await enqueue(magnet)) setMagnet("");
  }

  return <>
    <form onSubmit={submit} className="rounded-(--r-lg) bg-s1 p-4 sm:p-5">
      <label htmlFor="cloud-magnet" className="mb-2 block text-sm font-semibold">磁力链接</label>
      <div className="flex flex-col gap-2 sm:flex-row">
        <input id="cloud-magnet" type="text" inputMode="url" autoComplete="off" spellCheck={false}
          placeholder="magnet:?xt=urn:btih:…" maxLength={8192} required value={magnet}
          onChange={(event) => setMagnet(event.target.value)}
          className="min-w-0 flex-1 rounded-(--r-ctl) bg-s3 px-3 py-2 text-sm text-on" />
        <button type="submit" disabled={!data?.enabled || busy || !magnet.trim()}
          className="rounded-(--r-ctl) bg-primary px-4 py-2 text-sm font-semibold text-on-primary pressable disabled:opacity-50">
          {busy ? "提交中…" : "提交云下载"}
        </button>
      </div>
      <p className="mt-2 text-xs text-mut">任务仅当前账号可见。下载完成后显示 MP4 文件。</p>
      {data && !data.enabled && <p className="mt-2 text-sm text-mut">此实例尚未启用 115 云下载。</p>}
    </form>

    {file && <section className="mt-4 rounded-(--r-lg) bg-s1 p-4" aria-label="视频预览">
      <div className="mb-3 flex items-center gap-3">
        <h2 className="flex-1 break-all text-sm font-semibold">{file.name}</h2>
        <button onClick={() => setFile(undefined)} aria-label="关闭视频" className={button}><IClose size={16} /></button>
      </div>
      <video key={file.url} src={file.url} controls autoPlay playsInline preload="metadata"
        onError={() => setVideoError(true)} className="max-h-[55vh] w-full rounded-(--r-ctl) bg-black" />
      {videoError && <p className="mt-2 text-sm text-mut" role="status">115 视频暂时无法播放，可以稍后重新打开。</p>}
    </section>}

    <div className="mb-3 mt-6 flex items-center justify-between">
      <h2 className="text-base font-bold">我的任务</h2>
      <button className={button} onClick={() => void refresh(cursor)}>刷新</button>
    </div>
    {notice && <p className="mb-2 text-sm" role="status">{notice}</p>}
    {listError && <p className="text-sm text-mut" role="status">{listError}</p>}
    {!data && !listError && <p className="text-sm text-mut">正在读取任务…</p>}
    {data?.tasks.length === 0 && <p className="rounded-(--r-lg) bg-s1 p-4 text-sm text-mut">暂无任务</p>}
    <ul className="grid gap-2">
      {data?.tasks.map(task => <li key={task.id} className="rounded-(--r-lg) bg-s1 p-4">
        <h3 className="break-all text-sm font-semibold">{task.title}</h3>
        <p className="mt-1 text-sm text-mut">
          {task.message || "排队中"}{task.progress !== null && ` · ${Math.round(task.progress)}%`}
        </p>
        {task.progress !== null && !terminal.has(task.state) &&
          <progress className="mt-2 h-1.5 w-full accent-[var(--p-primary)]" value={task.progress} max={100} aria-label="115 下载进度" />}
        {task.files.map(video => <button key={video.url}
          onClick={() => { setVideoError(false); setFile(video); }}
          className="mt-2 block w-full break-all rounded-(--r-ctl) bg-s3 p-2.5 text-left text-sm pressable">
          播放 · {video.name}
        </button>)}
      </li>)}
    </ul>
    <div className="mt-3 flex gap-2">
      {cursor > 0 && <button className={button} onClick={() => setCursor(0)}>返回最新</button>}
      {data?.tasks.length === 100 && <button className={button}
        onClick={() => { setCursor(data.tasks[99].id); setData(undefined); }}>更早的任务</button>}
    </div>
  </>;
}

export default function CloudDownloads({ onBack, active }: { onBack: () => void; active: boolean }) {
  const { user, setLoginOpen } = useStore();
  return <section className="absolute inset-0 scroll-y bg-bg text-on">
    <div className="mx-auto max-w-3xl px-4 pb-20 pt-[calc(var(--sat,0px)+18px)] sm:px-6">
      <header className="mb-5 flex items-center gap-3">
        <button className={button} onClick={onBack}>返回</button>
        <ICloudDownload size={23} strokeWidth={1.7} />
        <h1 className="text-xl font-bold">云下载</h1>
      </header>
      {user ? <PrivateDownloads key={user.id} active={active} /> :
        <div className="rounded-(--r-lg) bg-s1 p-5">
          <p className="mb-3 text-sm text-mut">登录后即可提交磁力链接并查看私人下载。</p>
          <button className={button} onClick={() => setLoginOpen(true)}>登录</button>
        </div>}
    </div>
  </section>;
}
