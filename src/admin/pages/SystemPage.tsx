import { useCallback, useEffect, useState, type FormEvent } from "react";
import { audit, changePassword, type AuditRow, type Pagination } from "../api";
import { AdminTable, type AdminColumn } from "../components/AdminTable";
import AdminToast, { type ToastKind, type ToastState } from "../components/AdminToast";


export default function SystemPage() {
  const [audits, setAudits] = useState<AuditRow[]>([]);
  const [pagination, setPagination] = useState<Pagination>({ page: 1, size: 20, pages: 1, total: 0 });
  const [page, setPage] = useState(1);
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<ToastState | null>(null);
  const notify = useCallback((message: string, kind: ToastKind = "info") => {
    setToast({ message, kind });
  }, []);

  function loadAudit(nextPage: number) {
    audit(nextPage, 20)
      .then((data) => {
        setAudits(data.items ?? []);
        setPagination(data.pagination ?? { page: nextPage, size: 20, pages: 1, total: 0 });
      })
      .catch((err) => notify(err instanceof Error ? err.message : "加载审计日志失败", "error"));
  }

  useEffect(() => {
    loadAudit(page);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page]);

  async function submitPassword(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    if (!oldPassword || !newPassword) {
      notify("请填写原密码与新密码", "error");
      return;
    }
    if (newPassword !== confirmPassword) {
      notify("两次输入的新密码不一致", "error");
      return;
    }
    setBusy(true);
    try {
      await changePassword(oldPassword, newPassword);
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
      notify("密码已更新，其他设备会话已失效", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "修改密码失败", "error");
    } finally {
      setBusy(false);
    }
  }

  const columns: AdminColumn<AuditRow>[] = [
    { key: "at", title: "时间", width: "170px", render: (row) => row.at },
    { key: "admin", title: "管理员", width: "110px", render: (row) => row.admin || "-" },
    { key: "action", title: "动作", width: "140px", render: (row) => row.action },
    {
      key: "target",
      title: "对象",
      width: "160px",
      render: (row) => `${row.target_type}${row.target_id ? ` #${row.target_id}` : ""}`,
    },
    {
      key: "detail",
      title: "变更摘要",
      render: (row) => (
        <span className="cs-mono cs-detail" title={row.detail || ""}>
          {row.detail ? (row.detail.length > 120 ? `${row.detail.slice(0, 120)}…` : row.detail) : "-"}
        </span>
      ),
    },
  ];

  return (
    <div className="cs-page">

      <section className="cs-card">
        <div className="cs-card__head">
          <h3>修改密码</h3>
          <span className="cs-muted">修改后其他设备的登录会话会全部失效</span>
        </div>
        <form className="cs-form-grid cs-form-grid--password" onSubmit={submitPassword}>
          <label className="cs-field">
            <span>原密码</span>
            <input type="password" autoComplete="current-password" value={oldPassword} onChange={(e) => setOldPassword(e.target.value)} />
          </label>
          <label className="cs-field">
            <span>新密码</span>
            <input type="password" autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} />
          </label>
          <label className="cs-field">
            <span>确认新密码</span>
            <input type="password" autoComplete="new-password" value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} />
          </label>
          <div className="cs-form-grid__action">
            <button type="submit" className="cs-btn cs-btn--primary" disabled={busy}>
              {busy ? "提交中…" : "更新密码"}
            </button>
          </div>
        </form>
      </section>

      <section className="cs-card cs-card--table">
        <div className="cs-card__head">
          <h3>审计日志</h3>
          <span className="cs-muted">共 {pagination.total} 条</span>
        </div>
        <AdminTable columns={columns} rows={audits} rowKey={(row, index) => `${row.at}-${row.action}-${index}`} empty="暂无审计记录" />
        <div className="cs-pager cs-pager--inside">
          <button type="button" className="cs-btn cs-btn--tiny" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
          <span className="cs-muted">第 {pagination.page} / {pagination.pages} 页</span>
          <button type="button" className="cs-btn cs-btn--tiny" disabled={page >= pagination.pages} onClick={() => setPage(page + 1)}>下一页</button>
        </div>
      </section>

      <AdminToast toast={toast} onClose={() => setToast(null)} />
    </div>
  );
}