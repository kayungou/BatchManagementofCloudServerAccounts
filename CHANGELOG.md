# 变更记录

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 和[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

## [0.1.0] - 2026-07-17

### Added

- Go、PostgreSQL 18 和 React 管理后台。
- 管理员与普通用户权限、配额、系统设置、审计和账号归属转移。
- DigitalOcean Token 托管、账号同步、SSH Key 和 Droplet 生命周期管理。
- PostgreSQL Worker 队列、批量创建与批量操作。
- Docker Compose、Ubuntu/Debian systemd 安装器和本机初始化脚本。
- GitHub Actions CI、CodeQL、Dependabot、SemVer 发布包、GHCR 多架构镜像和审批部署流程。

### Fixed

- 修复 DigitalOcean 账号同步任务在没有 Provider Action ID 时持续显示执行中的问题。
- 支持 SMTP 465 端口隐式 TLS，并增加连接、读写和整体操作超时诊断。
- 完成审计动作、资源类型、云账号状态与 DigitalOcean 状态说明中文化。
- 增加区域中文位置、实例规格月流量信息和带宽数据可用性说明。
- 修复实例详情图标错位及窄屏操作按钮布局。

### Security

- Argon2id 密码哈希、AES-256-GCM 凭据加密、数据库会话、CSRF、限流、近期密码验证和审计日志。

[Unreleased]: https://github.com/kayungou/BatchManagementofCloudServerAccounts/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/kayungou/BatchManagementofCloudServerAccounts/releases/tag/v0.1.0
