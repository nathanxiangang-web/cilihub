import { useEffect, useState } from "react";
import "./admin.css";
import {
  logout,
  onUnauthorized,
  session,
  type AdminUser,
} from "./api";
import AdminSidebar, { ADMIN_MENU, type AdminPageKey } from "./AdminSidebar";
import LoginGate from "./LoginGate";
import OverviewPage from "./pages/OverviewPage";
import LibraryPage from "./pages/LibraryPage";
import CategoriesHomePage from "./pages/CategoriesHomePage";
import SyncPage from "./pages/SyncPage";
import DiagnosticsPage from "./pages/DiagnosticsPage";
import UsersPage from "./pages/UsersPage";
import SharesPage from "./pages/SharesPage";
import SystemPage from "./pages/SystemPage";

const TITLES: Record<AdminPageKey, string> = {
  overview: "概览",
  library: "影视库",
  categories: "分类与首页",
  sync: "OpenList 同步",
  diagnostics: "播放诊断",
  users: "用户",
  shares: "分享",
  system: "系统与审计",
};

export default function AdminApp() {
  const [user, setUser] = useState<AdminUser | null | undefined>(undefined);
  const [page, setPage] = useState<AdminPageKey>("overview");

  useEffect(() => {
    document.documentElement.classList.add("cs-admin-mode");
    return () => document.documentElement.classList.remove("cs-admin-mode");
  }, []);

  useEffect(() => {
    onUnauthorized(() => setUser(null));
    let alive = true;
    session()
      .then((current) => {
        if (alive) setUser(current);
      })
      .catch(() => {
        if (alive) setUser(null);
      });
    return () => {
      alive = false;
      onUnauthorized(null);
    };
  }, []);

  async function signOut() {
    try {
      await logout();
    } catch {
      /* 会话可能已过期，直接回登录表单 */
    }
    setUser(null);
  }

  if (user === undefined) {
    return <div className="cs-admin-boot">加载管理后台…</div>;
  }

  if (!user) {
    return <LoginGate onSuccess={setUser} />;
  }

  return (
    <div className="cs-admin">
      <AdminSidebar current={page} onSelect={setPage} />
      <div className="cs-main">
        <header className="cs-topbar">
          <h1>{TITLES[page]}</h1>
          <div className="cs-topbar__right">
            <span className="cs-topbar__user">{user.username}</span>
            <button type="button" className="cs-btn cs-btn--ghost" onClick={signOut}>
              退出登录
            </button>
          </div>
        </header>
        <div className="cs-content">
          {page === "overview" ? <OverviewPage /> : null}
          {page === "library" ? <LibraryPage /> : null}
          {page === "categories" ? <CategoriesHomePage /> : null}
          {page === "sync" ? <SyncPage /> : null}
          {page === "diagnostics" ? <DiagnosticsPage /> : null}
          {page === "users" ? <UsersPage /> : null}
          {page === "shares" ? <SharesPage /> : null}
          {page === "system" ? <SystemPage /> : null}
        </div>
      </div>
    </div>
  );
}