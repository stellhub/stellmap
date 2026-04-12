# Deploy 说明

这个目录承载的是 `StarMap` 的 VM 发布与升级方案，而不是节点初始化安装的全部内容。

当前包含：

- [install-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/install-starmapd.sh)
- [deploy-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/deploy-starmapd.sh)
- [COS_LAYOUT_SPEC.md](/E:/PersonalCode/GoProject/StarMap/deploy/COS_LAYOUT_SPEC.md)
- [starmapd-deploy.service](/E:/PersonalCode/GoProject/StarMap/deploy/systemd/starmapd-deploy.service)
- [starmapd-deploy.timer](/E:/PersonalCode/GoProject/StarMap/deploy/systemd/starmapd-deploy.timer)
- [install-starmapd-deploy-timer.sh](/E:/PersonalCode/GoProject/StarMap/deploy/systemd/install-starmapd-deploy-timer.sh)
- GitHub Actions 工作流：
  [release-starmapd.yml](/E:/PersonalCode/GoProject/StarMap/.github/workflows/release-starmapd.yml)
  和说明文档：
  [README.md](/E:/PersonalCode/GoProject/StarMap/.github/workflows/README.md)

## 1. 两个脚本的职责边界

当前仓库里有两个和部署相关的脚本：

- [install-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/install-starmapd.sh)
- [deploy-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/deploy-starmapd.sh)

它们看起来相近，但职责其实不同。

### 1.1 install-starmapd.sh

它负责“第一次把节点装起来”：

- 在机器上编译二进制
- 创建运行用户
- 渲染 `starmapd.toml`
- 生成 `systemd service`
- 启动节点

适合场景：

- 新机器第一次安装
- 初始化单机或集群节点

### 1.2 deploy-starmapd.sh

它负责“节点已经安装好之后的版本升级和回滚”：

- 从 `current.json` 或 `manifest.json` 拉版本信息
- 下载 tar.gz
- 做 sha256 校验
- 解压到版本目录
- 切换 `current / previous`
- 重启服务
- 健康检查
- 失败回滚

适合场景：

- 已安装节点的版本发布
- 日常升级
- 回滚

## 2. 是否可以只保留 deploy-starmapd.sh

当前阶段我不建议直接删除 [install-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/install-starmapd.sh)。

原因很实际：

- `install-starmapd.sh` 负责“初始化安装”
- `deploy-starmapd.sh` 负责“发布升级”

如果只保留后者，你还需要额外补一套初始化逻辑：

- 创建用户
- 写配置
- 生成 systemd unit
- 初始化目录结构

也就是说，**现在它们不是重复关系，而是 bootstrap 和 upgrade 两个阶段的分工关系**。

后续如果你想继续收敛，也更推荐：

- 保留 `deploy/install-starmapd.sh` 作为首装脚本
- 保留 `deploy/deploy-starmapd.sh` 作为升级脚本

而不是现在就强行合并。

## 3. GitHub Actions 工作流说明

工作流文件：

- [release-starmapd.yml](/E:/PersonalCode/GoProject/StarMap/.github/workflows/release-starmapd.yml)

功能：

1. 使用 `Go 1.25.9` 构建 `linux/amd64` 版本
2. 执行 `go test ./...`
3. 打包发布产物
4. 生成 `manifest.json` / `current.json`
5. 上传到腾讯云 COS
6. 可选更新 channel 指针

### 3.1 需要的 GitHub Secrets

下面这些是必需的：

- `TENCENT_SECRET_ID`
- `TENCENT_SECRET_KEY`
- `TENCENT_COS_BUCKET`
- `TENCENT_COS_REGION`

建议权限：

- `TENCENT_SECRET_ID / TENCENT_SECRET_KEY`
  只给当前 bucket 的最小上传权限
- `TENCENT_COS_BUCKET`
  例如 `starmap-release-1250000000`
- `TENCENT_COS_REGION`
  例如 `ap-guangzhou`

### 3.2 需要的 GitHub Variables

建议配置：

- `COS_BASE_URL`

示例：

```text
https://starmap-release-1250000000.cos.ap-guangzhou.myqcloud.com
```

说明：

- `workflow_dispatch` 触发时，也可以在表单里直接传 `cos_base_url`
- 如果不传，就回退使用 `vars.COS_BASE_URL`

### 3.3 触发方式

当前支持：

- 手工触发 `workflow_dispatch`
- 推送 `v*` tag 自动触发

## 4. systemd timer 轮询示例

为了让 CVM 自动轮询新版本，可以使用本目录下的 systemd 示例：

- [starmapd-deploy.service](/E:/PersonalCode/GoProject/StarMap/deploy/systemd/starmapd-deploy.service)
- [starmapd-deploy.timer](/E:/PersonalCode/GoProject/StarMap/deploy/systemd/starmapd-deploy.timer)

### 4.1 使用方式

推荐直接使用一键生成脚本：

```bash
sudo bash deploy/systemd/install-starmapd-deploy-timer.sh \
  --channel-url https://starmap-release-1250000000.cos.ap-guangzhou.myqcloud.com/starmapd/channels/prod/current.json \
  --service-name starmapd-node-1 \
  --health-url http://127.0.0.1:8080/readyz \
  --workdir /opt/src/StarMap \
  --enable-now
```

这个脚本会：

1. 从模板生成 `.service` 和 `.timer`
2. 自动替换占位符
3. `daemon-reload`
4. 可选直接 `enable --now`

### 4.2 查看轮询状态

```bash
sudo systemctl status starmapd-deploy.timer
sudo systemctl list-timers | grep starmapd-deploy
```

### 4.3 手工触发一次

```bash
sudo systemctl start starmapd-deploy.service
sudo journalctl -u starmapd-deploy.service -n 200 --no-pager
```
