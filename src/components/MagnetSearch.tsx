import { useEffect, useRef, useState } from "react";
import { cloudEndpoint, cloudRequest, queueCloudDownload } from "../cloudDownloadsApi";

type Hit = { id: string; title: string; filename: string; magnet: string; size: string };
type SearchStatus = { state: string; items: Hit[] };

export default function MagnetSearch({ query, onRunningChange }: {
  query: string;
  onRunningChange: (running: boolean) => void;
}) {
  const [running, setRunning] = useState(true);
  const [searched, setSearched] = useState(false);
  const [items, setItems] = useState<Hit[]>([]);
  const [notice, setNotice] = useState("");
  const [added, setAdded] = useState<Set<string>>(new Set());
  const [queueNotice, setQueueNotice] = useState("");
  const [queueing, setQueueing] = useState<string>();
  const submission = useRef<AbortController | null>(null);

  useEffect(() => {
    const job = new AbortController();
    onRunningChange(true);
    const endpoint = cloudEndpoint + "/search";
    async function search() {
      try {
        const { key } = await cloudRequest<{ key: string }>(endpoint, {
          method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ query }), signal: job.signal,
        });
        const deadline = Date.now() + 180000;
        while (!job.signal.aborted) {
          const result = await cloudRequest<SearchStatus>(`${endpoint}/${encodeURIComponent(key)}`, { signal: job.signal });
          if (job.signal.aborted) return;
          setItems(result.items);
          if (result.state !== "running") {
            if (result.state === "done") setSearched(true);
            else setNotice("磁力搜索中断，可以稍后重试。");
            return;
          }
          if (Date.now() >= deadline) {
            setNotice("搜索时间较长，可以先使用已返回的结果，或稍后再试。");
            return;
          }
          await new Promise(resolve => window.setTimeout(resolve, 3000));
        }
      } catch (error) {
        if (!job.signal.aborted) setNotice(error instanceof Error ? error.message : "磁力搜索失败");
      } finally {
        if (!job.signal.aborted) {
          setRunning(false);
          onRunningChange(false);
        }
      }
    }
    void search();
    return () => {
      job.abort();
      submission.current?.abort();
      onRunningChange(false);
    };
  }, [query, onRunningChange]);

  async function choose(item: Hit) {
    if (submission.current || added.has(item.id)) return;
    const job = new AbortController();
    submission.current = job;
    setQueueing(item.id);
    setQueueNotice("");
    try {
      const result = await queueCloudDownload(item.magnet, item.title, job.signal);
      if (job.signal.aborted) return;
      setAdded(prev => new Set(prev).add(item.id));
      setQueueNotice(result.created ? "已加入我的云下载，过几分钟可在我的任务里查看进度。" : "这个资源已在我的云下载中。");
    } catch (error) {
      if (!job.signal.aborted) setQueueNotice(error instanceof Error ? error.message : "加入云下载失败");
    } finally {
      submission.current = null;
      if (!job.signal.aborted) setQueueing(undefined);
    }
  }

  return <section className="mb-4 rounded-(--r-lg) bg-s1 p-4 sm:p-5" aria-label="磁力搜索结果">
    <h2 className="break-all text-sm font-semibold">「{query}」的磁力结果</h2>
    {running && <p className="mt-2 text-xs text-mut" role="status">正在逐条查询资源，结果会陆续出现。</p>}
    {notice && <p className="mt-2 text-sm text-mut" role="status">{notice}</p>}
    {searched && items.length===0 && <p className="mt-2 text-sm text-mut">没有找到符合条件的资源。</p>}
    {!!items.length && <ul className="mt-3 grid gap-2">
      {items.map(item => (
        <li key={item.id} className="flex flex-wrap items-center gap-2 rounded-(--r-ctl) bg-s3 p-3">
          <div className="min-w-0 flex-1 break-all">
            <div className="text-sm font-semibold">{item.title}</div>
            {item.filename && item.filename!==item.title && <div className="mt-1 text-xs text-mut">{item.filename}</div>}
            {item.size && <div className="mt-1 text-xs text-mut">{item.size}</div>}
          </div>
          <button type="button" disabled={!!queueing || added.has(item.id)}
            className="rounded-(--r-ctl) bg-s1 px-3 py-2 text-xs font-semibold text-on pressable disabled:opacity-50"
            onClick={() => void choose(item)}>
            {added.has(item.id) ? "已加入" : queueing === item.id ? "提交中…" : "加入我的云下载"}
          </button>
        </li>
      ))}
    </ul>}
    {queueNotice && <p className="mt-3 text-sm text-mut" role="status">{queueNotice}</p>}
  </section>;
}
