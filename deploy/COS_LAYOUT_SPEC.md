# StarMap COS 发布目录与版本清单规范

## 1. 目标

这份规范用于统一下面三件事：

1. GitHub Actions 把什么内容上传到 COS
2. CVM 上的 `deploy-starmapd.sh` 应该从哪里下载
3. 回滚、审计和多环境发布时，如何保持目录结构稳定

目标原则：

- 发布产物不可变
- 通道指针可变
- 支持健康检查失败后的快速回滚
- 支持历史版本长期保留
- 支持单机和多节点 VM 部署

## 2. COS 目录结构

推荐使用下面这套目录：

```text
starmapd/
  releases/
    <version>/
      manifest.json
      SHA256SUM
      starmapd-linux-amd64.tar.gz
  channels/
    prod/
      current.json
    staging/
      current.json
```

说明：

- `releases/<version>/`：不可变版本目录，一旦发布完成不再覆盖
- `channels/<channel>/current.json`：发布指针，表示当前通道应该部署哪个版本

## 3. version 命名建议

建议 version 使用以下二选一：

1. Git Tag

```text
v1.0.0
v1.0.1
```

2. 时间戳 + Git SHA

```text
2026.04.12-120501-abcdef1
```

建议规则：

- 版本名必须全局唯一
- 不能复用同一个 version 覆盖不同内容
- 不要把 `latest` 当 version 使用

## 4. 产物包结构

推荐 tar.gz 解压后得到：

```text
starmapd-linux-amd64/
  bin/
    starmapd
  config/
    starmapd.toml
  deploy/
    install-starmapd.sh
    deploy-starmapd.sh
    COS_LAYOUT_SPEC.md
    README.md
    systemd/
      install-starmapd-deploy-timer.sh
      starmapd-deploy.service
      starmapd-deploy.timer
```

说明：

- `bin/starmapd`：真正可执行文件
- `config/starmapd.toml`：配置模板
- `deploy/`：部署和发布相关文件，便于 CVM 上排查
- `deploy/install-starmapd.sh`：初始化安装脚本

## 5. manifest.json 规范

每个版本目录下必须有 `manifest.json`。

推荐字段如下：

```json
{
  "app": "starmapd",
  "version": "2026.04.12-120501-abcdef1",
  "git_sha": "abcdef1234567890",
  "os": "linux",
  "arch": "amd64",
  "artifact_name": "starmapd-linux-amd64.tar.gz",
  "artifact_url": "https://<cos-domain>/starmapd/releases/2026.04.12-120501-abcdef1/starmapd-linux-amd64.tar.gz",
  "sha256": "<sha256>",
  "released_at": "2026-04-12T12:05:01Z",
  "release_channel": "prod"
}
```

字段含义：

- `app`：应用名，固定 `starmapd`
- `version`：当前产物版本号
- `git_sha`：源代码提交 SHA
- `os` / `arch`：目标平台
- `artifact_name`：COS 中的产物文件名
- `artifact_url`：CVM 下载地址
- `sha256`：完整 tar.gz 文件的校验值
- `released_at`：发布时间，UTC 格式
- `release_channel`：该次发布面向的通道

## 6. current.json 规范

每个通道目录下必须有 `current.json`。

推荐字段如下：

```json
{
  "app": "starmapd",
  "channel": "prod",
  "version": "2026.04.12-120501-abcdef1",
  "manifest_url": "https://<cos-domain>/starmapd/releases/2026.04.12-120501-abcdef1/manifest.json",
  "updated_at": "2026-04-12T12:06:00Z"
}
```

字段含义：

- `app`：应用名
- `channel`：发布通道，例如 `prod`、`staging`
- `version`：当前通道所指向的版本
- `manifest_url`：部署端真正要读取的 manifest 地址
- `updated_at`：指针更新时间

## 7. 发布原则

### 7.1 不可变版本目录

`releases/<version>/` 一旦发布成功，不应再覆盖内容。

原因：

- 便于追溯
- 便于回滚
- 便于审计
- 避免同版本不同内容

### 7.2 可变通道指针

`channels/prod/current.json` 可以更新。

它的作用不是存储产物，而是表达：

- `prod` 当前应该跑哪个版本

### 7.3 先传产物，后切指针

推荐顺序：

1. 上传 tar.gz
2. 上传 `manifest.json`
3. 上传 `SHA256SUM`
4. 最后更新 `channels/<channel>/current.json`

这样部署端拿到的新指针，一定能读到完整产物。

## 8. CVM 部署端如何消费

`deploy-starmapd.sh` 推荐按下面顺序工作：

1. 下载 `current.json`
2. 读取 `manifest_url`
3. 下载 `manifest.json`
4. 读取 `artifact_url` 与 `sha256`
5. 下载 tar.gz
6. 校验 sha256
7. 解压到 `/opt/starmap/releases/<version>/`
8. 切换 `/opt/starmap/current`
9. 重启 systemd 服务
10. 做健康检查
11. 失败则切回 `/opt/starmap/previous`

## 9. VM 上推荐目录布局

```text
/opt/starmap/
  releases/
    2026.04.01-080000-1234567/
    2026.04.12-120501-abcdef1/
  current -> /opt/starmap/releases/2026.04.12-120501-abcdef1
  previous -> /opt/starmap/releases/2026.04.01-080000-1234567

/etc/starmapd/
  starmapd-node-1.toml

/var/lib/starmap/
  node-1/
```

说明：

- `releases/` 保存版本化程序文件
- `current` / `previous` 负责切换和回滚
- `/etc/starmapd` 放配置，不随版本切换
- `/var/lib/starmap` 放数据，不随版本切换

## 10. 建议开启 COS Versioning

建议对 COS Bucket 开启对象版本控制。

原因：

- 避免误删
- 避免误覆盖
- 可以追溯 channel 指针历史
- 对紧急回滚很有帮助

即使已经有 `releases/<version>/` 这种不可变目录，`channels/<channel>/current.json` 依然是会被覆盖更新的，所以开启 versioning 仍然很有价值。

## 11. 推荐保留策略

建议至少保留：

- `prod` 最近 10 个已发布版本
- `staging` 最近 20 个已发布版本
- 所有带正式 Tag 的版本长期保留

清理策略建议：

- 只清理 `releases/`
- 清理前确认没有任何节点还在使用该版本
- 不要自动删除 `channels/` 下的指针文件

## 12. 推荐的后续增强

后面如果继续演进，可以再补：

- `signature` 字段，用于产物签名校验
- `min_upgrade_from` 字段，用于约束最低可升级版本
- `rollback_hint` 字段，告诉部署端推荐回滚到哪个版本
- `breaking_change` 字段，用于提示需要人工介入的升级
