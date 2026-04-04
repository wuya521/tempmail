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
改了 Go/api 代码：pull 后 docker compose build api（大改或疑缓存时可 --no-cache）&& docker compose up -d api（需读新 .env 时用 --force-recreate）。
api/Dockerfile：Go 用 GOPROXY/GOSUMDB；运行阶段 apk 前已 sed 换阿里云 Alpine 源（国内构建 apk 否则可能卡 10+ 分钟）；勿在服务器手写改 Dockerfile 与仓库冲突。
docker-compose：frontend 挂载 ./frontend:ro + nginx/default.conf；对外常见 8880:80；**域名 HTTPS 若 502，先查 `docker compose ps` 是否有 tempmail-frontend-1**，没有则 `docker compose up -d frontend`；宝塔反代应指向该端口。API 依赖 postgres/redis/pgbouncer；数据卷 ./data（含 admin.key、shop 收款码；**支付宝 PEM 可放 data/alipay_*.pem**，见下文 KEY_FILE）。
nginx：location 必须用 ^~ /public/（及 ^~ /api/），否则正则 location ~* \\.(png|jpg)$ 会抢走 /public/shop-assets/*.png 导致收款码 404。
前端强缓存：nginx 对 .js/.css 使用 immutable；改 app.js/style.css 后必须在 frontend/index.html 增大 ?v= 版本号并部署。
数据库迁移在 sql/，新库用 init.sql；已有库按序执行 migrate_v*.sql（migrate_v5 Claude 店铺、v6 batch_label、v7 支付宝当面付字段与 static_payment_manual_confirm）。
scripts/ 目录已 .gitignore，不进入仓库；不要在回复里依赖该目录被推送。
业务功能摘要：管理员账户分页/模糊搜/封禁仅禁登录；Claude 自助售号（库存导入 Tab/CSV/####/表头跳过；可选支付宝当面付 precreate + POST /public/alipay/notify 自动发货，静态收款码备用；店铺可配置 static_payment_manual_confirm）；库存 batch_label；GET /admin/shop/inventory/batches、POST purge-batch / purge-available 仅删待售；管理员侧店铺三页；GET /api/admin/shop/orders/:id 订单详情。
生产域名示例：Web https://mail.yahoohh.cloud/；支付宝异步通知须与 .env 一致：https://mail.yahoohh.cloud/public/alipay/notify（支付宝开放平台同址）。
支付宝密钥：推荐 `.env` 设 `ALIPAY_PRIVATE_KEY_FILE=/data/alipay_private.pem`、`ALIPAY_PUBLIC_KEY_FILE=/data/alipay_public.pem`（文件在宿主机 `./data/`，容器内为 `/data`），避免宝塔/超长 env/CRLF；`isv.invalid-signature` 多为**应用私钥与开放平台上传的应用公钥不是一对**或 APPID 不对应；网关请求已带 `format=JSON`。
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

疑镜像未更新或依赖缓存异常时：`docker compose build api --no-cache`。改 `.env` 后要让进程读到新环境变量：`docker compose up -d api --force-recreate`。若 `--force-recreate` 报 **容器名 `/tempmail-api-1` already in use**：

```bash
docker compose stop api 2>/dev/null || true
docker rm -f tempmail-api-1 2>/dev/null || true
docker rm -f $(docker ps -aq --filter "name=tempmail-api") 2>/dev/null || true
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

**密钥（推荐文件方式，少踩坑）**

- `./data` 已挂载到容器 **`/data`**，不必改 compose。将**应用私钥**、**支付宝公钥**（开放平台「查看支付宝公钥」，非应用公钥）写成 PEM，例如：
  - 宿主机：`/root/tempmail/data/alipay_private.pem`、`data/alipay_public.pem`
  - `chmod 600 data/alipay_*.pem`
- `.env` 中设（并清空或注释掉超长单行 `ALIPAY_PRIVATE_KEY` / `ALIPAY_PUBLIC_KEY`，避免与 FILE 混用同内容重复）：

  ```env
  ALIPAY_APP_ID=你的APPID
  ALIPAY_PRIVATE_KEY=
  ALIPAY_PUBLIC_KEY=
  ALIPAY_PRIVATE_KEY_FILE=/data/alipay_private.pem
  ALIPAY_PUBLIC_KEY_FILE=/data/alipay_public.pem
  ALIPAY_NOTIFY_URL=https://你的域名/public/alipay/notify
  ALIPAY_GATEWAY=https://openapi.alipay.com/gateway.do
  ```

- 若坚持用环境变量单行 Base64：变量名须为 **`ALIPAY_PRIVATE_KEY`**（勿写成 `ALIPAY_PRIVATE_KE`）；Base64 长度须为 **4 的倍数**；Windows/宝塔编辑的 `.env` 建议 **`sed -i 's/\r$//' .env`** 去 CRLF。

**启动自检**

- 日志应出现 **`[alipay] 解析器 v5：私钥长度=… 公钥长度=…`** 与 **`当面付已初始化`**。PEM 多行时私钥「长度」含换行，`len%4` 不必为 0。
- 容器内粗检（不贴密钥）：`docker compose exec api sh -c 'echo -n "$ALIPAY_PRIVATE_KEY" | wc -c'`（用 FILE 时可能为 0，以文件为准）；`printf '%.3s\n' "$ALIPAY_PUBLIC_KEY"` 若走 env 应为 `MII` 开头。

**错误**

- **`isv.invalid-signature`**：绝大多数是 **服务器应用私钥** 与开放平台 **该 APPID 下上传的应用公钥** 不是同一对；或 APPID 填错。与「支付宝公钥」（验异步通知）是两套概念。
- 代码侧网关请求已包含 **`format=JSON`**；`api/alipay` 中公钥 DER 回退解析使用 **`x509.ParsePKCS1PublicKey`**（与 `crypto/rsa` 二选一在 Go 1.23 均可编译，仓库已统一为 x509）。

**其余步骤**

1. [支付宝开放平台](https://open.alipay.com/) 应用里 **异步通知地址** 与 `ALIPAY_NOTIFY_URL` **完全一致**。
2. 改配置后 **`docker compose build api`（必要时 `--no-cache`）+ `docker compose up -d api --force-recreate`**。
3. 宝塔 / 外层 Nginx 须把带 `/public/`、`/api/` 的请求转到本项目的 **`frontend`** 容器（默认宿主机 **8880→80**），且 **POST** `/public/alipay/notify` 能到达 Go API（与仓库 `nginx/default.conf` 中 `^~ /public/` 一致）。
4. 线上验证接口勿把两条命令粘成一行（否则 `grep` 会吃到 `curl -sS` 的 `-S`）：  
   `curl -sS 'https://域名/public/claude-shop' | grep alipay_precreate`

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
| **`docker compose build api` 极慢（10+ 分钟卡在 apk）** | 正常：在拉 `dl-cdn.alpinelinux.org`。仓库 `api/Dockerfile` 已在 `apk` 前换 **阿里云 Alpine 源**；`git pull` 后重建。 |
| **HTTPS 访问站点 502**，直连 `8080/health` 正常 | `docker compose ps` 是否缺少 **frontend**；`docker compose up -d frontend`；宝塔反代是否指向 **8880**（或 compose 实际映射端口）。 |
| **`up -d api --force-recreate` 容器名冲突** | 见上文 **B** 中 `docker rm -f tempmail-api-1` 与按 name 过滤删除后再 `up -d api`。勿轻易 `docker compose down` 全栈。 |
| 日志无 **`解析器 v5`** 却仍报密钥错误 | 多为未拉到最新镜像：确认已 `git pull` 且 **`build api --no-cache`** 后再 `--force-recreate`。 |
| `curl \| grep` 报 **`grep: invalid option -- 'S'`** | 两行命令粘成一行，`grep` 误解析了第二个 `curl -sS`。整行只保留一条 `curl \| grep`，或分两行执行。 |

---

## 五、与本仓库相关的链接

- 远程仓库：<https://github.com/wuya521/tempmail>

---

*文件可随项目演进自行增删行；重大架构变更时更新「给 Cursor 的一行上下文」块。*
