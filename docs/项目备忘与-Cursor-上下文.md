# 项目备忘与 Cursor 上下文（AImail / tempmail）

> 给 **你自己** 的部署与排错备忘，以及 **给 Cursor** 的一行事实列表；新开对话时可粘贴「Cursor 上下文」一节或 `@本文件`。

---

## 一、给 Cursor 的一行上下文（复制下面整块即可）

```
仓库：自托管临时邮箱 TempMail（Go API + 静态 SPA + Postgres + Redis + Postfix + PgBouncer），模块名 tempmail，Go 代码在 api/。
GitHub：github.com/wuya521/tempmail，main 分支；本地开发路径 e:\AImail；生产服务器路径 /root/tempmail（OpenCloudOS / 宝塔）。
服务器 Git 铁律：任何 git 命令前必须先 cd /root/tempmail（在 ~ 执行会报 fatal: not a git repository）。
服务器拉代码用 SSH（推荐）：git remote set-url origin git@github.com:wuya521/tempmail.git；需把服务器 ~/.ssh/id_ed25519.pub 加到 GitHub SSH keys；勿在服务器设 127.0.0.1:7890 为 git 代理。
仅前端/静态/nginx 配置变更：cd /root/tempmail && git pull origin main && docker compose restart frontend（或改 default.conf 时用 up -d frontend --force-recreate）；不必 build api。
改了 Go/api 代码：pull 后 docker compose build api && docker compose up -d api。
api/Dockerfile 已内置 GOPROXY 与 GOSUMDB，勿在服务器手写改 Dockerfile（会与 pull 冲突）；若 dirty 用 git checkout -- api/Dockerfile 或 git restore api/Dockerfile 再以仓库为准 pull。
docker-compose：frontend 挂载 ./frontend:ro + nginx/default.conf；对外常见 8880:80；API 依赖 postgres/redis/pgbouncer；数据卷 ./data（含 admin.key、shop 收款码目录 data/shop）。
nginx：location 必须用 ^~ /public/（及 ^~ /api/），否则正则 location ~* \\.(png|jpg)$ 会抢走 /public/shop-assets/*.png 导致收款码 404。
前端强缓存：nginx 对 .js/.css 使用 immutable；改 app.js/style.css 后必须在 frontend/index.html 增大 ?v= 版本号并部署。
数据库迁移在 sql/，新库用 init.sql；已有库按序执行 migrate_v*.sql（migrate_v5 Claude 店铺、v6 batch_label、v7 支付宝当面付字段与 static_payment_manual_confirm）。
scripts/ 目录已 .gitignore，不进入仓库；不要在回复里依赖该目录被推送。
业务功能摘要：管理员账户分页/模糊搜/封禁仅禁登录；Claude 自助售号（库存导入 Tab/CSV/####/表头跳过；可选支付宝当面付 precreate + POST /public/alipay/notify 自动发货，静态收款码备用；店铺可配置 static_payment_manual_confirm）；库存 batch_label；GET /admin/shop/inventory/batches、POST purge-batch / purge-available 仅删待售；管理员侧店铺三页；GET /api/admin/shop/orders/:id 订单详情。
生产域名示例：Web https://mail.yahoohh.cloud/；支付宝异步通知须与 .env 一致：https://mail.yahoohh.cloud/public/alipay/notify（支付宝开放平台同址）。
用户偏好：回复简体中文；代码改动聚焦需求；本机推送 GitHub：代理可用时 `git config --global http.https://github.com.proxy http://127.0.0.1:7890`（仅 github.com）；不用代理则 `git config --global --unset http.https://github.com.proxy`；或 `git -c http.proxy= -c https.proxy= push`。服务器 Git 勿设 127.0.0.1:7890 代理。
```

---

## 二、本地（Windows）开发

| 项 | 说明 |
|----|------|
| 路径 | `e:\AImail` |
| 推送 | `git push origin main`；仅 GitHub 走本机 Clash 7890：`git config --global http.https://github.com.proxy http://127.0.0.1:7890`；不用代理：`git config --global --unset http.https://github.com.proxy`；临时绕过坏代理：`git -c http.proxy= -c https.proxy= push origin main` |
| 不提交 | `scripts/`、`.cursor/` 已在 `.gitignore` |

---

## 三、生产服务器（宝塔 / `/root/tempmail`）

### 铁律（避免踩坑）

- **所有 `git` 命令必须在仓库根目录执行**：先 `cd /root/tempmail`，再 `git pull`。在 `/root` 或 `~` 下执行会报错：`fatal: not a git repository`。

### 服务器 GitHub 用 SSH（推荐，国内一般比 HTTPS 快）

一次性配置（每台服务器做一次）：

```bash
# 若没有密钥
ssh-keygen -t ed25519 -C "server-tempmail" -f ~/.ssh/id_ed25519 -N ""
cat ~/.ssh/id_ed25519.pub   # 整行复制到 GitHub → Settings → SSH and GPG keys
ssh -T git@github.com       # 应出现 Hi wuya521!

cd /root/tempmail
git remote set-url origin git@github.com:wuya521/tempmail.git
git remote -v
```

