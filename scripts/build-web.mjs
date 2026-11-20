#!/usr/bin/env node
/**
 * 把 Web 目标的 vite 产物投递到 Go 的嵌入目录 `server/web/dist/`。
 *
 * 为什么单独一个脚本：
 *   `//go:embed all:dist` 只认编译期已存在的文件，因此前端构建必须先落到
 *   Go 包内；用 node 驱动 vite 还能跨平台注入 VITE_TARGET=web（Windows 的
 *   npm script 跑不了 `VITE_TARGET=web vite build` 这种前置赋值）。
 *
 * 用法：
 *   node scripts/build-web.mjs --build   # 构建并投递（npm run build:web）
 *   node scripts/build-web.mjs           # 仅投递，dist/ 需已存在
 */
import { spawnSync } from "node:child_process";
import { cpSync, existsSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(root, "dist");
const dest = join(root, "server", "web", "dist");

if (process.argv.includes("--build")) {
  const viteBin = join(root, "node_modules", "vite", "bin", "vite.js");
  if (!existsSync(viteBin)) {
    console.error(`[web] 缺少 ${viteBin}，请先安装依赖`);
    process.exit(1);
  }
  const built = spawnSync(process.execPath, [viteBin, "build"], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, VITE_TARGET: "web", VITE_OPENLIST_LIBRARY: "1" },
  });
  if (built.status !== 0) {
    console.error("[web] 前端构建失败");
    process.exit(built.status ?? 1);
  }
}

if (!existsSync(join(dist, "index.html"))) {
  console.error(`[web] 缺少 ${join(dist, "index.html")}，请先运行 npm run build:web`);
  process.exit(1);
}

rmSync(dest, { recursive: true, force: true });
cpSync(dist, dest, { recursive: true });
console.log(`[web] 产物已同步 → ${dest}`);
