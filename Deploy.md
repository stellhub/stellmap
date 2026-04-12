# StarMap 部署说明

当前仓库已经切换为“GitHub Actions 直推 CVM + 本地 `/data/start.sh` 启动”的部署方式。

## 1. 目标机最终文件

每次发布后，目标 CVM 的 `/data` 目录下会有 4 个文件：

- `/data/starmapd.toml`
- `/data/starmapd`
- `/data/starmapctl`
- `/data/start.sh`

其中：

- `starmapd.toml` 是运行时读取的配置文件
- `starmapd` 是服务端二进制
- `starmapctl` 是控制面 CLI
- `start.sh` 是本地安装并启动 systemd 服务的脚本

## 2. 最简单的启动方式

在目标机器上执行：

```bash
sudo bash /data/start.sh
```

默认行为：

1. 从 `/data` 读取 `starmapd`、`starmapctl`、`starmapd.toml`
2. 安装到 `/opt/starmap/bin`
3. 安装配置到 `/etc/starmapd/starmapd.toml`
4. 生成 `systemd` 服务 `starmapd`
5. 启动并做健康检查

## 3. 默认配置文件

默认配置文件是：

- [config/starmapd.toml](/E:/PersonalCode/GoProject/StarMap/config/starmapd.toml)

当前仓库里的这份默认配置已经改成“可直接启动的单机配置”，默认值包括：

- `node.id = 1`
- `cluster_id = 100`
- `http_addr = "0.0.0.0:8080"`
- `admin_addr = "127.0.0.1:18080"`
- `grpc_addr = "0.0.0.0:19090"`
- 单节点 `peer_*` 地址簿

如果你要部署多节点集群，需要在发布前或发布后按节点修改 `/data/starmapd.toml` 中的：

- `node.id`
- `server.http_addr`
- `server.grpc_addr`
- `cluster.peer_ids`
- `cluster.peer_http_addrs`
- `cluster.peer_grpc_addrs`
- `cluster.peer_admin_addrs`

## 4. GitHub Actions 直推方式

工作流文件：

- [release-starmapd.yml](/E:/PersonalCode/GoProject/StarMap/.github/workflows/release-starmapd.yml)

工作流会：

1. 构建 `starmapd`
2. 构建 `starmapctl`
3. 复制 `config/starmapd.toml`
4. 复制 `deploy/start.sh`
5. 通过 SSH 上传到一台或多台 CVM 的 `/data`
6. 可选执行远程命令，例如 `sudo bash /data/start.sh`

## 5. 需要配置的 Actions Secrets / Variables

- `VM_HOSTS`
- `VM_USER`
- `VM_PORT`
- `VM_SSH_KEY` 或 `VM_SSH_PASSWORD`

多台机器使用分号分隔，例如：

```text
10.0.0.11;10.0.0.12;10.0.0.13
```

可选：

- `REMOTE_POST_UPLOAD_CMD`

推荐值：

```text
sudo bash /data/start.sh
```

## 6. 常用检查命令

查看服务状态：

```bash
sudo systemctl status starmapd
```

查看日志：

```bash
sudo journalctl -u starmapd -f
```

查看健康检查：

```bash
curl http://127.0.0.1:8080/readyz
```

查看控制面状态：

```bash
curl \
  -H "Authorization: Bearer starmap" \
  http://127.0.0.1:18080/admin/v1/status
```
