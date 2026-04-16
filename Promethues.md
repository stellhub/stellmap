## Prometheus 与 Grafana 最小接入示例

### 1 Prometheus 通过 StarMap 做 HTTP SD

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

### 2 docker-compose 最小示例

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

### 3 Grafana 面板与新增指标说明

当前 `StarMap` 暴露的指标可以分成 4 组：

- 基础运行时指标：`go_*`、`process_*`
- 传输层指标：`starmap_http_server_*`、`starmap_grpc_server_*`
- 复制状态指标：`starmap_replication_*`
- 注册中心画像与治理指标：`starmap_registry_*`

#### 3.1 新增的注册中心画像与治理指标

本次新增了以下指标：

- `starmap_registry_active_instances`
  - 当前仍处于有效租约内的实例数
  - 标签：
    - `namespace`
    - `service`
    - `organization`
    - `business_domain`
    - `capability_domain`
    - `application`
    - `role`
    - `zone`
- `starmap_registry_register_requests_total`
  - 注册请求总量
  - 标签：
    - `namespace`
    - `service`
    - `organization`
    - `business_domain`
    - `capability_domain`
    - `application`
    - `role`
    - `zone`
    - `code`
- `starmap_registry_heartbeat_requests_total`
  - 心跳请求总量
  - 标签同上
- `starmap_registry_deregister_requests_total`
  - 注销请求总量
  - 标签同上
- `starmap_registry_watch_sessions`
  - 当前处于活跃状态的 watch 会话数
  - 标签：
    - `watch_kind`
      - `instances`：业务客户端对注册中心实例目录的 watch
      - `replication`：内部跨 region 复制 watch
    - `caller_namespace`
    - `caller_service`
    - `caller_organization`
    - `caller_business_domain`
    - `caller_capability_domain`
    - `caller_application`
    - `caller_role`
    - `target_namespace`
    - `target_service`
    - `target_scope`
      - `service`
      - `service_set`
      - `service_prefix`
      - `namespace`

其中：

- `active_instances` 不是走请求热路径，而是在 `Prometheus scrape` 时扫描本地注册中心目录并聚合，因此不会放大注册/心跳写路径开销
- `watch_sessions` 是会话级 `gauge`，只在连接建立和断开时增减，不会在每条 watch event 上打点

#### 3.2 调用方身份透传约定

如果你希望做“谁 watch 了最多的下游服务”这类治理面板，业务客户端在调用：

- `GET /api/v1/registry/watch`

时应尽量透传调用方身份。

支持两种方式：

1. URL Query 参数

```text
callerNamespace
callerService
callerOrganization
callerBusinessDomain
callerCapabilityDomain
callerApplication
callerRole
```

2. HTTP Header

```text
X-StarMap-Caller-Namespace
X-StarMap-Caller-Service
X-StarMap-Caller-Organization
X-StarMap-Caller-Business-Domain
X-StarMap-Caller-Capability-Domain
X-StarMap-Caller-Application
X-StarMap-Caller-Role
```

兼容的简化 header 也支持：

```text
X-Caller-Namespace
X-Caller-Service
X-Caller-Organization
X-Caller-Business-Domain
X-Caller-Capability-Domain
X-Caller-Application
X-Caller-Role
```

如果调用方没有透传这些字段，对应 watch 指标会落到 `unknown`。

#### 3.3 推荐 Grafana 面板

下面给出一套可以直接使用的面板 PromQL。

##### A. 总览面板

- HTTP 总 QPS

```promql
sum(rate(starmap_http_server_requests_total{route!~"/metrics|/healthz|/readyz"}[5m]))
```

- HTTP 5xx 错误率

```promql
sum(rate(starmap_http_server_requests_total{route!~"/metrics|/healthz|/readyz", code=~"5.."}[5m]))
/
sum(rate(starmap_http_server_requests_total{route!~"/metrics|/healthz|/readyz"}[5m]))
```

- gRPC 总 QPS

```promql
sum(rate(starmap_grpc_server_requests_total[5m]))
```

- gRPC 非 OK 错误率

```promql
sum(rate(starmap_grpc_server_requests_total{code!="OK"}[5m]))
/
sum(rate(starmap_grpc_server_requests_total[5m]))
```

- 当前 HTTP inflight

```promql
sum(starmap_http_server_inflight_requests)
```

- 当前 gRPC inflight

```promql
sum(starmap_grpc_server_inflight_requests)
```

##### B. HTTP 注册中心面板

- 各 route QPS

```promql
sum by (route, method) (
  rate(starmap_http_server_requests_total{route!~"/metrics|/healthz|/readyz|/api/v1/registry/watch|/internal/v1/replication/watch"}[5m])
)
```

- 普通 HTTP p95 延迟

```promql
histogram_quantile(
  0.95,
  sum by (le, route, method) (
    rate(starmap_http_server_request_duration_seconds_bucket{route!~"/metrics|/healthz|/readyz|/api/v1/registry/watch|/internal/v1/replication/watch"}[5m])
  )
)
```

