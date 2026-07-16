# GitHub 仓库发布指南

本文说明首次上传、仓库保护和自动构建设置。项目当前模块路径为 `github.com/ikun/cloud-account-manager`；如果实际 GitHub 用户名或仓库名不同，必须在首次提交前同步修改 `go.mod`、Go import、徽章和安全报告链接。

## 1. 先决定仓库可见性与许可证

建议先创建私有仓库，完成真实 DigitalOcean Token、SMTP 和生产部署验证后再公开。

公开仓库必须由项目所有者明确选择许可证：

| 许可证 | 适用场景 |
| --- | --- |
| MIT | 最宽松，允许商业和闭源再分发 |
| Apache-2.0 | 宽松，同时提供明确专利授权 |
| AGPL-3.0 | 要求通过网络提供的修改版本公开对应源码 |
| 专有授权 | 不允许未经许可复制、修改或部署 |

不要直接复制整个目录到 GitHub 网页。应使用 Git CLI，让 `.gitignore` 排除 `.env.local`、构建产物和备份。

## 2. 首次提交前检查

```bash
make verify
find . -maxdepth 2 -type f -name '.env*' -print
```

确认只有 `.env.example` 和 `.env.production.example` 会进入仓库，真实 `.env.local`、`.env.production`、`bin/`、`web/dist/` 和 `backups/` 均未提交。

推荐安装 [Gitleaks](https://github.com/gitleaks/gitleaks) 后扫描：

```bash
gitleaks detect --no-git --redact --source .
```

## 3. 初始化并上传

安装并登录 GitHub CLI 后执行：

```bash
git init -b main
git add .
git status --short
git diff --cached --check
git commit -m "chore: initial project release"
gh auth login
gh repo create cloud-account-manager --private --source=. --remote=origin --push
```

决定公开后可在 GitHub 仓库 Settings 中修改可见性。若需要立即创建公开仓库，将 `--private` 改为 `--public`，但应先添加所有者确认的 `LICENSE`。

## 4. 仓库设置

在 Settings 中完成：

1. Actions 允许使用本仓库工作流，Workflow permissions 保持最小权限。
2. 启用 Issues、Private vulnerability reporting、Dependency graph、Dependabot alerts、Secret scanning 和 Push protection。
3. 为 `main` 创建 Ruleset：只允许 PR 合并、禁止 force push、合并前清除旧审批。
4. 要求以下检查通过：`Backend`、两个 `Frontend`、`Scripts and Compose`、`Container smoke test` 和 CodeQL。
5. 至少要求一名审批者；涉及迁移、安全和发布流程时建议要求代码所有者审批。

建议创建标签：

```text
type:bug type:feature type:docs type:security
area:backend area:web area:database area:digitalocean area:deployment
priority:p0 priority:p1 priority:p2 priority:p3
status:needs-triage status:blocked status:ready
breaking-change good first issue help wanted dependencies
```

安全漏洞本身不应进入公开 Issue；`type:security` 只用于已经公开的修复工作。

## 5. 自动化行为

- PR 和 `main` 推送运行 `.github/workflows/ci.yml`。
- `main` 和 PR 定期接受 CodeQL 分析。
- Dependabot 每周检查 Go、npm、Docker 和 GitHub Actions 依赖。
- 推送 `vX.Y.Z` 标签后，Release 工作流生成 Linux 发布包、SHA-256 校验文件和 GHCR 多架构镜像。
- 生产部署只能手工触发，并经过 GitHub `production` Environment 的审批。

运行时密钥不属于构建系统。`MASTER_KEY`、数据库密码、SMTP 密码和 DigitalOcean Token 必须留在服务器，不能上传为 Actions Secret。

## 6. 首个版本

完成许可证、`CHANGELOG.md` 和外部验证后：

```bash
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

Release 工作流会发布：

- `cloud-account-manager_0.1.0_linux_amd64.tar.gz`
- `cloud-account-manager_0.1.0_linux_arm64.tar.gz`
- `SHA256SUMS`
- `ghcr.io/<owner>/<repo>:0.1.0` 等 OCI 镜像标签

发布与部署的详细约束见 [RELEASING.md](RELEASING.md) 和 [DEPLOYMENT.md](DEPLOYMENT.md)。
