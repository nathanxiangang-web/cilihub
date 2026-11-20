import { useCallback, useEffect, useState } from "react";
import {
  categories as fetchCategories,
  createCategory,
  home as fetchHome,
  reorderCategories,
  setHome,
  updateCategory,
  works as fetchWorks,
  type Category,
  type HomeOrder,
} from "../api";
import AdminToast, { type ToastKind, type ToastState } from "../components/AdminToast";

export default function CategoriesHomePage() {
  const [cats, setCats] = useState<Category[]>([]);
  const [order, setOrder] = useState<HomeOrder>({ pinned: [], order: [] });
  const [titles, setTitles] = useState<Record<number, string>>({});
  const [drafts, setDrafts] = useState<Record<number, string>>({});
  const [newName, setNewName] = useState("");
  const [newSlug, setNewSlug] = useState("");
  const [addWorkId, setAddWorkId] = useState(0);
  const [works, setWorks] = useState<{ id: number; title: string }[]>([]);
  const [toast, setToast] = useState<ToastState | null>(null);
  const [busy, setBusy] = useState(false);
  const notify = useCallback((message: string, kind: ToastKind = "info") => {
    setToast({ message, kind });
  }, []);

  function loadCategories() {
    fetchCategories()
      .then((items) => {
        setCats(items);
        const names: Record<number, string> = {};
        for (const c of items) names[c.id] = c.name;
        setDrafts(names);
      })
      .catch((err) => notify(err instanceof Error ? err.message : "加载分类失败", "error"));
  }

  function loadHome() {
    fetchHome()
      .then(setOrder)
      .catch((err) => notify(err instanceof Error ? err.message : "加载首页编排失败", "error"));
  }

  useEffect(() => {
    loadCategories();
    loadHome();
    fetchWorks({ size: 200 })
      .then((data) => {
        const items = (data.items ?? []).map((w) => ({ id: w.id, title: w.title }));
        setWorks(items);
        const labels: Record<number, string> = {};
        for (const w of items) labels[w.id] = w.title;
        setTitles(labels);
      })
      .catch(() => setWorks([]));
  }, [notify]);

  async function saveCategory(cat: Category) {
    const name = (drafts[cat.id] ?? "").trim();
    if (!name) {
      notify("分类名不能为空", "error");
      return;
    }
    try {
      await updateCategory(cat.id, name, cat.visible);
      loadCategories();
      notify("分类已更新", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "更新分类失败", "error");
    }
  }

  async function toggleCategory(cat: Category) {
    try {
      await updateCategory(cat.id, drafts[cat.id] ?? cat.name, !cat.visible);
      loadCategories();
    } catch (err) {
      notify(err instanceof Error ? err.message : "更新分类失败", "error");
    }
  }

  async function moveCategory(index: number, dir: -1 | 1) {
    const target = index + dir;
    if (target < 0 || target >= cats.length) return;
    const ids = cats.map((c) => c.id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    try {
      await reorderCategories(ids);
      loadCategories();
      notify("分类顺序已更新", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "调整分类顺序失败", "error");
    }
  }

  async function create() {
    const name = newName.trim();
    const slug = newSlug.trim();
    if (!name) {
      notify("请填写分类名", "error");
      return;
    }
    if (!/^[a-z0-9-]{1,40}$/.test(slug)) {
      notify("标识仅允许小写字母、数字与短横线（1-40 位）", "error");
      return;
    }
    try {
      await createCategory(name, slug);
      setNewName("");
      setNewSlug("");
      loadCategories();
      notify("分类已创建", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "创建分类失败", "error");
    }
  }

  async function saveHome(pinned: number[], ordered: number[]) {
    setBusy(true);
    try {
      await setHome(pinned, ordered);
      loadHome();
      notify("首页编排已保存", "ok");
    } catch (err) {
      notify(err instanceof Error ? err.message : "保存首页编排失败", "error");
    } finally {
      setBusy(false);
    }
  }

  function moveHome(index: number, dir: -1 | 1) {
    const ordered = [...order.order];
    const target = index + dir;
    if (target < 0 || target >= ordered.length) return;
    [ordered[index], ordered[target]] = [ordered[target], ordered[index]];
    void saveHome(order.pinned, ordered);
  }

  function togglePin(id: number) {
    const pinned = order.pinned.includes(id)
      ? order.pinned.filter((x) => x !== id)
      : [...order.pinned, id];
    void saveHome(pinned, order.order);
  }

  function removeFromHome(id: number) {
    void saveHome(order.pinned.filter((x) => x !== id), order.order.filter((x) => x !== id));
  }

  function addToHome() {
    if (!addWorkId || order.order.includes(addWorkId)) return;
    void saveHome(order.pinned, [...order.order, addWorkId]);
    setAddWorkId(0);
  }

  return (
    <div className="cs-page">
      <section className="cs-card">
        <div className="cs-card__head">
          <h3>分类</h3>
          <span className="cs-muted">名称与排序会立即影响前台分类页</span>
        </div>
        <div className="cs-cat-list">
          {cats.map((cat, index) => (
            <div key={cat.id} className="cs-cat-row">
              <span className="cs-cat-row__index">{index + 1}</span>
              <input
                className="cs-input"
                value={drafts[cat.id] ?? cat.name}
                onChange={(e) => setDrafts((prev) => ({ ...prev, [cat.id]: e.target.value }))}
              />
              <span className="cs-muted cs-cat-row__slug">{cat.slug}</span>
              <label className="cs-check">
                <input type="checkbox" checked={cat.visible} onChange={() => toggleCategory(cat)} />
                <span>可见</span>
              </label>
              <span className="cs-muted cs-cat-row__count">{cat.work_count} 部</span>
              <div className="cs-cat-row__ops">
                <button type="button" className="cs-btn cs-btn--tiny" disabled={index === 0} onClick={() => moveCategory(index, -1)}>上移</button>
                <button type="button" className="cs-btn cs-btn--tiny" disabled={index === cats.length - 1} onClick={() => moveCategory(index, 1)}>下移</button>
                <button type="button" className="cs-btn cs-btn--tiny" onClick={() => saveCategory(cat)}>保存</button>
              </div>
            </div>
          ))}
        </div>
        <div className="cs-inline-form">
          <input className="cs-input" placeholder="新分类名" value={newName} onChange={(e) => setNewName(e.target.value)} />
          <input className="cs-input" placeholder="标识 slug" value={newSlug} onChange={(e) => setNewSlug(e.target.value)} />
          <button type="button" className="cs-btn cs-btn--primary" onClick={create}>新增分类</button>
        </div>
      </section>

      <section className="cs-card">
        <div className="cs-card__head">
          <h3>首页编排</h3>
          <span className="cs-muted">置顶作品优先，其余按下列顺序展示</span>
        </div>
        {order.order.length ? (
          <div className="cs-home-list">
            {order.order.map((id, index) => (
              <div key={id} className="cs-home-row">
                <span className="cs-cat-row__index">{index + 1}</span>
                <span className="cs-home-row__title">{titles[id] ?? `#${id}`}</span>
                <label className="cs-check">
                  <input type="checkbox" checked={order.pinned.includes(id)} onChange={() => togglePin(id)} />
                  <span>置顶</span>
                </label>
                <div className="cs-cat-row__ops">
                  <button type="button" className="cs-btn cs-btn--tiny" disabled={busy || index === 0} onClick={() => moveHome(index, -1)}>上移</button>
                  <button type="button" className="cs-btn cs-btn--tiny" disabled={busy || index === order.order.length - 1} onClick={() => moveHome(index, 1)}>下移</button>
                  <button type="button" className="cs-btn cs-btn--tiny" onClick={() => removeFromHome(id)}>移除</button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="cs-empty">首页还没有编排作品</div>
        )}
        <div className="cs-inline-form">
          <select className="cs-input" value={addWorkId} onChange={(e) => setAddWorkId(Number(e.target.value))}>
            <option value={0}>选择作品加入首页</option>
            {works.filter((w) => !order.order.includes(w.id)).map((w) => (
              <option key={w.id} value={w.id}>{w.title}</option>
            ))}
          </select>
          <button type="button" className="cs-btn cs-btn--primary" disabled={!addWorkId} onClick={addToHome}>加入首页</button>
        </div>
      </section>

      <AdminToast toast={toast} onClose={() => setToast(null)} />
    </div>
  );
}