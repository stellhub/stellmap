# StarMap 腾讯云 CVM 部署说明

本文档面向使用腾讯云 `CVM` 做 `StarMap` 部署测试的场景，重点覆盖：

- 如何理解当前仓库里的部署相关脚本
- 如何在 `Linux VM + systemd` 上部署 `starmapd`
- 如何使用 `TOML` 配置文件管理节点参数
- 如何做最小可用的注册、查询、注销验证
- 如何让 Prometheus 通过 StarMap 的 `HTTP SD` 发现目标
- 如何结合 `journalctl` 观察服务日志

## 1. scripts/protoc-gen.ps1 现在有没有用

结论先说：

- 对当前服务运行、VM 部署、功能测试来说，它没有直接作用
- 对本地 Windows 开发者手工重新生成 `protobuf` 代码来说，它仍然可以用
- 它不是当前仓库“正式构建链路”的唯一入口

原因有两个：

1. 仓库里没有任何地方自动引用 `scripts/protoc-gen.ps1`
2. 仓库根目录已经有 [buf.gen.yaml](E:\PersonalCode\GoProject\StarMap\buf.gen.yaml) 和 [buf.yaml](E:\PersonalCode\GoProject\StarMap\buf.yaml)，说明 `buf generate` 才更像推荐的标准生成方式

所以当前更准确的判断是：

- `scripts/protoc-gen.ps1` 不是运行时必需脚本
- 也不是当前部署测试会用到的脚本
- 它更像一个“历史保留的 Windows 本地开发辅助脚本”

如果你们后续团队统一改成只使用 `buf generate`，这个脚本可以删除；如果还希望兼容 Windows 下手工直接跑 `protoc`，也可以保留。

## 2. 部署方式建议

当前最适合 `CVM` 的部署方式是：

- 直接在 Linux VM 上 `go build`
- 通过 `systemd` 托管 `starmapd`
- 使用 `journalctl` 查看运行日志

仓库里已经新增了部署脚本：

- [install-starmapd.sh](E:\PersonalCode\GoProject\StarMap\scripts\install-starmapd.sh)

这个脚本会做下面几件事：

1. 在目标机器上编译 `./cmd/starmapd`
2. 将二进制安装到 `/opt/starmap/bin/starmapd`
3. 从仓库里的 [starmapd.toml](E:\PersonalCode\GoProject\StarMap\config\starmapd.toml) 模板复制并渲染节点专属 `TOML` 配置文件
4. 生成只携带 `--config` 的节点专属启动脚本
5. 生成对应的 `systemd service`
6. 可选地复制 `replication targets` JSON 文件
7. 自动 `enable` 并启动服务

## 3. 腾讯云资源规划建议

### 3.1 单机 smoke test

如果只是先验证功能链路，建议先买 1 台 `CVM`，先跑单节点。

优点：

- 成本低
- 排查简单
- 可以先验证 API、日志、存储目录、systemd 行为

### 3.2 三节点集群测试

如果你要验证 `Raft` 选主、复制、节点恢复，建议再扩成 3 台 `CVM`。

建议示例：

| 节点 | 内网 IP | HTTP | Admin | gRPC |
| --- | --- | --- | --- | --- |
| node-1 | 10.0.0.11 | 10.0.0.11:8080 | 127.0.0.1:18080 | 10.0.0.11:19090 |
| node-2 | 10.0.0.12 | 10.0.0.12:8080 | 127.0.0.1:18080 | 10.0.0.12:19090 |
| node-3 | 10.0.0.13 | 10.0.0.13:8080 | 127.0.0.1:18080 | 10.0.0.13:19090 |

说明：

- `HTTP`：给服务注册、发现、watch、health、metrics 用
- `Admin`：给成员变更、状态查询、复制状态查询用
- `gRPC`：节点间复制、快照传输用

## 4. 腾讯云安全组建议

最小建议如下：

| 端口 | 作用 | 建议 |
| --- | --- | --- |
| 8080/tcp | 对外 HTTP | 允许你的测试机器访问；多节点时节点间也可互通 |
| 18080/tcp | admin HTTP | 当前实现默认只允许本机回环访问，安全组里通常无需对外放通 |
| 19090/tcp | 节点间 gRPC | 只允许集群节点内网互通 |

额外建议：

- 测试环境和生产环境的隔离仍然优先依赖网络和安全组
- `admin-token` 只是第二道防护，不应替代网络隔离

## 5. VM 上准备运行环境

下面示例以常见的 Ubuntu / Debian 系 Linux 为例。

### 5.1 安装依赖

