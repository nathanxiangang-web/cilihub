import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { AppProvider } from "./store";
import { invoke } from "./rpc";
import "./index.css";
import { initPlaybackLog, installPlaybackPageEvents } from "./playbackDiagnostics";

installPlaybackPageEvents();
void initPlaybackLog();

/* 运行期错误统一落到 debug.log，便于自查 */
function report(kind: string, msg: string) {
  invoke("debug_log", { msg: `[${kind}] ${msg}` }).catch(() => {});
}
window.addEventListener("error", (e) => {
  report("error", `${e.message} @ ${e.filename}:${e.lineno}:${e.colno}`);
});
window.addEventListener("unhandledrejection", (e) => {
  report("reject", String((e.reason && (e.reason.stack || e.reason.message)) || e.reason));
});

const AdminApp = React.lazy(() => import("./admin/AdminApp"));
const isAdminRoute = location.pathname === "/admin" || location.pathname.startsWith("/admin/");

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    {isAdminRoute ? (
      <React.Suspense fallback={<div className="cs-admin-boot">加载管理后台…</div>}>
        <AdminApp />
      </React.Suspense>
    ) : (
      <AppProvider>
        <App />
      </AppProvider>
    )}
  </React.StrictMode>,
);
