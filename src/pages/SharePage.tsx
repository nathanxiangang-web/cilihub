import { useEffect, useRef, useState } from "react";
import Hls from "hls.js";
import { api, LOCAL } from "../api";
import type { ShareInfo, ShareState } from "../api";

const STATE_TEXT: Record<Exclude<ShareState, "active">, { title: string; sub: string }> = {
  missing: { title: "分享不存在", sub: "分享链接无效，可能已被删除或从未创建。" },
  revoked: { title: "分享已取消", sub: "该分享已被创建者或管理员取消。" },
  expired: { title: "分享已过期", sub: "该分享已超过有效期，请联系分享者重新生成。" },
};

function StateBlock({ title, sub, action, onAction }: {
  title: string; sub: string; action?: string; onAction?: () => void;
}) {
  return (
    <div className="mt-16 text-center">
      <div className="text-[18px] font-bold text-on">{title}</div>
      <p className="mt-2 text-[12.5px] text-mut leading-relaxed">{sub}</p>
      {action && onAction && (
        <button onClick={onAction}
          className="mt-6 h-10 px-6 rounded-(--r-ctl) bg-primary text-on-primary text-[13px] font-bold pressable">
          {action}
        </button>
      )}
    </div>
  );
}

/** `/s/{code}` 分享落地页：有效则播放，需提取码则先校验，失效则给出原因。 */
export default function SharePage() {
  const code = decodeURIComponent(location.pathname.replace(/^\/s\/?/, "").replace(/\/+$/, ""));
  const videoRef = useRef<HTMLVideoElement>(null);
  const [info, setInfo] = useState<ShareInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState("");
  const [pwd, setPwd] = useState("");
  const [pwdErr, setPwdErr] = useState("");
  const [unlocked, setUnlocked] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!code) return;
    let alive = true;
    setLoading(true);
    api.shareInfo(code)
      .then((res) => { if (alive) { setInfo(res); setErr(""); } })
      .catch((e: Error) => { if (alive) setErr(e.message || "加载分享信息失败"); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [code]);

  const playable = !!info && info.state === "active" && (!info.need_password || unlocked);
  /* 提取码通过查询参数随播放请求提交，服务端每次都会校验 */
  const src = playable
    ? `${LOCAL}/share/${encodeURIComponent(code)}/1${pwd ? `?pwd=${encodeURIComponent(pwd)}` : ""}`
    : "";

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !src) return undefined;
    if (Hls.isSupported()) {
      const hls = new Hls({ maxBufferLength: 30, maxMaxBufferLength: 60, backBufferLength: 30 });
      hls.loadSource(src);
      hls.attachMedia(video);
      return () => hls.destroy();
    }
    video.src = src;
    return () => { video.removeAttribute("src"); video.load(); };
  }, [src]);

  const home = () => { location.href = "/"; };

  const unlock = async () => {
    const value = pwd.trim();
    if (!value || busy) return;
    setBusy(true);
    setPwdErr("");
    try {
      await api.shareUnlock(code, value);
      setPwd(value);
      setUnlocked(true);
    } catch (e) {
      setPwdErr((e as Error).message || "提取码不正确");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="h-full w-full overflow-y-auto bg-bg text-on">
      <header className="sticky top-0 z-10 flex items-center gap-2 h-14 px-4 bg-bg/90 backdrop-blur border-b border-s3">
        <button onClick={home} className="text-[12.5px] text-mut pressable">返回首页</button>
        <span className="ml-auto text-[12px] text-dim">分享</span>
      </header>

      <main className="mx-auto w-full max-w-[860px] px-4 py-6">
        {loading ? (
          <div className="py-20 text-center text-[12.5px] text-mut">加载中…</div>
        ) : err ? (
          <StateBlock title="加载失败" sub={err} action="重试" onAction={() => location.reload()} />
        ) : !info ? (
          <StateBlock {...STATE_TEXT.missing} />
        ) : playable ? (
          <>
            <div className="rounded-(--r-md) overflow-hidden bg-black">
              <video id="share-video" ref={videoRef} controls playsInline
                className="w-full aspect-video bg-black" />
            </div>
            <h1 className="mt-4 text-[17px] font-bold text-on">{info.title || "未命名作品"}</h1>
            <p className="mt-1 text-[11.5px] text-mut">共 {info.episode_count} 集 · 分享码 {code}</p>
          </>
        ) : info.state === "active" ? (
          <>
            <h1 className="text-[17px] font-bold text-on">{info.title || "未命名作品"}</h1>
            <p className="mt-1 text-[11.5px] text-mut">共 {info.episode_count} 集</p>
            <div className="mt-6 rounded-(--r-md) bg-s2 p-4">
              <div className="text-[13px] font-semibold text-on">此分享需要提取码</div>
              <div className="mt-3 flex gap-2">
                <input id="share-unlock-input" value={pwd} onChange={(e) => setPwd(e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") unlock(); }}
                  placeholder="请输入提取码"
                  className="flex-1 h-10 px-3.5 rounded-(--r-sm) bg-s4 text-[13px] text-on placeholder-mut outline-none focus:ring-2 focus:ring-[var(--p-primary)]" />
                <button id="share-unlock-submit" onClick={unlock} disabled={busy}
                  className="h-10 px-5 rounded-(--r-ctl) bg-primary text-on-primary text-[13px] font-bold pressable disabled:opacity-60">
                  {busy ? "校验中…" : "观看"}
                </button>
              </div>
              {pwdErr && <div className="mt-2 text-[11.5px] text-danger">{pwdErr}</div>}
            </div>
          </>
        ) : (
          <StateBlock {...STATE_TEXT[info.state]} action="返回首页" onAction={home} />
        )}
      </main>
    </div>
  );
}