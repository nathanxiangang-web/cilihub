import { useState, type FormEvent } from "react";
import { login, type AdminUser } from "./api";

export default function LoginGate({ onSuccess }: { onSuccess: (user: AdminUser) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    if (!username.trim() || !password) {
      setError("请填写用户名和密码");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const user = await login(username.trim(), password);
      onSuccess(user);
    } catch (err) {
      setError(err instanceof Error ? err.message : "登录失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="cs-login">
      <form className="cs-login__card" onSubmit={submit}>
        <div className="cs-login__brand">
          <span className="cs-sidebar__mark">磁</span>
          <div>
            <h1>磁力短剧 · 管理后台</h1>
            <p>请使用管理员账号登录</p>
          </div>
        </div>
        <label className="cs-field">
          <span>用户名</span>
          <input
            autoFocus
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </label>
        <label className="cs-field">
          <span>密码</span>
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error ? <div className="cs-login__error">{error}</div> : null}
        <button className="cs-btn cs-btn--primary cs-login__submit" type="submit" disabled={busy}>
          {busy ? "登录中…" : "登录"}
        </button>
      </form>
    </div>
  );
}