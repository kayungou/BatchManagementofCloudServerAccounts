## 变更说明

<!-- 说明问题、实现方式和用户可见行为。 -->

关联 Issue：

## 验证

- [ ] `make test`
- [ ] `go vet ./...`
- [ ] `npm --prefix web run build`
- [ ] Shell 与 Compose 配置检查
- [ ] 涉及界面时已检查桌面和手机视口并附截图

## 风险检查

- [ ] 没有提交 Token、密码、Cookie、数据库备份、`.env.local`、`.env.production` 或 `MASTER_KEY`
- [ ] 已说明数据库迁移及回滚影响，或本次不涉及迁移
- [ ] 已说明新增或变更的环境变量，或本次不涉及配置
- [ ] 已评估权限、加密数据、审计和 DigitalOcean API 影响
- [ ] 已更新 README、`docs/` 和 `CHANGELOG.md`，或说明无需更新
- [ ] 破坏性变更和真实云资源费用已明确标记