### 日常更新（按改动选一种）

**A. 只改了前端静态或 `nginx/default.conf`（最常见）**

`frontend` 是挂载 `./frontend`，pull 后宿主机文件已更新；重启容器即可：

```bash
cd /root/tempmail
git pull origin main
docker compose restart frontend
```

若改了 `nginx/default.conf`，用重建更稳妥：

```bash
docker compose up -d frontend --force-recreate
```

**B. 改了 `api/` Go 代码或 `api/Dockerfile`**

```bash
cd /root/tempmail
git pull origin main
docker compose build api
docker compose up -d api
```

**C. A + B 都有**

先 pull，再 `build api` + `up -d api`，最后 `restart frontend` 或 `--force-recreate frontend`。

### `git pull` 被本地修改挡住（典型：`api/Dockerfile`）

仓库里的 `api/Dockerfile` 已含 `GOPROXY`/`GOSUMDB`。若在服务器上又手改同一文件，pull 会提示会覆盖本地修改。以远程为准：

```bash
cd /root/tempmail
git restore api/Dockerfile    # 或 git checkout -- api/Dockerfile
git pull origin main
```

### 首次或有大版本数据库变更

**在仓库根目录执行**（`POSTGRES_USER` / `POSTGRES_DB` 若与 `.env` 不一致请改成你的）：

```bash
cd /root/tempmail
docker exec -i $(docker compose ps -q postgres) psql -U tempmail -d tempmail < sql/migrate_v5.sql
# 库存批次标签（claude_inventory.batch_label）
docker exec -i $(docker compose ps -q postgres) psql -U tempmail -d tempmail < sql/migrate_v6.sql
# 支付宝当面付 + 静态收款是否人工确认（claude_orders.payment_channel / alipay_trade_no；claude_shop_config.static_payment_manual_confirm）
docker exec -i $(docker compose ps -q postgres) psql -U tempmail -d tempmail < sql/migrate_v7.sql
```

若 `docker compose ps -q postgres` 为空，先 `docker compose up -d postgres` 再执行。**已执行过的 migrate 不要重复执行**（重复 ALTER IF NOT EXISTS 一般无害，但养成按版本核对习惯）。

### 支付宝当面付（可选）

1. 在服务器 **`/root/tempmail/.env`** 增加或填写（勿提交 Git）：

   - `ALIPAY_APP_ID`
   - `ALIPAY_PRIVATE_KEY`（应用私钥，PEM 或多行写成一行用 `\n`）
   - `ALIPAY_PUBLIC_KEY`（支付宝公钥，用于验签异步通知）
   - `ALIPAY_NOTIFY_URL=https://mail.yahoohh.cloud/public/alipay/notify`（域名随你实际站点修改）
   - `ALIPAY_GATEWAY=https://openapi.alipay.com/gateway.do`（沙箱用 `https://openapi.alipaydev.com/gateway.do`）

2. 改完后 **`docker compose build api && docker compose up -d api`**（`docker-compose.yml` 会把上述变量注入 `api` 容器）。

3. [支付宝开放平台](https://open.alipay.com/) 应用里 **异步通知地址** 与 `ALIPAY_NOTIFY_URL` **完全一致**。

4. 宝塔 / 外层 Nginx 须把带 `/public/`、`/api/` 的请求转到本项目的 `frontend` 容器（或等价反代），且 **POST** `/public/alipay/notify` 能到达 Go API（与仓库 `nginx/default.conf` 中 `^~ /public/` 一致）。

### 前端「改了不生效」

1. 确认 `frontend/index.html` 里 `app.js?v=` / `style.css?v=` 已加大。  
2. 强刷或无痕；3. 已执行 `frontend --force-recreate`（nginx 配置变更时）。

### Git 说明

- 生产环境 **`origin` 建议为 SSH**：`git@github.com:wuya521/tempmail.git`。  
- `Your branch is ahead of 'upstream/main'` 指 fork 上游，**可忽略**；以 `origin/main` 为准。

---

## 四、常用排查

| 现象 | 处理 |
|------|------|
| `fatal: not a git repository` | 当前目录不是仓库；先 `cd /root/tempmail` 再执行 git。 |
| pull 报 Dockerfile/index 冲突 | 以仓库为准：`git restore <文件>` 后 `git pull`；Dockerfile 已在仓库含 GOPROXY，勿在服务器手写。 |
| 功能与线上不一致 | `git log -1`、`grep app.js frontend/index.html` 看是否最新提交与 `?v=`。 |
| API 404 新接口 | 是否 `docker compose build api` 后未重启容器。 |

---

## 五、与本仓库相关的链接

- 远程仓库：<https://github.com/wuya521/tempmail>

---

*文件可随项目演进自行增删行；重大架构变更时更新「给 Cursor 的一行上下文」块。*
