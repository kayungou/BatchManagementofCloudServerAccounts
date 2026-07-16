# 安全策略

## 支持范围

安全修复优先提供给最新稳定版本和 `main`。旧版本可能要求先升级后再获得支持。

| 版本 | 安全更新 |
| --- | --- |
| 最新稳定版本 | 支持 |
| `main` | 尽力支持 |
| 更早版本 | 不保证 |

## 报告漏洞

不要通过公开 Issue、Discussion、Pull Request 或日志粘贴站点提交漏洞。请使用 GitHub 的[私有安全报告](https://github.com/kayungou/BatchManagementofCloudServerAccounts/security/advisories/new)。如果仓库地址发生变化，请同步更新本链接。

报告中请包含受影响版本、部署方式、复现步骤、影响范围和可行的缓解措施。所有 Token、密码、Cookie、`MASTER_KEY`、数据库 URL、IP 和个人信息都必须脱敏。

维护者目标是在 72 小时内确认收到报告，并在确认影响后协调修复和披露时间。复杂问题的实际修复时间取决于风险和兼容性。

## 密钥边界

- `MASTER_KEY`、DigitalOcean Token、SMTP 密码和托管 root 密码不得进入仓库、镜像层、GitHub Actions 日志或 GitHub Secrets。
- 运行密钥只保存在生产服务器的受限环境文件，并与数据库成对备份。
- 丢失 `MASTER_KEY` 后，数据库中的加密凭据无法恢复。
- 怀疑 Token 泄露时，应先在 DigitalOcean 撤销，再替换系统内凭据并检查审计日志。

请在公开披露前给维护者合理的修复时间。此策略不是服务等级协议，也不构成漏洞奖励承诺。
