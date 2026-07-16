# DigitalOcean 云服务器账号托管管理系统

[![CI](https://github.com/kayungou/BatchManagementofCloudServerAccounts/actions/workflows/ci.yml/badge.svg)](https://github.com/kayungou/BatchManagementofCloudServerAccounts/actions/workflows/ci.yml)
[![CodeQL](https://github.com/kayungou/BatchManagementofCloudServerAccounts/actions/workflows/codeql.yml/badge.svg)](https://github.com/kayungou/BatchManagementofCloudServerAccounts/actions/workflows/codeql.yml)

这是一个面向多用户的 DigitalOcean 云资源托管后台。后端使用 Go，数据存储使用 PostgreSQL 18，管理端使用 React + TypeScript。系统把耗时的云资源操作放入 PostgreSQL 任务队列，由独立 Worker 执行并追踪结果。

DigitalOcean API 行为以[官方 API 文档](https://docs.digitalocean.com/reference/api/reference/)为准。

> 创建、重建、扩容和销毁 Droplet 会操作真实云资源，可能产生费用或造成不可逆数据丢失。生产使用前必须完成备份，并使用专用 DigitalOcean Token 和测试账号验证流程。

## 已实现功能

- 邮箱注册、邮箱验证与安全重发、登录、退出、找回密码和数据库会话。
- 管理员用户管理、角色和状态管理、三维本地配额、系统状态、审计日志。
- 站点、注册开关、维护模式、会话时长、默认配额、时区和 SMTP 设置。
- 每个用户托管多个 DigitalOcean Token；同一 DigitalOcean Team 在全系统只能归属一个用户。
- DigitalOcean 账号状态、官方 Droplet 配额、余额、当月用量和速率限制同步。
- Droplet 列表、详情、每账号最多 10 台批量创建，以及开关机、重启、关机、快照、扩容、重建、备份和销毁等操作。
- Region、Size、Image、VPC、Project、Snapshot 和 SSH Key 目录与 SSH 公钥管理。
- SSH 公钥登录，或由系统生成 root 密码并通过 cloud-init 注入。系统不保存 SSH 私钥。
- 用户与管理员在近期密码验证后查看或更新托管 root 密码。
- 创建后分配 Project；分配失败时保留已创建的 Droplet，并将任务标为部分成功。
- Docker Compose、本机开发、Ubuntu 24.04 / Debian 12 systemd 三种运行方式。

首版只支持 DigitalOcean，不支持跨云账号批量操作。对同一账号执行批量实例操作时，上限为 100 台；创建实例时每批上限为 10 台。

## 文档

- [GitHub 上传与仓库设置](docs/GITHUB.md)
- [系统架构](docs/ARCHITECTURE.md)
- [生产部署](docs/DEPLOYMENT.md)
- [发布流程](docs/RELEASING.md)
- [运维手册](docs/OPERATIONS.md)
- [贡献指南](CONTRIBUTING.md)
- [安全策略](SECURITY.md)
- [变更记录](CHANGELOG.md)

## 安全模型

- 用户密码使用 Argon2id 哈希。
- DigitalOcean Token、SMTP 密码和托管 root 密码使用 AES-256-GCM 加密。
- `MASTER_KEY` 是解密这些数据的唯一主密钥。已有加密数据后不得更换，必须与数据库一起备份。
- Token 保存后不再通过 API 或页面回显。
- Cookie 会话、CSRF 校验、登录和注册限流、近期密码验证以及审计日志默认启用。
- 生产环境应只让 API 监听回环地址，并由 HTTPS 反向代理对外提供服务。
- 本系统需要用户确认 DigitalOcean Token 已授予 **Full Access**。请在 DigitalOcean 控制台创建专用 Token，不要与其他系统共用。

## 环境要求

本机开发需要：

- Go 1.26 或更高版本
- Node.js 22 或更高版本，以及 npm
- PostgreSQL 18
- macOS、Linux 或其他可运行以上工具的系统

仓库默认开发数据库参数为：

```text
地址: 127.0.0.1:5432
用户: ikun
密码: ServBay.dev
数据库: cloud_account_manager
```

以上固定密码仅用于你指定的本机开发环境，属于公开已知值。公网、Docker 生产和远程数据库必须使用独立随机密码。

## 本机快速开始

确认 PostgreSQL 18 已启动，并且 `ikun` 用户具备创建数据库和扩展的权限，然后执行：

```bash
./scripts/dev-init.sh --admin-email admin@example.com
make build
make serve
```

初始化脚本会：

1. 从 `.env.example` 生成权限为 `0600` 的 `.env.local`。
2. 生成 32 字节随机 `MASTER_KEY`，已有密钥时保持不变。
3. 创建 `cloud_account_manager` 数据库，已存在时保留数据。
4. 执行所有数据库迁移。
5. 按需创建初始管理员，已存在的邮箱不会重复创建。

管理员密码长度必须为 12 到 128 个字符。也可以不传邮箱，按初始化脚本提示交互创建；之后运行 `make admin` 也能创建管理员。

服务启动后访问 [http://127.0.0.1:8080](http://127.0.0.1:8080)。开发模式在 SMTP 未配置时会把验证链接写入服务日志，并在相关接口响应中提供开发 Token；生产环境不会暴露 Token。

前后端分开开发时运行：

```bash
make serve
make web-dev
```

Vite 地址为 `http://127.0.0.1:5173`，API 请求会代理到 `127.0.0.1:8080`。

## Docker Compose

首次初始化数据库并可选创建管理员：

```bash
./scripts/dev-init.sh --docker --admin-email admin@example.com
docker compose --env-file .env.local up -d
```

也可以直接执行：

```bash
make docker-up
```

`make docker-up` 会生成环境文件、构建镜像、启动 PostgreSQL 18、API 和 Worker；API 启动时自动迁移数据库。若还没有管理员，再执行：

```bash
docker compose --env-file .env.local run --rm api admin -email admin@example.com
```

常用命令：

```bash
make docker-logs       # 跟踪 API 和 Worker 日志
make docker-config     # 展开并校验 Compose 配置
make docker-down       # 停止容器，保留数据库卷
```

PostgreSQL 18 官方镜像使用主版本分层的数据目录，因此 Compose 将命名卷挂载到 `/var/lib/postgresql`。不要把该卷改回旧版本常用的 `/var/lib/postgresql/data`。

对外使用域名时，编辑 `.env.local`：

```dotenv
APP_BASE_URL=https://cloud.example.com
COOKIE_SECURE=true
```

然后运行 `docker compose --env-file .env.local up -d` 重新创建服务。

此处的 `docker-compose.yml` 面向本机开发和源码构建。生产环境应使用 GHCR 镜像及 `deploy/docker-compose.production.yml`，其数据库密码和 `MASTER_KEY` 都是必填项，详见[生产部署](docs/DEPLOYMENT.md)。

## GitHub 自动构建与发布

仓库内置以下 GitHub Actions：

- PR 和 `main` 推送：Go 格式、Vet、Race Test、PostgreSQL 18 集成测试、Node.js 22/24 前端构建、ShellCheck 和 Docker 冒烟测试。
- CodeQL：分析 Go 与 JavaScript/TypeScript，并按周重新扫描。
- Dependabot：每周检查 Go、npm、Docker 和 Actions 依赖。
- `vX.Y.Z` 标签：生成 Linux `amd64/arm64` 发布包、校验文件和 GHCR 多架构镜像。
- 手工生产部署：使用 GitHub `production` Environment 审批后通过 SSH 部署指定镜像 digest。

仓库设置、分支保护和 Actions 说明见 [GitHub 仓库发布指南](docs/GITHUB.md)。正式 Release 会验证 AGPL-3.0 许可证文件存在。

## Ubuntu / Debian 安装

安装脚本仅支持 Ubuntu 24.04 和 Debian 12 的 amd64/arm64。默认安装 PostgreSQL 18，下载并校验 Go 1.26 与 Node.js 22，构建前后端，创建受限系统用户并启动两个 systemd 服务。

```bash
sudo ./scripts/install.sh \
  --app-base-url https://cloud.example.com \
  --admin-email admin@example.com
```

使用外部 PostgreSQL 18：

```bash
sudo ./scripts/install.sh \
  --app-base-url https://cloud.example.com \
  --database-url 'postgres://user:password@db.example.com:5432/cloud_account_manager?sslmode=require' \
  --admin-email admin@example.com
```

数据库 URL 中的用户名和密码必须按 URL 规则编码。外部数据库用户需要能够创建 `citext`、`pgcrypto` 扩展和应用表。

安装位置：

```text
/usr/local/bin/cloudmanager                 Go 二进制
/opt/cloud-account-manager/web/dist         前端静态文件
/etc/cloud-account-manager/cloudmanager.env 生产环境和主密钥
/etc/systemd/system/cloudmanager-api.service
/etc/systemd/system/cloudmanager-worker.service
```

服务管理：

```bash
sudo systemctl status cloudmanager-api cloudmanager-worker
sudo systemctl restart cloudmanager-api cloudmanager-worker
sudo journalctl -u cloudmanager-api -u cloudmanager-worker -f
```

再次从新版源码运行同一安装命令即可升级。安装器会保留现有生产配置、数据库和 `MASTER_KEY`，重新构建应用、执行迁移并重启服务。

## HTTPS 反向代理

应用默认只监听 `127.0.0.1:8080`，不直接处理 TLS。生产环境必须在同一台主机配置 Nginx、Caddy 或等效反向代理。

Nginx 示例：

```nginx
server {
    listen 80;
    server_name cloud.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name cloud.example.com;

    ssl_certificate     /etc/letsencrypt/live/cloud.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/cloud.example.com/privkey.pem;

    client_max_body_size 2m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_connect_timeout 10s;
        proxy_read_timeout 90s;
    }
}
```

Caddy 示例：

```caddyfile
cloud.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

同时确认生产环境包含：

```dotenv
APP_ENV=production
APP_BASE_URL=https://cloud.example.com
COOKIE_SECURE=true
DEV_EXPOSE_TOKENS=false
```

反向代理必须覆盖而不是追加来自公网的 `X-Forwarded-For` 等头部。防火墙不应向公网开放 8080 和 5432 端口。

## 首次配置

1. 用初始管理员登录。
2. 在“系统设置”中确认站点名称、时区、会话时长和默认用户配额。
3. 配置 SMTP 并发送测试邮件。
4. SMTP 测试成功前关闭公开注册；生产环境不会在页面或接口中暴露邮箱验证 Token。
5. 添加 DigitalOcean Token，勾选 Full Access 确认并等待首次同步完成。
6. 添加或选择 DigitalOcean SSH 公钥，再创建 Droplet。

DigitalOcean `/v2/account` 目前只直接提供 `droplet_limit` 和 `floating_ip_limit`。界面中的 vCPU、内存和 Droplet 本地配额由本系统单独统计，默认不限制。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` 会启用生产行为 |
| `LISTEN_ADDR` | `127.0.0.1:8080` | API 监听地址 |
| `APP_BASE_URL` | `http://127.0.0.1:8080` | 邮件链接和站点外部地址 |
| `DATABASE_URL` | 本机开发连接 | PostgreSQL 18 连接 URL |
| `MASTER_KEY` | 无 | 必填，Base64 编码的 32 字节密钥 |
| `COOKIE_NAME` | `cloud_manager_session` | 会话 Cookie 名称 |
| `COOKIE_SECURE` | 生产环境为 `true` | 仅允许 HTTPS 发送 Cookie |
| `SESSION_TTL` | `168h` | 数据库设置无效时的会话时长回退值 |
| `FRONTEND_DIR` | `web/dist` | 前端构建产物目录 |
| `RUN_WORKER` | 开发环境为 `true` | 是否在 API 进程内运行 Worker |
| `WORKER_CONCURRENCY` | `4` | Worker 并发数，范围 1-32 |
| `WORKER_POLL_INTERVAL` | `2s` | 任务轮询间隔 |
| `SYNC_INTERVAL` | `5m` | 云账号周期同步间隔 |
| `DEV_EXPOSE_TOKENS` | 开发环境为 `true` | 是否向开发响应暴露一次性 Token，生产必须关闭 |

完整开发模板见 `.env.example`。

## 命令行

```bash
cloudmanager serve                         # API 服务
cloudmanager worker                        # 独立 Worker
cloudmanager migrate                       # 执行数据库迁移
cloudmanager admin -email admin@example.com # 创建管理员
cloudmanager keygen                        # 生成 MASTER_KEY
cloudmanager version                       # 查看版本、提交和构建时间
```

程序启动时也会自动执行尚未应用的迁移。生产环境的 systemd API 单元在启动前额外执行一次迁移，确保 Worker 不会与首次迁移竞争。

## 测试与构建

```bash
make test                       # Go 测试、前端类型检查、Go 格式检查
make verify                     # 完整本地质量门，包括构建、Vet、Shell 与 Compose
make build                      # 构建前端和 bin/cloudmanager
VERSION=v0.1.0 make release     # 生成 Linux amd64/arm64 发布包
```

GitHub CI 会设置 `TEST_DATABASE_URL` 并启动 PostgreSQL 18，因此数据访问层集成测试不会被跳过。

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

`/healthz` 表示进程正常，`/readyz` 还会检查数据库连接。

## 备份与恢复

必须同时备份数据库和包含 `MASTER_KEY` 的环境文件。只有数据库、没有原主密钥时，DigitalOcean Token、SMTP 密码和 root 密码都无法恢复。

Docker 数据库备份示例：

```bash
docker compose --env-file .env.local exec -T db \
  pg_dump -U ikun -d cloud_account_manager -Fc > cloud-account-manager.dump
cp .env.local cloud-account-manager.env.backup
chmod 600 cloud-account-manager.env.backup
```

systemd 部署备份示例：

```bash
sudo -u postgres pg_dump -d cloud_account_manager -Fc > cloud-account-manager.dump
sudo cp /etc/cloud-account-manager/cloudmanager.env ./cloudmanager.env.backup
sudo chmod 600 ./cloudmanager.env.backup
```

恢复前停止 API 和 Worker，将数据库恢复到空库，再放回原环境文件并启动服务。不要在恢复过程中生成新的 `MASTER_KEY`。

## 故障排查

- API 无法启动：检查 `MASTER_KEY` 是否为 Base64 编码的 32 字节值，以及 `DATABASE_URL` 是否可连接。
- 页面显示数据库未就绪：运行 `curl http://127.0.0.1:8080/readyz` 并查看 API 日志。
- 任务一直排队：确认 Worker 正常，检查管理员“系统状态”中的心跳和 `journalctl`/Compose 日志。
- 注册返回邮件不可用：先由管理员配置 SMTP 并完成测试；待验证用户可在登录页重新发送验证邮件。
- DigitalOcean 返回 401/403：重新生成 Full Access Token，并在账号页面经过近期密码验证后替换。
- 创建成功但 Project 分配失败：Droplet 会保留，任务结果会显示部分成功，可在 DigitalOcean 控制台或后续操作中重新分配。
- Docker 升级 PostgreSQL 镜像前：先做逻辑备份，不能只替换跨主版本镜像后直接复用数据目录。

## 目录结构

```text
cmd/cloudmanager/       程序入口和命令行
internal/config/        环境配置
internal/database/      PostgreSQL 连接和迁移
internal/digitalocean/  DigitalOcean API 客户端
internal/httpapi/       HTTP API、认证与权限
internal/security/      密码哈希、加密和令牌
internal/store/         数据访问层
internal/worker/        后台任务执行器
web/                    React 管理后台
deploy/systemd/         systemd 服务单元
deploy/docker-compose.production.yml  GHCR 生产部署模板
.github/                CI、发布、部署和协作模板
docs/                   架构、部署、发布和运维文档
scripts/                初始化和安装脚本
```

## 许可证

Copyright (C) 2026 kayungou。

本项目采用 [GNU Affero General Public License v3.0](LICENSE)，SPDX 标识为 `AGPL-3.0-only`。修改后通过网络向用户提供本程序功能时，必须按许可证要求向这些用户提供对应源代码。