```bash
sudo apt-get update
sudo apt-get install -y git curl build-essential
```

### 5.2 安装 Go

请安装与当前仓库兼容的 Go 版本，然后确认：

```bash
go version
```

### 5.3 拉取仓库

```bash
sudo mkdir -p /opt/src
sudo chown "$USER":"$USER" /opt/src
cd /opt/src
git clone <your-repo-url> StarMap
cd /opt/src/StarMap
```

## 6. 单节点部署示例

在单台 `CVM` 上执行：

```bash
cd /opt/src/StarMap
chmod +x scripts/install-starmapd.sh

sudo bash scripts/install-starmapd.sh \
  --repo-dir /opt/src/StarMap \
  --node-id 1 \
  --cluster-id 100 \
  --region default \
  --http-addr 0.0.0.0:8080 \
  --admin-addr 127.0.0.1:18080 \
  --grpc-addr 0.0.0.0:19090 \
  --admin-token starmap
```

说明：

- 脚本会生成类似 `/etc/starmapd/starmapd-node-1.toml` 的配置文件
- `systemd` 启动时会使用 `--config=/etc/starmapd/starmapd-node-1.toml`
- `--data-dir` 可以不传，默认是 `/var/lib/starmap/node-1`
- `--admin-token` 可以不传，当前默认值就是 `starmap`
- 单节点时不传 `--peer-ids` 也可以，脚本会默认只使用当前节点

## 6.1 TOML 配置文件示例

安装脚本使用的模板文件可参考：

- [starmapd.toml](E:\PersonalCode\GoProject\StarMap\config\starmapd.toml)

注意这个文件现在是脚本模板，里面带占位符；如果你要手工部署，可以直接按下面这份“真实可用配置”填写：

一个真实可用的最小配置示例如下：

```toml
[node]
id = 1
cluster_id = 100
region = "default"
data_dir = "/var/lib/starmap/node-1"

[server]
http_addr = "0.0.0.0:8080"
admin_addr = "127.0.0.1:18080"
grpc_addr = "0.0.0.0:19090"

[auth]
admin_token = "starmap"
replication_token = "starmap"
prometheus_sd_token = "starmap"

[cluster]
peer_ids = "1"
peer_grpc_addrs = ""
peer_http_addrs = ""
peer_admin_addrs = ""

[runtime]
request_timeout = "5s"
shutdown_timeout = "10s"

[registry]
cleanup_interval = "1s"
cleanup_delete_limit = 128

[replication]
targets_file = ""
```

如果你不想依赖安装脚本，也可以手动创建配置文件，然后这样启动：

```bash
./starmapd --config=/etc/starmapd/starmapd-node-1.toml
```

命令行参数优先级高于配置文件，例如：

```bash
./starmapd --config=/etc/starmapd/starmapd-node-1.toml --http-addr=:28080 --admin-token=new-token
```

## 7. 三节点部署示例

下面示例假设三台腾讯云 `CVM` 的内网地址分别是：

- `10.0.0.11`
- `10.0.0.12`
- `10.0.0.13`

### 7.1 node-1

```bash
cd /opt/src/StarMap
chmod +x scripts/install-starmapd.sh

sudo bash scripts/install-starmapd.sh \
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

### 7.2 node-2

```bash
cd /opt/src/StarMap
chmod +x scripts/install-starmapd.sh

sudo bash scripts/install-starmapd.sh \
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

### 7.3 node-3

```bash
cd /opt/src/StarMap
chmod +x scripts/install-starmapd.sh

sudo bash scripts/install-starmapd.sh \
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

## 8. 部署后检查

### 8.1 查看服务状态

```bash
sudo systemctl status starmapd-node-1
```

### 8.2 跟踪日志

```bash
sudo journalctl -u starmapd-node-1 -f
```

### 8.3 查看健康检查

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/metrics | head
```

### 8.4 查看 admin 状态

当前实现中，`admin` 接口应该通过本机 `127.0.0.1` 访问：

```bash
curl \
  -H "Authorization: Bearer starmap" \
  http://127.0.0.1:18080/admin/v1/status
```

## 9. 基本功能测试

下面给出一组最小操作，完成：

1. 查看当前日志
2. 注册一个实例
3. 查询实例
4. 注销实例
5. 再次查看日志

### 9.1 先盯住日志

在一个终端执行：

```bash
sudo journalctl -u starmapd-node-1 -f
```

当前版本已经补了最小业务日志，注册、心跳、注销成功后会看到类似内容：

