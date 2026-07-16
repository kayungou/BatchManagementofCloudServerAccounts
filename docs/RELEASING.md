# 发布流程

项目使用语义化版本，Git 标签是发布版本的唯一来源。

## 版本规则

- `v0.1.0`：功能版本。
- `v0.1.1`：兼容的缺陷和安全修复。
- `v0.2.0`：兼容的新功能。
- `v1.0.0`：稳定公共契约。
- `v1.0.0-rc.1`：预发布版本，不应自动视为稳定生产版。

## 发布前检查

1. `LICENSE` 为项目确认的 GNU AGPL v3.0 正文。
2. `CHANGELOG.md` 中的 `[Unreleased]` 已整理为本次版本。
3. `make verify` 通过，CI 和 CodeQL 无阻塞问题。
4. PostgreSQL 迁移已在生产数据副本上验证。
5. 涉及 DigitalOcean 或 SMTP 的变更已使用专用测试账号完成外部验证。
6. 已评估真实云资源费用、不可逆操作、备份和回滚。

## 创建发布

```bash
git switch main
git pull --ff-only
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

`.github/workflows/release.yml` 会先复用完整 CI，然后：

- 构建 Linux `amd64` 和 `arm64` 压缩包。
- 将二进制、`web/dist`、环境模板和 systemd units 放在同一发布包。
- 生成 `SHA256SUMS` 和 GitHub build provenance。
- 构建并推送带 SBOM/provenance 的 GHCR 多架构镜像。
- 创建 GitHub Release 并生成发布说明。

Release 工作流在缺少或误改 AGPL-3.0 `LICENSE` 时会主动失败。

## 本地生成发布包

```bash
VERSION=v0.1.0 COMMIT=<full-sha> BUILD_TIME=2026-07-17T00:00:00Z make release
```

产物位于 `dist/release/`。验证：

```bash
cd dist/release
sha256sum -c SHA256SUMS
```

## 发布后

1. 检查 GitHub Release 文件和校验值。
2. 在 GHCR 中记录镜像 digest，而不是只记录可变标签。
3. 先部署测试环境，再通过 `production` Environment 审批部署生产。
4. 更新运行手册和已知问题，关闭对应 Milestone。

删除或移动已发布标签会破坏可追溯性。发现问题时发布新的补丁版本，不覆盖现有产物。
