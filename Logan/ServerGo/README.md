# Logan ServerGo

Hertz + hz + GORM 重写：兼容契约、项目骨架及移动端/Web协议、全部20个旧接口及任务解析链路。

20 个旧业务接口均已登记。默认关闭移动端功能时全部返回 **HTTP 501 / code 501**；配置 `LOGAN_NATIVE_ENABLED=true` 并提供数据库、协议密钥和 OSS 配置后，启用上传、任务下载地址和签名重定向 3 个接口，再配置 `LOGAN_WEB_ENABLED=true`、Web私钥和显式数据库迁移后启用全部10个Web接口；再设置 `LOGAN_NATIVE_QUERY_ENABLED=true` 并校验内部任务表后，接通剩余7个移动端查询/解析接口，全部20个旧映射都有实现。
`/readyz` 始终返回 503，防止被误接入正式流量。`/livez`、`/ping` 返回 200。
Java Server、前端和部署配置保持原样。

隔离镜像构建、容器验收与切换回滚拓扑演练见[容器验收说明](docs/container-validation.md)，实测与环境限制见[进度记录](docs/progress.md)。

Go三类MySQL并发锁修复、Web上传与有效一小时零错误复验见[锁冲突修复报告](docs/go-mysql-lock-recovery.md)。保留租约、fence及幂等校验；只测Go，未操作生产或切流。

活跃请求/任务排空、SIGKILL恢复及真实 Java/Go 数据和 HTTP 往返见[故障与兼容演练](docs/recovery-rehearsal.md)。

默认构建使用 Hertz 官方 `stdjson` 标签，固定使用标准 JSON 编解码，避免本机 Go 1.27 超出 Sonic 当前支持范围后的隐式回退。

## 持久化排查工作台

本地 Go/Web 工作台的固定启动、私有配置及重启保留见 [操作说明](script/workbench-local-README.md)；真实 Android → Go → Web 的验收及边界见 [真机记录](../../docs/product/2026-10-21-logan-workbench/physical-android-acceptance-2026-10-23.md)。

## 启动与验证

本机验证工具链：Go 1.27.0、hz 0.9.7；Hertz 0.10.6、GORM 1.31.2、GORM MySQL driver 1.6.0。

```sh
cd /Users/lhs/Desktop/workspace/APM/logan-env/Logan/Logan/ServerGo
go mod download
go run -tags stdjson .
```

默认监听 `:8080`，业务路由上下文 `/logan-web`，与现有 nginx 转发一致。
如端口被 Java 占用：

```sh
LOGAN_HTTP_ADDRESS=127.0.0.1:18080 go run -tags stdjson .
curl -i http://127.0.0.1:18080/livez
curl -i http://127.0.0.1:18080/readyz
curl -i http://127.0.0.1:18080/logan-web/logan/latest.json
```

开发模式默认不连接数据库。启用连接时，通过环境变量设置 `LOGAN_DB_ENABLED=true` 和 `LOGAN_MYSQL_DSN`，不要将真实 DSN 写入命令历史、文档或 Git。生产模式要求启用数据库。启动连接失败会退出，不执行任何自动迁移。

```sh
make test
make race
make vet
make build
```

`make test` 会检查原 Java 路由和源码指纹，因此源码目录应和 ServerGo 一起保留。完整说明见 [兼容基线](docs/compatibility.md)、[配置](docs/configuration.md)、[阶段进度](docs/progress.md)。

## Java 基线

无需修改 Java 工程，使用独立的 Controller 测试工程：

```sh
mvn -B -f tests/legacy-java/pom.xml test
```

需要 JDK 17 和 Maven 3.9。测试直接编译原 Java 源码，使用 Spring MockMvc/Jackson 执行控制器；service 是隔离替身，不连接 MySQL/OSS。输出在忽略提交的 `tests/contract/captures/java-controller/`。
另有原 Java 协议执行的 12 个合成向量、9 个元数据样本及 9 个含过滤器的 HTTP 样本。Go 测试直接对照这些证据；它们不代替真实 SDK 日志和真实 MySQL/OSS 联调。

有可运行的隔离 Java 部署后，可采集完整 HTTP 证据：

```sh
go run -tags stdjson ./cmd/contract --mode capture \
  --base-url http://127.0.0.1:8080/logan-web \
  --suite safe --out tests/contract/captures/java-http
```

后续 Go 业务完成后，对照同一数据快照验证：

```sh
go run -tags stdjson ./cmd/contract --mode verify \
  --base-url http://127.0.0.1:18080/logan-web \
  --suite safe --reference tests/contract/captures/java-http
```

骨架当前会在验证时失败，这是业务尚未迁移的真实状态。不能把 501 采集为兼容成功基线。

## 脚手架来源

