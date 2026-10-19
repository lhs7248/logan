# 容器排空、强杀恢复与 Java/Go 往返演练

本入口只连接新建的隔离 MySQL，创建随机测试库和归属标签资源；不读取生产配置，不操作现有 Java、网关或生产数据。`/readyz` 继续返回 503。Docker 必须显式指定专用 Logan Unix socket，并确认运行环境可丢弃；授权参数不是生产操作许可。

## 1. 活跃请求与任务

从 ServerGo 编译测试专用 Linux 二进制，平台与测试 Engine 一致：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -tags 'stdjson runtimefault' -o /private/tmp/logan-runtimefault.test .
python3 script/verify-runtime-recovery.py \
  --docker /absolute/path/docker \
  --host unix:///private/tmp/logan-runtime/docker.sock \
  --allow-isolated-runtime \
  --binary /private/tmp/logan-runtimefault.test \
  --out /private/tmp/logan-runtime-recovery-unique
```

先在这个独立 Engine 准备官方 `mysql:8.4.11` 和 `python:3.12-alpine`。脚本只使用已存在镜像，不自动选用其他 Engine。运行于内部网络，不发布宿主端口。

测试专用入口复用真实 `RunContext`、Hertz 请求处理、Native/Web worker、生产解析和 MySQL 发布逻辑。慢 HTTP 路由、下载屏障和 Web 解析屏障是测试夹具。Native 使用本地合成 gzip `storage.Store`，不访问真实 OSS。

验收场景：

- 请求进入处理后发送真实 Docker SIGTERM；确认进程仍等待，释放请求后客户端收到完整正文、进程退出 0。
- Native/Web 在持久化领取任务后暂停；SIGTERM 后确认进程仍等待，释放屏障后发布准确内容及一条详情、退出 0。
- Native/Web 在领取任务后、发布前发送真实 SIGKILL；确认 exit137、原租约和零详情。替代容器等待未缩短的生产 30 秒数据库租约到期，发布后 fence/attempts 从 1 增至 2、owner 清空。重复启动不改变详情 ID，也不重复插入。

它不证明发布事务中途、提交确认丢失、活跃旧进程持有过期 fence、真实 OSS 下载中断、维护任务或所有强杀时刻的恢复。旧进程已经被杀，因此不能单凭本场景声称“仍存活的旧发布者被 fence 拒绝”。此前真实 MySQL 测试覆盖的范围另见[集成说明](integration.md)。

## 2. 原 Java → Go → 原 Java

Java 使用现有 `tests/legacy-java/pom.xml` 编译原始源码，原 MyBatis XML 逐字复制；不修改原源码、配置、golden 或指纹。该测试工具链更新了编译插件/Lombok以支持当前 JDK，不是原 Java Dockerfile 的逐位重建。测试运行时为 JRE17；旧 MyBatis 3.5.0 的反射需要测试进程添加 `--add-opens=java.base/java.util=ALL-UNNAMED`，原 JRE8 部署未在本轮复验。

```sh
mvn -f tests/legacy-java/pom.xml -Dtest=CrossRuntimeDatabaseTest test
python3 tests/crossruntime/build-java-bundle.py --output /private/tmp/logan-java-bundle-unique
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -tags 'stdjson crossruntime' -o /private/tmp/logan-crossruntime.test ./tests/crossruntime
python3 script/rehearse-data-rollback.py \
  --docker /absolute/path/docker \
  --host unix:///private/tmp/logan-runtime/docker.sock \
  --allow-isolated-runtime \
  --binary /private/tmp/logan-crossruntime.test \
  --java-bundle /private/tmp/logan-java-bundle-unique \
  --work-dir /private/tmp/logan-data-work-unique \
  --out /private/tmp/logan-data-evidence-unique \
  --with-http --http-go-image your-prebuilt-local-go-image:tag
```

第一步无集成环境变量时有意跳过集成测试，只用于生成编译结果和依赖报告，**不能记为数据库验收通过**。真正验收由后续控制器设置随机库及完整 opt-in 参数，再分别运行 Java seed、Go、Java verify；必须有成功标记、非跳过测试和新生成的三份数据清单。

`--work-dir` 和 Java bundle 必须通过 VM 在相同绝对路径可见。共享数据只有合成日志，容器使用宿主 UID 访问；本脚本不替代生产 UID10001/真实私钥权限验收。证据目录必须新建且位于仓库外。另需准备 `eclipse-temurin:17-jre`；启用 `--with-http` 还需 `tomcat:9-jre17-temurin`、Python 镜像、明确指定的实际 Go 镜像和本机 openssl。所有镜像 ID 会进入命令记录。省略 `--with-http` 只运行数据层，不算 HTTP 验收。

数据层在真实 MySQL 原四表与显式迁移 001/002 上执行：Java 创建 Native/Web 已解析和待解析任务；Go 保留已解析 ID/内容、处理待解析任务，并创建/解析新任务；退出 Go 后由原 Java 服务与 mapper 读取全部数据。Native 对象为本地 gzip 夹具，Web 使用真实解析逻辑。保留原 `web_task/web_detail` 的 utf8 字符集；Web 夹具使用中文等 BMP 内容，Native 的 utf8mb4 内容另含 emoji，不更改原表以放宽用例。

HTTP 阶段将原 webapp/类/资源组装成临时 WAR，仅替换副本中的隔离 DB/OSS 配置；原文件不变。实际 Tomcat → 生产 Go 镜像 → Tomcat 回滚严格串行，每个旧端停止后再启动新端，确认没有 OOM 或强制 SIGKILL。原 Java 所需日志/部署目录使用小型专属 tmpfs，根文件系统保持只读。

对 Java 已解析数据和 Go 新发布数据分别验证 Native/Web 的任务、索引、单详情和批量详情，合计每阶段 16 个请求、三阶段 48 个响应。要求 HTTP200、业务 code200、JSON类型、稳定字段/ID/内容一致，索引必须包含预期详情ID；不只比较三个空结果。Go readiness 必须精确返回503。测试请求只在内部网络直连，不开放网关。显式 OSS 夹具必须存活且收到零对象请求，因而这些读接口验收不依赖云存储。

## 3. 证据与限制

控制器对命令、Docker 信号/退出、SQL 状态、三份数据清单、HTTP 捕获和启停顺序分别留证据；失败也保留诊断。清理只针对匹配本轮归属标签的资源，清理失败返回非零。随机凭据只用于临时库，日志脱敏；env 文件及 HTTP 临时 WAR/RSA 夹具回收。返回0仅证明摘要中覆盖的场景。

本轮不覆盖全部20接口、上传/签名下载、真实OSS/RSA协议上传/SDK全链路、真实网关reload、生产备份还原或完整生产切流；也不证明长期容量和所有故障点的数据一致性。生产切换仍遵循[发布手册](release-runbook.md)，最新实测范围见[进度记录](progress.md)。
