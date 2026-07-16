# 运维手册

## 健康检查

```bash
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
```

`healthz` 表示进程存活，`readyz` 还会检查 PostgreSQL。管理员“系统状态”页面显示版本、提交、数据库延迟、迁移版本、Worker 心跳和任务计数。

建议对以下情况告警：

- `/readyz` 连续失败。
- Worker 心跳超时或排队任务持续增长。
- 24 小时失败任务异常上升。
- PostgreSQL 磁盘、连接数或备份失败。
- DigitalOcean API 401/403、限流剩余额度过低。

## 日志

Docker：

```bash
docker compose --env-file .env.production -f deploy/docker-compose.production.yml logs -f api worker db
```

systemd：

```bash
sudo journalctl -u cloudmanager-api -u cloudmanager-worker -f
```

日志和 Issue 中必须移除 Token、Cookie、密码、`MASTER_KEY`、数据库 URL 和个人信息。

## 备份

数据库与 `MASTER_KEY` 必须成对备份：

```bash
docker compose --env-file .env.production -f deploy/docker-compose.production.yml \
  exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > cloud-account-manager.dump
cp .env.production cloudmanager.env.backup
chmod 0600 cloudmanager.env.backup
```

备份应加密并存放到另一故障域，定期在隔离环境执行恢复演练。只恢复数据库但没有原 `MASTER_KEY`，加密凭据不可用。

## 升级

1. 记录当前镜像 digest、迁移版本和备份位置。
2. 阅读 `CHANGELOG.md`，检查配置和数据库变化。
3. 在数据副本上运行迁移。
4. 执行生产部署并观察 `/readyz`、Worker 和失败任务。
5. 保留旧镜像与备份直到观察期结束。

## 故障处理

- 数据库不可用：停止重复重启，检查容量、连接和 PostgreSQL 日志。
- Worker 离线：API 可继续提供只读状态，但新任务会排队；恢复 Worker 后观察积压。
- Token 失效：在 DigitalOcean 撤销旧 Token，创建 Full Access 专用 Token，并通过近期密码验证替换。
- `MASTER_KEY` 不匹配：不要生成新密钥覆盖；恢复与数据库匹配的环境备份。
- 创建实例部分成功：先核对 DigitalOcean 控制台和任务结果，避免重复创建产生费用。

销毁、重建、扩容和创建 Droplet 会改变真实云资源并产生费用。生产操作必须使用审计账号、明确目标和变更窗口。