初始工程由官方 `hz new -module logan/servergo` 生成，版本写在 `.hz`。
`router_gen.go`、`biz/router/register.go` 为生成入口；`router.go`、`bootstrap.go` 和 `biz/router/legacy.go` 为手动维护。
本阶段未引入 IDL，不要再次运行 `hz new` 覆盖工程；引入 IDL 后再审查生成更新。

构建脚本输出 `output/bin/logan-server` 与启动脚本。Dockerfile 已提供，隔离 arm64 镜像构建运行已验证，范围见 docs/container-validation.md。

移动端链路与故障边界见 [移动端实现说明](docs/native.md)。OSS 使用阿里云官方 Go SDK V2 1.6.0，真实凭据仅由环境注入。

Web链路、迁移、历史密钥兼容和故障保护差异见 [Web实现说明](docs/web.md)。

移动端查询启用、任务发布、协议兼容边界和验证证据见 [移动端查询说明](docs/native-queries.md)。全部映射迁移完成后readiness仍保持503，等待真实依赖与生产验收。

保留期清理、OSS/临时目录回收、Prometheus 指标和退出流程见 [运维说明](docs/operations.md)。维护需显式执行迁移 002 并开启，默认不执行删除。

真实依赖测试与多进程强杀恢复见 [集成验证](docs/integration.md)，压测入口与已测边界见 [容量说明](docs/capacity.md)，最新 [Go并发与一小时实测](docs/capacity-soak.md) 已完成有效时长，但因10次Web上传业务失败及解析重试，稳定性门槛未通过。缺环境明确报告 blocked，不以跳过或仅编译代替通过。

独立 Go 候选与固定镜像发布模板见 [部署说明](deploy/README.md)，告警规则见 [监控说明](deploy/monitoring/README.md)，维护窗口切换与单一写入者回滚见 [发布手册](docs/release-runbook.md)。这些文件不替换原 Java 部署、不自动迁移或切流。

全20接口、真实SDK产物及OSS验收结果见 [端到端验收](docs/e2e-acceptance.md)。

移动端日志丢失修复、历史数据边界与Java差异见 [SDK恢复说明](docs/native-sdk-recovery.md)。

后续实际重建镜像、Go20接口及历史对象容器复验结果见 [容器SDK验收](docs/container-sdk-validation.md)。

已完成但不完整Native任务的默认dry-run、追加补数与受控回滚见 [补数工具](docs/native-reparse.md)。独立入口为 `go build -tags stdjson ./cmd/logan-reparse`，当前只接受隔离库。

补数/回滚提交确认丢失和真实Java/Go读取兼容见 [故障与读取验收](docs/reparse-confirmation.md)。

MySQL强杀重启、连接中断下补数/回滚恢复的矩阵、实测证据与边界见 [数据库故障恢复验收](docs/reparse-database-recovery.md)。

MySQL证书校验、加密连接故障与真实GTID副本提升验证见 [TLS与主从提升验收](docs/reparse-tls-failover.md)。未授权的旧计划/审计跨UUID仍安全拒绝；显式源端见证、预检和单操作授权后的旧操作恢复及回滚见 [受控身份迁移](docs/reparse-identity-migration.md)，不提供自动fencing或无见证追认。

重建镜像后，在身份授权、补数和回滚三个状态检查全20接口与真实SDK的入口和证据边界见 [身份迁移后完整回归](docs/identity-full-regression.md)。

Go全20接口混合并发和分档大日志的工具、实测与失败边界见 [大日志压测验收](docs/all20-large-load.md)：小日志混合及1MiB档正式通过，较大档发现MySQL 1GiB OOM，后续诊断受宿主磁盘限制，整轮稳定性尚未通过。

大日志OOM的字符集/连接保留对照、显式预算及磁盘阻断状态见[MySQL预算定位](docs/large-mysql-budget.md)。磁盘恢复后8/4池三档正式已执行、均未OOM，前两档通过；最大档首轮10次、复核22次brief等待失败，完整性复核通过，稳定性验收仍失败。

阶段 23：32 MiB Native 延迟修复、默认等待预算调整、上传至全部明细核验 P95/P99 及 Go-only 三档正式回归结果见 [大日志延迟报告](docs/native-large-latency.md)。三档本轮通过，历史失败及对照局限仍保留。

实际 iOS 模拟器写入、上传至 Go 并经原 LoganSite 查询和下载的验证结果见 [iOS 整体链路验收](docs/ios-simulator-e2e.md)。

iOS 连续上传及1/8/32MiB实测见 [连续与大日志报告](docs/ios-continuous-large-e2e.md)：连续和1/8MiB通过，32MiB三次因SDK末尾零长度帧头被拒绝，整轮部分通过。

上述32MiB空帧尾已在共享CLogan生成端修复，Go严格校验不变；修复后iOS模拟器三份32MiB及连续/小档回归通过，见 [修复及完整性报告](docs/ios-empty-frame-fix.md)。需要重新编译并更新移动端SDK，历史坏文件仍被拒绝。
