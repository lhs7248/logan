# 配置与运行边界

配置来源仅为环境变量；`.env.example` 是说明文件，程序不会自动加载它。

| 变量 | 默认值 | 校验/含义 |
|---|---|---|
| LOGAN_ENV | development | development / production |
| LOGAN_HTTP_ADDRESS | :8080 | host:port，端口1～65535 |
| LOGAN_CONTEXT_PATH | /logan-web | 旧WAR上下文；设为 / 表示无上下文 |
| LOGAN_TIMEZONE | Asia/Shanghai | 有效IANA时区；生产应核对旧JVM时区 |
| LOGAN_HTTP_READ_TIMEOUT | 30s | 正数 |
| LOGAN_HTTP_WRITE_TIMEOUT | 30s | 正数 |
| LOGAN_HTTP_IDLE_TIMEOUT | 1m | 正数 |
| LOGAN_HTTP_MAX_BODY_BYTES | 33554432 | 正数；32MiB仅为骨架实验值，非历史文件兼容承诺 |
| LOGAN_EXIT_WAIT | 60s | HTTP优雅退出预算 |
| LOGAN_DB_ENABLED | false | 生产模式必须true |
| LOGAN_MYSQL_DSN | 无 | 启用DB时必填，要求user/database |
| LOGAN_DB_MAX_OPEN | 20 | 正整数，各副本共用总预算 |
| LOGAN_DB_MAX_IDLE | 10 | 0～max_open |
| LOGAN_DB_CONN_MAX_LIFETIME | 30m | 正数 |
| LOGAN_DB_PING_TIMEOUT | 5s | 正数 |

DSN会强制 `parseTime=true` 并按配置时区解析；未显式指定时设置连接5s、读10s、写10s超时。
这不修改MySQL会话时区，旧JVM/MySQL/Go的TIMESTAMP语义仍需真实数据集成验证。
禁止 `AutoMigrate`；四表映射显式声明列名和数据库维护的时间列，不嵌入 gorm.Model。
数据库错误仅输出经过处理的概要，避免驱动错误包含凭据。连接池保持到Hertz排空结束后再关闭。

Hertz暂用标准网络库。Netpoll性能比较、实际日志大小预算、后台worker、Prometheus、访问日志与readiness就绪逻辑在后续工作包实现。
当前 `/readyz` 永不成功，即使已连接MySQL。

JSON 编解码构建标签为 `stdjson`；所有 Makefile/Docker/构建脚本保持一致。兼容响应写出 Content-Type 为 Java Controller 实测的 `application/json`，不擅自增加 charset。

## 移动端与 OSS

设置 `LOGAN_NATIVE_ENABLED=true` 启用三个移动端接口；同时要求 `LOGAN_DB_ENABLED=true`。启动时检查必填项，不创建或迁移表。

| 变量 | 默认值 | 含义 |
|---|---|---|
| LOGAN_NATIVE_AES_KEY / LOGAN_NATIVE_AES_IV | 无 | 与旧服务一致的原始 UTF-8 字节，各16字节，不是十六进制文本 |
| LOGAN_NATIVE_TEMP_DIR | 系统临时目录 | 必须已存在；使用0600私有文件并及时清理 |
| LOGAN_NATIVE_CONCURRENCY | 4 | 每实例同时处理上传数，等待也计入超时 |
| LOGAN_NATIVE_MAX_FRAME_BYTES | min(请求上限,32MiB) | 单帧密文上限，至少16字节且不超过请求上限 |
| LOGAN_NATIVE_TIMEOUT | 30s | 接收、解密、排队、OSS和入库的整体预算 |
| LOGAN_OSS_ENDPOINT | 无 | 完整http(s)端点URL，无凭据、查询串或路径 |
| LOGAN_OSS_REGION / LOGAN_OSS_BUCKET | 无 | 签名区域及桶，必填 |
| LOGAN_OSS_PREFIX | logan-logs/ | 与旧服务保持一致，直接拼接文件名 |
| LOGAN_OSS_ACCESS_KEY_ID / LOGAN_OSS_ACCESS_KEY_SECRET | 无 | 必填，通过密钥管理注入 |
| LOGAN_OSS_SESSION_TOKEN | 无 | 可选STS token |
| LOGAN_OSS_TIMEOUT | 30s | OSS单次操作预算，仍受外层请求预算约束 |
| LOGAN_OSS_PATH_STYLE | false | 测试端点可设true，生产按端点要求配置 |

`LOGAN_HTTP_MAX_BODY_BYTES` 同时限制移动端输入；32MiB是当前实验预算，必须按历史日志分布调整。开启流式读取后最多预读64KiB，之后由协议层逐段计量，包括未知长度的chunked输入。读取超时取HTTP读取预算与移动端预算的较小值；响应写出仍受HTTP写超时约束。生产应按实际上传和依赖延迟统一设置各预算。

## Web协议和任务

| 变量 | 默认值 | 含义 |
|---|---|---|
| LOGAN_WEB_ENABLED | false | 启用10个Web接口，要求DB与显式迁移 |
| LOGAN_WEB_RSA_PRIVATE_KEY_FILE | 无 | 必填，挂载与客户端匹配的RSA私钥；最大64KiB |
| LOGAN_WEB_WORKERS | 4 | 每实例解析并发；索引等待者上限为其8倍 |
| LOGAN_WEB_MAX_TASKS | 200 | 每次查询任务/详情ID、类型列表数量预算 |
| LOGAN_WEB_TIMEOUT | 30s | 请求和每次后台解析的执行预算 |
| LOGAN_WEB_WAIT | 5s | 首次索引等待预算，超时后任务仍可后台执行 |

Web启用时同样使用流式请求读取；HTTP读取时间取已启用协议预算与HTTP预算的最小值。所有Web处理还受统一请求预算约束。后台worker在HTTP排空后取消/等待，再关闭数据库。详细资源预算、显式迁移、原Java覆盖行为与旧RSA运行时兼容见 [Web说明](web.md)。readiness仍503。

## 移动端查询与解析

通过 `LOGAN_NATIVE_QUERY_ENABLED=true` 单独启用7个剩余查询映射，要求既有移动端OSS配置及显式内部任务表迁移。新增变量、实验资源预算、同步等待、后台取消与旧错误边界详见 [移动端查询说明](native-queries.md)。不自动创建表；HTTP排空后取消并等待native/Web worker，再关闭OSS连接与数据库。所有20个映射实现后readiness依然503，未自动开放生产流量。

## 维护与指标

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| LOGAN_MAINTENANCE_ENABLED | false | 开启持久化保留期和对象回收，要求 Native DB/OSS 与迁移 002 |
| LOGAN_RETENTION | 168h | 按 add_time 判断的日志保留期 |
| LOGAN_MAINTENANCE_INTERVAL | 1m | 有界扫描间隔，启动立即执行 |
| LOGAN_MAINTENANCE_TIMEOUT | 30s | 单轮预算 |
| LOGAN_MAINTENANCE_BATCH | 100 | 每阶段数量上限，范围 1–1000 |
| LOGAN_ORPHAN_AGE | 24h | 上传登记和已结束临时会话的宽限期 |
| LOGAN_METRICS_ENABLED | false | 开启 /metrics |

租约和对象删除租约默认 2 分钟，失败重试延迟 1 分钟，最多 12 次。完整操作约束和历史孤儿对象边界见 [运维说明](operations.md)。
