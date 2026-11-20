#!/usr/bin/env bash
# cilihub 管理后台编译检查：前端 tsc+vite 构建、Go 依赖落定与编译。
# 在 dsh 的 /opt/cilihub 执行。
set -uo pipefail
cd "$(dirname "$0")/.."
fail=0
step() { printf '\n== %s ==\n' "$1"; }

step "frontend install"
corepack pnpm install --frozen-lockfile || fail=1

step "frontend build (tsc + vite)"
corepack pnpm run build || fail=1

step "go deps"
cd server || exit 1
GOPROXY=https://goproxy.cn,direct go mod tidy || fail=1

step "go build"
GOPROXY=https://goproxy.cn,direct go build ./... || fail=1

step "go vet"
go vet ./... || fail=1

printf '\ncheck result: %s\n' "$([ "$fail" -eq 0 ] && echo PASS || echo FAIL)"
exit "$fail"