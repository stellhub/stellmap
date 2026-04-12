# StarMap 部署说明

本文档聚焦一件事：

- 在一台全新的 Linux 机器上，如何通过 [install-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/install-starmapd.sh) 完成 `StarMap` 的单机部署
- 当你有三台新的机器时，如何填写节点 IP 并完成三节点集群部署

当前文档默认目标系统是常见的 `Ubuntu / Debian` 系 Linux，CPU 架构以 `linux-amd64` 为例。

## 1. 部署前准备

### 1.1 你需要准备什么

单机测试至少需要：

- 1 台新的 Linux 机器
- 这台机器可以访问 GitHub
- 你可以使用 `sudo`

三节点集群至少需要：

- 3 台彼此内网互通的 Linux 机器
- 每台机器都可以访问 GitHub
- 每台机器都可以使用 `sudo`

### 1.2 端口规划

每个 StarMap 节点会用到 3 类地址：

- `HTTP`：对外注册、发现、健康检查、指标抓取，默认 `8080`
- `Admin`：控制面接口，默认 `127.0.0.1:18080`
- `gRPC`：节点之间复制和快照传输，默认 `19090`

推荐规则：

- `HTTP` 和 `gRPC` 使用机器自己的内网 IP
- `Admin` 保持 `127.0.0.1:18080`

最小安全组建议：

- 放通 `8080/tcp`
- 仅在集群节点内网之间放通 `19090/tcp`
- 不需要对外放通 `18080/tcp`

## 2. 安装 Go 1.25.9

下面命令使用 Go 官方下载地址安装 `Go 1.25.9`。Go 官方下载页和安装说明见：

