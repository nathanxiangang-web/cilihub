#!/usr/bin/env bash
# cilihub 首次部署初始化：生成 .env（管理员初始密码、CILIHUB_SECRET、端口）并打印后续步骤。
# 已存在 .env 时不覆盖，只提示读取密码的方式。
set -euo pipefail
cd "$(dirname "$0")/.."

env_file=".env"

if [ -f "$env_file" ]; then
  printf '%s 已存在，未做修改。\n' "$env_file"
  printf '查看管理员初始密码：grep CILIHUB_ADMIN_PASSWORD %s\n' "$env_file"
  exit 0
fi

admin_password="admin"   # 默认 admin/admin；首次登录后请立即修改
secret="$(openssl rand -base64 32)"

umask 077
cat > "$env_file" <<EOF
CILIHUB_PORT=18792
CILIHUB_SECRET=$secret
CILIHUB_ADMIN_USER=admin
CILIHUB_ADMIN_PASSWORD=$admin_password
EOF
chmod 600 "$env_file"

printf '\n已生成 %s（0600，已加入 .gitignore）。\n' "$env_file"
printf '管理员账号：admin\n'
printf '管理员初始密码：%s\n' "$admin_password"
printf '\n后续步骤：\n'
printf '  1. bash build.sh\n'
printf '  2. sudo docker compose up -d --wait\n'
printf '  3. 若忘记密码，可在容器内读取：sudo docker compose exec web cat /data/admin-initial-password.txt\n'
printf '  4. SSH 转发到本机后访问 http://127.0.0.1:18792/admin ，登录后立即修改密码\n'
printf '\n注意：不要提交或公开 .env；密码只在本机与服务器上保存。\n'