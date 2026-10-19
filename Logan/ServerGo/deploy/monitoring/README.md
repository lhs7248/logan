# Logan Go monitoring candidate

This optional overlay scrapes the Go candidate through the Compose `logan-go-ops` network at `logan-go:8080/metrics`. It neither switches production traffic nor provisions external notifications. The candidate must enable metrics. Never publish `/metrics` through an ingress or public reverse proxy. The shared bridge permits the Go service to reach its configured external DB/OSS; the network is not an isolation boundary against other attached containers.

From the ServerGo directory, after preparing the candidate environment as described in the deployment README, review the merged configuration:

```sh
docker compose -f deploy/compose.go.yaml -f deploy/monitoring/compose.monitoring.yaml --profile monitoring config --quiet
```

An authorized operator can start the candidate stack using the same arguments and `up -d --build`. Adding this overlay without `--profile monitoring` does not start monitoring services. Keep the same first file and project name on subsequent commands; relative mounts resolve against `deploy/`. Do not use this overlay alone.

Prometheus is available only at [localhost:19090](http://127.0.0.1:19090) and Alertmanager at [localhost:19093](http://127.0.0.1:19093). They have no configured authentication; retain loopback binding and use a private authenticated access path if remote access is required. Check Prometheus Targets and Rules, then query `up{job="logan-go"}`. A target value of one proves scraping, not business readiness. `/readyz` may deliberately remain 503 until deployment gates are satisfied.

## Alerts and interpretation

All rules are warning candidates, with `service` and bounded `reason` labels. Thresholds are starting points for evaluation, not measured capacity or SLO claims.

| Alert | Condition held continuously | Meaning / limit |
| --- | --- | --- |
| ScrapeDown | Scrape fails for 2 minutes | Network, server, disabled metrics, or scrape failure; inspect target error. |
| MetricsAbsent | No configured `up` series for 5 minutes | Target removed or monitoring configuration broken; no instance label can be inferred. |
| InstrumentationAbsent | Successful scrape lacks `go_goroutines` for 5 minutes | Detects a wrong/empty endpoint; optional feature series are not required. |
| HTTP5xxRatio | >5% 5xx over 5 minutes, with >=100 requests per 5 minutes, for 5 minutes | Excludes `/livez`, `/readyz`, `/ping`, `/metrics` from numerator and denominator. |
| HTTPP99High | Estimated p99 >2.5s and >=100 observations per 5 minutes, for 10 minutes | Same route exclusions; aggregates across business routes. |
| DBPoolSaturated | Finite pool >90% in use for 10 minutes | `max_open=0` means unlimited and is excluded. |
| ProcessingFailed | Failed persistent processing count >0 for 5 minutes | Includes each kind; operator must diagnose/retry through supported procedures. |
| ProcessingBacklog | Pending count >100 for 15 minutes | Includes retry_wait; count only, no age signal or proven worker capacity. |
| MaintenanceFailed | Failed maintenance snapshot >0 for 5 minutes | Snapshot count, not maintenance cycle success. |
| MaintenanceCycleErrors | Positive 5-minute error/failed increase for 5 minutes | Persistent/repeated cycle errors; one isolated event may clear before firing. |
| OSSErrors | Positive 5-minute error increase per operation for 5 minutes | Cancellations excluded. Get measures headers/body-handle return; body-read failures are not observed. |

HTTP latency uses classic default Prometheus histogram buckets; the largest finite bucket is 10s. Quantiles are bucket estimates and cannot measure tail magnitude above that boundary. There is no last-success timestamp or backlog age metric, so no freshness/oldest-job-age alert is claimed. When optional workers or OSS are disabled, their absent metric series do not fire these alerts. An all-zero/no-traffic HTTP window does not fire a ratio/latency warning. Alert `for` resets when its expression is absent or false.

## Notification handling

Alertmanager's `blackhole` receiver deliberately has no integrations. Prometheus forwards alerts to its local UI, but no email, chat, pager, or webhook notification is delivered. Before enabling any receiver, an authorized operator must choose and verify the destination, provide secrets outside version control, validate routing with the destination owner, and explicitly authorize a controlled delivery test. No external receiver address is supplied by this candidate.

## Offline configuration checks

Use matching `promtool` 3.15.0 and `amtool` 0.34.1 from the [official Prometheus download page](https://prometheus.io/download/). Versions are fixed in Compose, verified against that primary source on 2026-10-16; upgrades require config and rule revalidation. The official [installation documentation](https://prometheus.io/docs/prometheus/latest/installation/) documents Docker image distribution.

From this directory:

```sh
promtool check config --syntax-only prometheus.yml
promtool check rules alerts.yml
promtool test rules alerts.test.yml
amtool check-config alertmanager.yml
```

`--syntax-only` avoids host resolution of the container's `/etc/prometheus/alerts.yml`; check rules independently as above. In the built container, `promtool check config /etc/prometheus/prometheus.yml` also validates mounted rule-file discovery. The fixtures cover every alert firing, pending time, recovery, readiness-only 503s, low traffic, zero backlog, unlimited DB pools, zero error counters, and absent optional feature metrics. Synthetic evaluation does not prove live scrape connectivity, volume permissions, real dependency behavior, or notification delivery.

Prometheus data has 7-day / 1GB retention bounds; size is a retention target, not a hard filesystem quota. Named volumes preserve monitoring state across recreation. Do not remove volumes while retaining incident evidence. No dashboard, exporter, or external notification service is implied.


## 采集链路本机冒烟（不使用容器）

ServerGo 的 `script/smoke-monitoring.py` 可启动自有的三个本机进程验证链路。必须提供已构建的 Go 可执行文件和可信的官方 Prometheus/Alertmanager 二进制，并显式传入 `--allow-local-processes`。它会清除继承的 LOGAN 配置、关闭所有业务/DB/OSS/维护开关、选择回环端口，使用临时目录、固定 blackhole 接收器与合成 `LoganLocalSmoke` 告警。配置不会读取操作员修改后的远程 receiver/remote_write；原告警规则仍由独立的 promtool fixtures 验证。

```sh
python3 script/smoke-monitoring.py --allow-local-processes --server /path/to/local/logan-server --prometheus /path/to/prometheus --alertmanager /path/to/alertmanager
```

该命令从 ServerGo 目录执行。验证真实本机采集、HTTP 指标和合成告警进入本机 Alertmanager，不发送外部通知，不连接真实 DB/OSS，不改变网关或 Java。结束、SIGINT 和 SIGTERM 都会回收自建进程；驱动进程遭 SIGKILL 时无法保证执行清理。结果不等于 Docker 网络、文件挂载或生产通知已验收。

当前 HTTP 指标只标记 HTTP 状态；旧接口 HTTP 200 内的业务错误码不会被 HTTP5xx 规则捕获。没有容器 cgroup/磁盘剩余量、DB 可达性、队列年龄与维护最后成功时间指标，对应的基础设施监控和业务合成探测仍需上线前补齐，不能伪造这些指标的规则。