- [Go Downloads](https://go.dev/dl/)
- [Go Install Guide](https://go.dev/doc/install)

### 2.1 安装依赖

```bash
sudo apt-get update
sudo apt-get install -y curl git tar build-essential
```

### 2.2 下载并安装 Go 1.25.9

```bash
cd /tmp
curl -LO https://go.dev/dl/go1.25.9.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.25.9.linux-amd64.tar.gz
```

### 2.3 配置 PATH

```bash
cat <<'EOF' >> ~/.profile
export PATH=/usr/local/go/bin:$PATH
EOF

source ~/.profile
go version
```

预期看到类似输出：

```text
go version go1.25.9 linux/amd64
```

## 3. 从 GitHub 拉取代码并构建

先准备源码目录：

```bash
sudo mkdir -p /opt/src
sudo chown "$USER":"$USER" /opt/src
cd /opt/src
```

然后从 GitHub 拉取仓库：

```bash
git clone <你的-github-仓库地址> StarMap
cd /opt/src/StarMap
```

建议先确认仓库能正常构建：

```bash
go test ./...
go build ./cmd/starmapd
```

如果这里能通过，说明这台机器的 Go 环境和依赖已经准备好了。

## 4. 单机部署

### 4.1 单机部署命令

在源码目录执行：

```bash
cd /opt/src/StarMap
chmod +x deploy/install-starmapd.sh

sudo bash deploy/install-starmapd.sh \
  --repo-dir /opt/src/StarMap \
  --node-id 1 \
  --cluster-id 100 \
  --region default \
  --http-addr 0.0.0.0:8080 \
  --admin-addr 127.0.0.1:18080 \
  --grpc-addr 0.0.0.0:19090 \
  --admin-token starmap
```

### 4.2 这个命令会做什么

脚本会自动完成这些动作：

1. 编译 `./cmd/starmapd`
2. 安装二进制到 `/opt/starmap/bin/starmapd`
3. 从 [starmapd.toml](/E:/PersonalCode/GoProject/StarMap/config/starmapd.toml) 模板复制并渲染出节点配置
4. 生成 `systemd` 服务
5. 启动服务

典型产物包括：

- 二进制：`/opt/starmap/bin/starmapd`
- 配置：`/etc/starmapd/starmapd-node-1.toml`
- 数据目录：`/var/lib/starmap/node-1`
- 服务名：`starmapd-node-1`

### 4.3 单机部署后检查

查看服务状态：

```bash
sudo systemctl status starmapd-node-1
```

查看日志：

```bash
sudo journalctl -u starmapd-node-1 -f
```

查看健康检查：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

查看控制面状态：

```bash
curl \
  -H "Authorization: Bearer starmap" \
  http://127.0.0.1:18080/admin/v1/status
```

## 5. 三节点集群怎么填写 IP

这是最关键的地方。先记住 3 条规则：

1. `cluster-id` 三台机器必须一样，因为它们属于同一个集群
2. `node-id` 每台机器必须唯一
3. `peer-*` 地址映射三台机器都必须写同一份全集，只是各自的 `http-addr` / `grpc-addr` 不同

假设你有三台新的机器，它们的内网 IP 是：

| 节点 | 内网 IP |
| --- | --- |
| node-1 | `10.0.0.11` |
| node-2 | `10.0.0.12` |
| node-3 | `10.0.0.13` |

那么推荐填写方式就是：

| 节点 | `node-id` | `cluster-id` | `http-addr` | `grpc-addr` | `admin-addr` |
| --- | --- | --- | --- | --- | --- |
| node-1 | `1` | `100` | `10.0.0.11:8080` | `10.0.0.11:19090` | `127.0.0.1:18080` |
| node-2 | `2` | `100` | `10.0.0.12:8080` | `10.0.0.12:19090` | `127.0.0.1:18080` |
| node-3 | `3` | `100` | `10.0.0.13:8080` | `10.0.0.13:19090` | `127.0.0.1:18080` |

三台机器上的公共 `peer` 映射都写成同一份：

```text
peer-ids         = 1,2,3
peer-http-addrs  = 1=10.0.0.11:8080,2=10.0.0.12:8080,3=10.0.0.13:8080
peer-grpc-addrs  = 1=10.0.0.11:19090,2=10.0.0.12:19090,3=10.0.0.13:19090
peer-admin-addrs = 1=127.0.0.1:18080,2=127.0.0.1:18080,3=127.0.0.1:18080
```

可以这样理解：

- `peer-http-addrs`：告诉每个节点，集群里每个节点的业务 HTTP 地址是什么
- `peer-grpc-addrs`：告诉每个节点，集群里每个节点的内部复制地址是什么
- `peer-admin-addrs`：告诉每个节点，集群里每个节点的 admin 地址是什么

虽然 `admin` 默认是本机回环地址，但这个映射仍然要保持一致，便于控制面状态展示和内部地址簿维护。

## 6. 三节点集群部署命令

下面三条命令分别在三台机器上执行。

### 6.1 node-1

```bash
cd /opt/src/StarMap
chmod +x deploy/install-starmapd.sh

sudo bash deploy/install-starmapd.sh \
  --repo-dir /opt/src/StarMap \
  --node-id 1 \
  --cluster-id 100 \
  --region default \
  --http-addr 10.0.0.11:8080 \
  --admin-addr 127.0.0.1:18080 \
  --grpc-addr 10.0.0.11:19090 \
  --admin-token starmap \
  --peer-ids 1,2,3 \
  --peer-http-addrs 1=10.0.0.11:8080,2=10.0.0.12:8080,3=10.0.0.13:8080 \
  --peer-admin-addrs 1=127.0.0.1:18080,2=127.0.0.1:18080,3=127.0.0.1:18080 \
  --peer-grpc-addrs 1=10.0.0.11:19090,2=10.0.0.12:19090,3=10.0.0.13:19090
```

### 6.2 node-2

```bash
cd /opt/src/StarMap
chmod +x deploy/install-starmapd.sh

sudo bash deploy/install-starmapd.sh \
  --repo-dir /opt/src/StarMap \
  --node-id 2 \
  --cluster-id 100 \
  --region default \
  --http-addr 10.0.0.12:8080 \
  --admin-addr 127.0.0.1:18080 \
  --grpc-addr 10.0.0.12:19090 \
  --admin-token starmap \
  --peer-ids 1,2,3 \
  --peer-http-addrs 1=10.0.0.11:8080,2=10.0.0.12:8080,3=10.0.0.13:8080 \
  --peer-admin-addrs 1=127.0.0.1:18080,2=127.0.0.1:18080,3=127.0.0.1:18080 \
  --peer-grpc-addrs 1=10.0.0.11:19090,2=10.0.0.12:19090,3=10.0.0.13:19090
```

### 6.3 node-3

```bash
cd /opt/src/StarMap
chmod +x deploy/install-starmapd.sh

sudo bash deploy/install-starmapd.sh \
  --repo-dir /opt/src/StarMap \
  --node-id 3 \
  --cluster-id 100 \
  --region default \
  --http-addr 10.0.0.13:8080 \
  --admin-addr 127.0.0.1:18080 \
  --grpc-addr 10.0.0.13:19090 \
  --admin-token starmap \
  --peer-ids 1,2,3 \
  --peer-http-addrs 1=10.0.0.11:8080,2=10.0.0.12:8080,3=10.0.0.13:8080 \
  --peer-admin-addrs 1=127.0.0.1:18080,2=127.0.0.1:18080,3=127.0.0.1:18080 \
  --peer-grpc-addrs 1=10.0.0.11:19090,2=10.0.0.12:19090,3=10.0.0.13:19090
```

## 7. 集群部署完成后怎么验证

每台机器都可以先看服务状态：

```bash
sudo systemctl status starmapd-node-1
sudo systemctl status starmapd-node-2
sudo systemctl status starmapd-node-3
```

然后分别看日志：

```bash
sudo journalctl -u starmapd-node-1 -f
sudo journalctl -u starmapd-node-2 -f
sudo journalctl -u starmapd-node-3 -f
```

最后在任意一台机器本机查看控制面状态：

```bash
curl \
  -H "Authorization: Bearer starmap" \
  http://127.0.0.1:18080/admin/v1/status
```

如果三节点正常，你应该能看到：

- 一个 Leader
- 两个 Follower
- `voters` 里包含 `1,2,3`

## 8. 常见问题

### 8.1 为什么单机可以用 `0.0.0.0`，集群更推荐写真实内网 IP

单机时主要目的是先跑起来，`0.0.0.0:8080` / `0.0.0.0:19090` 更省事。  
集群时每个节点都需要互相访问，因此更推荐直接写真实内网 IP，否则其他节点无法准确连接你。

### 8.2 三台机器的 `peer-*` 为什么要一样

因为每个节点都需要知道“整个集群的地址簿”。  
这不是“只写自己”，而是“每台机器都保存同一份集群成员地址映射”。

### 8.3 `cluster-id` 能不能每台不一样

不能。  
同一个 StarMap 集群里的所有节点必须使用同一个 `cluster-id`。

### 8.4 脚本名字是不是 `install-starmap.sh`

当前仓库里的实际脚本名是：

- [install-starmapd.sh](/E:/PersonalCode/GoProject/StarMap/deploy/install-starmapd.sh)

如果你前面口头上写成了 `install-starmap.sh`，这里按仓库里的真实脚本名执行就可以。
