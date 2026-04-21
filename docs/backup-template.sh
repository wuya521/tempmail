#!/usr/bin/env bash
# ============================================================
# TempMail 生产服务器定期备份脚本 — 参考模板
# 用法：把这个文件复制到服务器的 /root/tempmail/scripts/backup.sh 下
#       （scripts/ 已在 .gitignore，不会进仓库）然后放入 crontab。
#
# 建议 crontab（每天凌晨 3:10 执行，保留 14 天）：
#   10 3 * * * /bin/bash /root/tempmail/scripts/backup.sh >> /root/tempmail/scripts/backup.log 2>&1
#
# 备份内容：
#   1. Postgres 全量 pg_dump  → /root/backups/db_YYYYMMDD_HHMM.sql.gz
#   2. ./data 目录 tar       → /root/backups/data_YYYYMMDD_HHMM.tgz
#        含 admin.key、shop 收款码图、alipay_*.pem 等运行时文件
# ============================================================
set -euo pipefail

REPO_DIR="/root/tempmail"
BACKUP_DIR="/root/backups"
KEEP_DAYS=14
POSTGRES_USER="${POSTGRES_USER:-tempmail}"
POSTGRES_DB="${POSTGRES_DB:-tempmail}"

ts="$(date +%Y%m%d_%H%M)"
mkdir -p "${BACKUP_DIR}"

cd "${REPO_DIR}"

pg_container="$(docker compose ps -q postgres)"
if [[ -z "${pg_container}" ]]; then
  echo "[backup] postgres 容器未运行，跳过 DB 备份" >&2
else
  echo "[backup] dumping postgres → ${BACKUP_DIR}/db_${ts}.sql.gz"
  docker exec -i "${pg_container}" pg_dump -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" \
    | gzip -c > "${BACKUP_DIR}/db_${ts}.sql.gz"
fi

if [[ -d "${REPO_DIR}/data" ]]; then
  echo "[backup] taring ./data → ${BACKUP_DIR}/data_${ts}.tgz"
  tar -czf "${BACKUP_DIR}/data_${ts}.tgz" -C "${REPO_DIR}" data
fi

# 清理超过保留天数的老备份
find "${BACKUP_DIR}" -maxdepth 1 -type f -name 'db_*.sql.gz' -mtime +${KEEP_DAYS} -delete || true
find "${BACKUP_DIR}" -maxdepth 1 -type f -name 'data_*.tgz'   -mtime +${KEEP_DAYS} -delete || true

echo "[backup] done at $(date -Is)"
