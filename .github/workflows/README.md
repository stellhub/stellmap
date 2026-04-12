# GitHub Actions 说明

当前仓库已经有发布工作流：

- [release-starmapd.yml](/E:/PersonalCode/GoProject/StarMap/.github/workflows/release-starmapd.yml)

## 1. 它会自动执行吗

有工作流文件之后，GitHub 会识别它，但是否真正开始构建，取决于触发条件。

当前这个工作流支持两种触发方式：

- 手工触发 `workflow_dispatch`
- 推送 `v*` tag 自动触发

也就是说：

- 仅仅把文件提交到仓库，不会在每次普通 push 时自动构建
- 你需要手工点 `Run workflow`，或者推送一个像 `v1.0.0` 这样的 tag

## 2. 需要配置什么

### 2.1 GitHub Secrets

在仓库的 `Settings -> Secrets and variables -> Actions -> Secrets` 中添加：

- `TENCENT_SECRET_ID`
- `TENCENT_SECRET_KEY`
- `TENCENT_COS_BUCKET`
- `TENCENT_COS_REGION`

用途：

- `TENCENT_SECRET_ID / TENCENT_SECRET_KEY`
  用于让 GitHub Actions 调用 COS 上传接口
- `TENCENT_COS_BUCKET`
  例如 `starmap-release-1250000000`
- `TENCENT_COS_REGION`
  例如 `ap-guangzhou`

### 2.2 GitHub Variables

在仓库的 `Settings -> Secrets and variables -> Actions -> Variables` 中建议添加：

- `COS_BASE_URL`

示例：

```text
https://starmap-release-1250000000.cos.ap-guangzhou.myqcloud.com
```

这个值会用来生成：

- `artifact_url`
- `manifest_url`

## 3. 构建完成后是谁推送到 COS

当前设计里，是 **GitHub Actions 主动推送到 COS**，不是腾讯云去 GitHub 拉结果。

链路是：

1. GitHub Actions 构建 `starmapd`
2. GitHub Actions 打包 tar.gz
3. GitHub Actions 生成 `manifest.json / current.json`
4. GitHub Actions 通过 `coscmd` 上传到腾讯云 COS

然后在腾讯云 CVM 上：

5. [deploy-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/deploy-starmapd.sh) 轮询 COS 的 `current.json`
6. 发现新版本后再下载并部署

所以是：

- GitHub -> COS
- CVM <- COS

不是：

- 腾讯云主动去 GitHub 拉 Actions 产物

## 4. 最推荐的第一次验证方式

建议这样做：

1. 先把工作流文件提交到默认分支
2. 在 GitHub 上配置好上面的 Secrets 和 Variables
3. 手工触发一次 `workflow_dispatch`
4. 去 COS 上确认是否已经出现：
   - `starmapd/releases/<version>/manifest.json`
   - `starmapd/releases/<version>/starmapd-linux-amd64.tar.gz`
   - `starmapd/channels/prod/current.json`
5. 再到 CVM 上手工运行一次：

```bash
sudo bash deploy/deploy-starmapd.sh \
  --channel-url https://<your-cos-domain>/starmapd/channels/prod/current.json \
  --service-name starmapd-node-1 \
  --health-url http://127.0.0.1:8080/readyz
```

6. 确认没问题后，再启用 timer 轮询