```text
registry register accepted namespace=prod service=order-service instance_id=order-1 remote=...
registry heartbeat accepted namespace=prod service=order-service instance_id=order-1 lease_ttl_seconds=30 remote=...
registry deregister accepted namespace=prod service=order-service instance_id=order-1 remote=...
```

### 9.2 注册实例

```bash
curl -X POST http://127.0.0.1:8080/api/v1/registry/register \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "prod",
    "service": "order-service",
    "instanceId": "order-1",
    "zone": "az1",
    "labels": {
      "version": "v1",
      "color": "gray"
    },
    "endpoints": [
      {
        "name": "http",
        "protocol": "http",
        "host": "10.0.0.11",
        "port": 9000
      }
    ]
  }'
```

### 9.3 查询实例

```bash
curl "http://127.0.0.1:8080/api/v1/registry/instances?namespace=prod&service=order-service"
```

如果你想按标签筛选，可以继续加：

```bash
curl "http://127.0.0.1:8080/api/v1/registry/instances?namespace=prod&service=order-service&selector=color=gray"
```

### 9.4 发送一次心跳

```bash
curl -X POST http://127.0.0.1:8080/api/v1/registry/heartbeat \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "prod",
    "service": "order-service",
    "instanceId": "order-1",
    "leaseTtlSeconds": 30
  }'
```

### 9.5 注销实例

```bash
curl -X POST http://127.0.0.1:8080/api/v1/registry/deregister \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "prod",
    "service": "order-service",
    "instanceId": "order-1"
  }'
```

### 9.6 再查一次确认已经注销

```bash
curl "http://127.0.0.1:8080/api/v1/registry/instances?namespace=prod&service=order-service"
```

## 10. 常用运维命令

### 10.1 重启服务

```bash
sudo systemctl restart starmapd-node-1
```

### 10.2 停止服务

```bash
sudo systemctl stop starmapd-node-1
```

### 10.3 查看最近 200 行日志

```bash
sudo journalctl -u starmapd-node-1 -n 200 --no-pager
```

### 10.4 查看复制状态

```bash
curl \
  -H "Authorization: Bearer starmap" \
  http://127.0.0.1:18080/admin/v1/replication/status
```

## 11. Prometheus 与 Grafana 最小接入示例

### 11.1 Prometheus 通过 StarMap 做 HTTP SD

`StarMap` 已经提供：

- `GET /internal/v1/prometheus/sd`

Prometheus 只需要改配置，不需要改源码。

最小 Prometheus 配置示例：

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: starmap-services
    http_sd_configs:
      - url: http://10.0.0.11:8080/internal/v1/prometheus/sd?endpoint=metrics
        refresh_interval: 30s
        authorization:
          type: Bearer
          credentials: starmap
```

如果你希望同时抓取 StarMap 自身节点指标，可以使用：

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: starmap-all
    http_sd_configs:
      - url: http://10.0.0.11:8080/internal/v1/prometheus/sd?endpoint=metrics&includeSelf=true
        refresh_interval: 30s
        authorization:
          type: Bearer
          credentials: starmap
```

### 11.2 docker-compose 最小示例

如果你想快速在测试机上拉一套 `Prometheus + Grafana`，可以先用下面这个最小 `docker-compose.yml`：

```yaml
version: "3.8"

services:
  prometheus:
    image: prom/prometheus:v2.54.1
    container_name: prometheus
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro

  grafana:
    image: grafana/grafana:11.1.0
    container_name: grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_USER=admin
      - GF_SECURITY_ADMIN_PASSWORD=admin
```

对应的 `prometheus.yml` 最小示例：

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: starmap-all
    http_sd_configs:
      - url: http://10.0.0.11:8080/internal/v1/prometheus/sd?endpoint=metrics&includeSelf=true
        refresh_interval: 30s
        authorization:
          type: Bearer
          credentials: starmap
```

Grafana 接入步骤：

1. 打开 `http://<grafana-ip>:3000`
2. 使用 `admin / admin` 登录
3. 添加数据源，类型选 `Prometheus`
4. URL 填写 `http://prometheus:9090`
5. 保存后即可按 `service`、`namespace`、`target_kind` 等标签建图

## 12. 建议的测试顺序

建议你按这个顺序做腾讯云上的第一次验证：

1. 先拉起单节点
2. 用 `curl` 完成注册、查询、心跳、注销
3. 用 `journalctl` 确认日志与 API 动作一致
4. 再让 Prometheus 通过 `HTTP SD` 抓取 StarMap 和业务实例指标
5. 再扩成三节点，验证选主和实例复制
6. 最后再测试节点重启、成员变更和跨 region 同步

这样会比一开始就直接堆三节点更容易排查问题。
