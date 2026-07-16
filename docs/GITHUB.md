# GitHub 仓库发布指南

本文说明公开仓库、分支保护和自动构建设置。当前仓库为 `kayungou/BatchManagementofCloudServerAccounts`，Go module 为 `github.com/kayungou/BatchManagementofCloudServerAccounts`。

## 1. 仓库与许可证

仓库为公开仓库，项目采用 `AGPL-3.0-only`。任何修改版本通过网络向用户提供功能时，都必须按 AGPL-3.0 要求提供对应源代码。

必须保留 `LICENSE` 和版权声明。不要直接复制整个目录到 GitHub 网页；应使用 Git，让 `.gitignore` 排除 `.env.local`、构建产物和备份。

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

## 3. 克隆与远端检查

安装并登录 GitHub CLI 后克隆：

```bash
gh auth login
gh repo clone kayungou/BatchManagementofCloudServerAccounts
cd BatchManagementofCloudServerAccounts
git remote -v
```

修改应从 `main` 创建独立分支，通过 Pull Request 合并。禁止强制推送受保护的 `main`。

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

完成 `CHANGELOG.md`、CI 和外部验证后：

```bash
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

Release 工作流会发布：

- `cloud-account-manager_0.1.0_linux_amd64.tar.gz`
- `cloud-account-manager_0.1.0_linux_arm64.tar.gz`
- `SHA256SUMS`
- `ghcr.io/kayungou/batchmanagementofcloudserveraccounts:0.1.0` 等 OCI 镜像标签

发布与部署的详细约束见 [RELEASING.md](RELEASING.md) 和 [DEPLOYMENT.md](DEPLOYMENT.md)。
