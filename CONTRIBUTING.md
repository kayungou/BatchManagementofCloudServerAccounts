# 贡献指南

感谢参与 Cloud Account Manager。项目当前只支持 DigitalOcean，用户界面和主要文档使用中文，代码标识符、API 字段和提交类型使用英文。

## 开发环境

需要 Go 1.26、Node.js 22 或 24、npm、PostgreSQL 18 和 Docker Compose v2。

```bash
./scripts/dev-init.sh
make build
make serve
```

不要提交 `.env.local`、`.env.production`、数据库备份、DigitalOcean Token、SMTP 密码、`MASTER_KEY` 或托管的 root 密码。

## 分支与提交

- 从最新 `main` 创建短生命周期分支，例如 `feat/batch-create`、`fix/token-sync`、`docs/deployment`。
- 提交信息采用 Conventional Commits：`feat:`、`fix:`、`docs:`、`test:`、`refactor:`、`build:`、`ci:`、`chore:`。
- 一次 PR 只解决一个清晰问题，不混入无关格式化或重构。
- 破坏性变更必须在提交正文和 PR 中标记 `BREAKING CHANGE:`。

## 数据库迁移

- 在 `internal/database/migrations/` 新增顺序递增的 SQL 文件，不修改已经发布的迁移。
- 迁移必须可在现有数据上执行，并尽量遵循 expand/contract，保证滚动升级期间新旧应用可短暂共存。
- 删除列、收紧约束或大表重写需要单独说明备份、停机和回滚方案。
- 当前项目没有自动 down migration；应用回滚不等于数据库回滚。

## 质量检查

提交 PR 前运行：

```bash
make verify
```

至少确保以下命令通过：

```bash
go test ./...
go vet ./...
cd web && npm run typecheck && npm run build
bash -n scripts/*.sh
docker compose --env-file .env.local config --quiet
```

涉及数据访问层时设置 `TEST_DATABASE_URL`，确保 PostgreSQL 集成测试没有被跳过。涉及界面布局时在桌面和手机视口检查，并在 PR 中提供截图。

## Pull Request

- 关联对应 Issue，说明动机、行为变化和风险。
- 更新受影响的 README、`docs/` 和 `CHANGELOG.md`。
- 明确是否涉及迁移、环境变量、权限、加密数据或 DigitalOcean API。
- 不使用真实云账号进行破坏性测试；必须使用时，说明测试账号、成本和清理结果，但不要暴露凭据。

提交 PR 即表示你有权提供相关代码和文档，并同意贡献内容按项目的 `AGPL-3.0-only` 许可证分发。
