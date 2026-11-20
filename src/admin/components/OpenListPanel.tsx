import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  openlistSettings,
  saveOpenList,
  testOpenList,
  type OpenListState,
  type OpenListTest,
} from "../api";
import type { ToastKind } from "./AdminToast";
import DirBrowser from "./DirBrowser";

const EMPTY: OpenListState = {
  configured: false,
  origin: "",
  folder: "",
  cdn_host: "",
  username: "",
  password_set: false,
  updated_at: "",
};

export default function OpenListPanel({ onNotify }: {
  onNotify: (message: string, kind?: ToastKind) => void;
}) {
  const [state, setState] = useState<OpenListState>(EMPTY);
  const [origin, setOrigin] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [folder, setFolder] = useState("");
  const [cdnHost, setCdnHost] = useState("");
  const [test, setTest] = useState<OpenListTest | null>(null);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [browsing, setBrowsing] = useState(false);

  const load = useCallback(() => {
    openlistSettings()
      .then((data) => {
        setState(data);
        setOrigin(data.origin ?? "");
        setUsername(data.username ?? "");
        setFolder(data.folder ?? "");
        setCdnHost(data.cdn_host ?? "");
      })
      .catch((err) => onNotify(err instanceof Error ? err.message : "加载 OpenList 配置失败", "error"));
  }, [onNotify]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleTest() {
    if (testing || saving) return;
    if (!origin.trim() || !folder.trim()) {
      onNotify("请填写地址与映射目录", "error");
      return;
    }
    setTesting(true);
    setTest(null);
    try {
      const result = await testOpenList({
        origin: origin.trim(),
        username: username.trim(),
        password,
        folder: folder.trim(),
      });
      setTest(result);
      if (result.ok) {
        onNotify(`连接成功，目录下 ${result.entries} 个条目`, "ok");
      } else {
        onNotify(result.message || "连接失败", "error");
      }
    } catch (err) {
      onNotify(err instanceof Error ? err.message : "测试连接失败", "error");
    } finally {
      setTesting(false);
    }
  }

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    if (saving || testing) return;
    if (!origin.trim() || !folder.trim()) {
      onNotify("请填写地址与映射目录", "error");
      return;
    }
    setSaving(true);
    try {
      await saveOpenList({
        origin: origin.trim(),
        username: username.trim(),
        password: password ? password : undefined,
        folder: folder.trim(),
        cdn_host: cdnHost.trim(),
      });
      setPassword("");
      onNotify("已保存，正在按新目录同步", "ok");
      load();
    } catch (err) {
      onNotify(err instanceof Error ? err.message : "保存失败", "error");
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="cs-card">
      <div className="cs-card__head">
        <h3>OpenList 连接</h3>
        <span className={`cs-badge ${state.configured ? "cs-badge--ok" : "cs-badge--muted"}`}>
          {state.configured ? "已配置" : "未配置"}
        </span>
      </div>

      <form className="cs-form-grid" onSubmit={handleSave}>
        <label className="cs-field">
          <span>地址</span>
          <input placeholder="https://openlist.example.com" value={origin} onChange={(e) => setOrigin(e.target.value)} />
        </label>
        <label className="cs-field">
          <span>账号</span>
          <input autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
        </label>
        <label className="cs-field">
          <span>密码</span>
          <input
            type="password"
            autoComplete="new-password"
            placeholder="留空则保持不变"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <label className="cs-field">
          <span>映射目录</span>
          <input placeholder="/影库" value={folder} onChange={(e) => setFolder(e.target.value)} />
          <button
            type="button"
            className="cs-btn cs-btn--tiny"
            disabled={testing || saving}
            onClick={() => setBrowsing(true)}
          >
            浏览
          </button>
        </label>
        <label className="cs-field">
          <span>CDN 主机</span>
          <input placeholder="可选，如 https://cdn.example.com" value={cdnHost} onChange={(e) => setCdnHost(e.target.value)} />
        </label>
        <div className="cs-form-grid__action">
          <div className="cs-actions">
            <button type="button" className="cs-btn" disabled={testing || saving} onClick={handleTest}>
              {testing ? "测试中…" : "测试连接"}
            </button>
            <button type="submit" className="cs-btn cs-btn--primary" disabled={saving || testing}>
              {saving ? "保存中…" : "保存并同步"}
            </button>
          </div>
        </div>
      </form>

      {test ? (
        <dl className="cs-kv">
          <div>
            <dt>结果</dt>
            <dd>
              <span className={`cs-badge ${test.ok ? "cs-badge--ok" : "cs-badge--danger"}`}>
                {test.ok ? "成功" : "失败"}
              </span>
            </dd>
          </div>
          <div><dt>状态码</dt><dd>{test.status || "-"}</dd></div>
          <div><dt>耗时</dt><dd>{test.elapsed_ms} ms</dd></div>
          <div><dt>条目数</dt><dd>{test.entries}</dd></div>
          <div><dt>原因</dt><dd>{test.message || "-"}</dd></div>
        </dl>
      ) : null}

      <dl className="cs-kv">
        <div><dt>已配置</dt><dd>{state.configured ? "是" : "否"}</dd></div>
        <div><dt>密码</dt><dd>{state.password_set ? "已保存" : "未设置"}</dd></div>
        <div><dt>更新时间</dt><dd>{state.updated_at || "-"}</dd></div>
      </dl>

      {browsing ? (
        <DirBrowser
          initialPath={folder}
          onPick={(path) => setFolder(path)}
          onClose={() => setBrowsing(false)}
        />
      ) : null}
    </section>
  );
}