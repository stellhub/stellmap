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