- 当前 watch 连接数

```promql
sum by (route) (
  starmap_http_server_inflight_requests{route=~"/api/v1/registry/watch|/internal/v1/replication/watch"}
)
```

##### C. gRPC 内部复制面板

- 按 method 的 QPS

```promql
sum by (method, rpc_type) (
  rate(starmap_grpc_server_requests_total[5m])
)
```

- gRPC p95 延迟

```promql
histogram_quantile(
  0.95,
  sum by (le, method, rpc_type) (
    rate(starmap_grpc_server_request_duration_seconds_bucket[5m])
  )
)
```

- `RaftTransport/Send` 平均 batch message 数

```promql
sum(rate(starmap_grpc_server_raft_batch_messages_sum{method="/starmap.v1.RaftTransport/Send"}[5m]))
/
sum(rate(starmap_grpc_server_raft_batch_messages_count{method="/starmap.v1.RaftTransport/Send"}[5m]))
```

- `RaftTransport/Send` 平均 batch bytes

```promql
sum(rate(starmap_grpc_server_raft_batch_payload_bytes_sum{method="/starmap.v1.RaftTransport/Send"}[5m]))
/
sum(rate(starmap_grpc_server_raft_batch_payload_bytes_count{method="/starmap.v1.RaftTransport/Send"}[5m]))
```

- Snapshot 接收吞吐

```promql
sum(rate(starmap_grpc_server_snapshot_bytes_sum{method="/starmap.v1.SnapshotService/Install", direction="recv"}[5m]))
```

- Snapshot 发送吞吐

```promql
sum(rate(starmap_grpc_server_snapshot_bytes_sum{method="/starmap.v1.SnapshotService/Download", direction="send"}[5m]))
```

##### D. 复制状态面板

- 当前连接状态

```promql
starmap_replication_connected
```

- 最近同步距今秒数

```promql
time() - starmap_replication_last_sync_unixtime
```

- 错误增长速率

```promql
rate(starmap_replication_error_total[5m])
```

##### E. 客户端画像面板

- 当前注册实例 Top 10 应用

```promql
topk(
  10,
  sum by (organization, business_domain, capability_domain, application, role) (
    starmap_registry_active_instances
  )
)
```

- 当前注册实例 Top 10 服务

```promql
topk(
  10,
  sum by (namespace, service) (
    starmap_registry_active_instances
  )
)
```

- 最近 5 分钟注册请求 Top 10 应用

```promql
topk(
  10,
  sum by (instance, organization, business_domain, capability_domain, application, role) (
    rate(starmap_registry_register_requests_total[5m])
  )
)
```

- 最近 5 分钟心跳请求 Top 10 应用

```promql
topk(
  10,
  sum by (instance, organization, business_domain, capability_domain, application, role) (
    rate(starmap_registry_heartbeat_requests_total[5m])
  )
)
```

##### F. Watch 治理面板

建议所有治理面板都加过滤：

```promql
{watch_kind="instances"}
```

这样可以把内部 `replication watch` 排除掉，避免干扰业务客户端画像。

- 当前被 watch 最多的下游服务 Top 10

```promql
topk(
  10,
  sum by (target_namespace, target_service) (
    starmap_registry_watch_sessions{watch_kind="instances"}
  )
)
```

- 当前 watch 最多下游服务的调用方 Top 10

```promql
topk(
  10,
  sum by (caller_organization, caller_business_domain, caller_capability_domain, caller_application, caller_role) (
    starmap_registry_watch_sessions{watch_kind="instances"}
  )
)
```

- 当前 namespace 级大范围 watch 数量

```promql
sum(
  starmap_registry_watch_sessions{watch_kind="instances", target_scope="namespace"}
)
```

- 当前 namespace 级大范围 watch Top 10 调用方

```promql
topk(
  10,
  sum by (caller_organization, caller_business_domain, caller_capability_domain, caller_application, caller_role) (
    starmap_registry_watch_sessions{watch_kind="instances", target_scope="namespace"}
  )
)
```

- 当前 watch 最多“不同下游目标”的调用方 Top 10

```promql
topk(
  10,
  sum by (caller_organization, caller_business_domain, caller_capability_domain, caller_application, caller_role) (
    max by (
      caller_organization,
      caller_business_domain,
      caller_capability_domain,
      caller_application,
      caller_role,
      target_namespace,
      target_service,
      target_scope
    ) (
      starmap_registry_watch_sessions{watch_kind="instances"} > 0
    )
  )
)
```

#### 3.4 最小推荐面板清单

如果你希望先做一版最小但足够有用的 Grafana 看板，建议先上这 12 个面板：

1. HTTP 总 QPS
2. HTTP 错误率
3. HTTP p95
4. watch 连接数
5. gRPC 总 QPS
6. gRPC 错误率
7. gRPC p95
8. snapshot 吞吐
9. replication sync age
10. 当前注册实例 Top 10 应用
11. 当前被 watch 最多的下游服务 Top 10
12. 当前 namespace 级 watch Top 10 调用方
