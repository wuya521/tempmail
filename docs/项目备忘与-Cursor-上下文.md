# 项目备忘与 Cursor 上下文（AImail / tempmail）

> 给 **你自己** 的部署与排错备忘，以及 **给 Cursor** 的一行事实列表；新开对话时可粘贴「Cursor 上下文」一节或 `@本文件`。

---

## 一、给 Cursor 的一行上下文（复制下面整块即可）

```
仓库：自托管临时邮箱 TempMail（Go API + 静态 SPA + Postgres + Redis + Postfix + PgBouncer），模块名 tempmail，Go 代码在 api/。
GitHub：github.com/wuya521/tempmail，main 分支；本地开发路径 e:\AImail；生产服务器路径 /root/tempmail（OpenCloudOS / 宝塔）。
服务器拉代码用 SSH：git@github.com:wuya521/tempmail.git；勿在服务器设 127.0.0.1:7890 为 git 代理（7890 是本地电脑代理）。
api/Dockerfile 已内置 GOPROXY=https://goproxy.cn,direct 与 GOSUMDB，勿在服务器再改 Dockerfile 以免 dirty；更新流程 git pull → docker compose build api → up -d api。
docker-compose：frontend 挂载 ./frontend:ro + nginx/default.conf；对外常见 8880:80；API 依赖 postgres/redis/pgbouncer；数据卷 ./data（含 admin.key、shop 收款码目录 data/shop）。
前端强缓存：nginx 对 .js/.css 使用 immutable；改 app.js/style.css 后必须在 frontend/index.html 增大 ?v= 版本号并部署。
数据库迁移在 sql/，新库用 init.sql；已有库按序执行 migrate_v*.sql（如 migrate_v5 含 Claude 店铺与 accounts.last_seen_at）。
scripts/ 目录已 .gitignore，不进入仓库；不要在回复里依赖该目录被推送。
业务功能摘要：管理员账户分页/模糊搜/封禁仅禁登录；Claude 自助售号（库存导入 Tab/CSV/####/表头跳过、人工确认收款发货）；店铺配置仅管理员。
用户偏好：回复简体中文；代码改动聚焦需求；推送 GitHub 时若 HTTPS 失败可用 git -c http.proxy= -c https.proxy= push 或改用 SSH。
```

---

## 二、本地（Windows）开发

| 项 | 说明 |
|----|------|
| 路径 | `e:\AImail` |
| 推送 | `git push origin main`；代理异常时：`git -c http.proxy= -c https.proxy= push origin main` |
| 不提交 | `scripts/`、`.cursor/` 已在 `.gitignore` |

---

## 三、生产服务器（宝塔 / `/root/tempmail`）

### 日常更新

```bash
cd /root/tempmail
git pull origin main
docker compose build api
docker compose up -d api
# nginx/default.conf 或仅前端静态有变时：
docker compose up -d frontend --force-recreate
```

### 首次或有大版本数据库变更

```bash
docker exec -i $(docker compose ps -q postgres) psql -U tempmail -d tempmail < sql/migrate_v5.sql
# 以后若有 migrate_v6 等，按发布说明执行对应文件
```

### 前端「改了不生效」

1. 确认 `frontend/index.html` 里 `app.js?v=` / `style.css?v=` 已加大。  
2. 强刷或无痕；3. 已执行 `frontend --force-recreate`（nginx 配置变更时）。

### Git 说明

- `git remote -v` 应用 SSH 地址拉 GitHub，避免国内 HTTPS 不稳定。  
- `Your branch is ahead of 'upstream/main'` 指 fork 上游，**可忽略**；以 `origin/main` 为准。

---

## 四、常用排查

| 现象 | 处理 |
|------|------|
| pull 报 Dockerfile/index 冲突 | 以仓库为准：`git restore <文件>` 后 `git pull`；Dockerfile 已在仓库含 GOPROXY，勿在服务器手写。 |
| 功能与线上不一致 | `git log -1`、`grep app.js frontend/index.html` 看是否最新提交与 `?v=`。 |
| API 404 新接口 | 是否 `docker compose build api` 后未重启容器。 |

---

## 五、与本仓库相关的链接

- 远程仓库：<https://github.com/wuya521/tempmail>

---

*文件可随项目演进自行增删行；重大架构变更时更新「给 Cursor 的一行上下文」块。*
