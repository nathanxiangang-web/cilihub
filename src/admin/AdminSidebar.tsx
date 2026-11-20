import type { ReactNode } from "react";

export const ADMIN_MENU = [
  { key: "overview", label: "概览" },
  { key: "library", label: "影视库" },
  { key: "categories", label: "分类与首页" },
  { key: "sync", label: "OpenList 同步" },
  { key: "diagnostics", label: "播放诊断" },
  { key: "system", label: "系统与审计" },
] as const;

export type AdminPageKey = (typeof ADMIN_MENU)[number]["key"];

const ICONS: Record<AdminPageKey, ReactNode> = {
  overview: (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7">
      <rect x="3" y="3" width="7" height="7" rx="1.6" />
      <rect x="14" y="3" width="7" height="7" rx="1.6" />
      <rect x="3" y="14" width="7" height="7" rx="1.6" />
      <rect x="14" y="14" width="7" height="7" rx="1.6" />
    </svg>
  ),
  library: (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7">
      <rect x="3" y="4" width="18" height="14" rx="2" />
      <path d="M3 8h18M9 4v14" />
    </svg>
  ),
  categories: (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7">
      <path d="M4 6h16M4 12h10M4 18h7" />
    </svg>
  ),
  sync: (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7">
      <path d="M20 12a8 8 0 1 1-2.34-5.66M20 4v4h-4" />
    </svg>
  ),
  diagnostics: (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7">
      <path d="M3 12h4l2.5-7 4 14L16 12h5" />
    </svg>
  ),
  system: (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7">
      <circle cx="12" cy="12" r="3.2" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06-2.83 2.83-.06-.06A1.7 1.7 0 0 0 15 19.4a1.7 1.7 0 0 0-1 .6 1.7 1.7 0 0 0-.4 1.1V21H9.6v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.87.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.5-1H3v-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.34-1.87l-.06-.06 2.83-2.83.06.06A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-1.5V3h4v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.87-.34l.06-.06 2.83 2.83-.06.06A1.7 1.7 0 0 0 19.4 9c.2.4.6.9 1.5 1H21v4h-.1a1.7 1.7 0 0 0-1.5 1Z" />
    </svg>
  ),
};

export default function AdminSidebar({ current, onSelect }: {
  current: AdminPageKey;
  onSelect: (key: AdminPageKey) => void;
}) {
  return (
    <aside className="cs-sidebar">
      <div className="cs-sidebar__brand">
        <span className="cs-sidebar__mark">磁</span>
        <span className="cs-sidebar__brand-text">
          <strong>磁力短剧</strong>
          <em>管理后台</em>
        </span>
      </div>
      <nav className="cs-sidebar__nav">
        {ADMIN_MENU.map((item) => (
          <button
            key={item.key}
            type="button"
            className={`cs-sidebar__item${current === item.key ? " is-active" : ""}`}
            onClick={() => onSelect(item.key)}
          >
            <span className="cs-sidebar__icon">{ICONS[item.key]}</span>
            <span>{item.label}</span>
          </button>
        ))}
      </nav>
      <div className="cs-sidebar__footer">cilihub admin</div>
    </aside>
  );
}