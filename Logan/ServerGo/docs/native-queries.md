# 移动端查询与按需解析

## 本阶段范围

补齐原Java的7个GET映射，路径和默认部署上下文保持不变：

| 路径（默认前缀 `/logan-web`） | 行为 |
|---|---|
| `/logan/latest.json` | 数据库先按ID倒序取20条，再按logDate/ID倒序返回 |
| `/logan/task/search.json` | deviceId、add_time闭区间、可选平台过滤；结束时间加一天 |
| `/logan/task/{taskId}/info.json` | 64位任务ID；不存在时success null |
| `/logan/meta/logtypes.json` | first/second元组，类型1/2/3及原中文名称 |
| `/logan/task/{taskId}/brief.json` | 首次查询触发持久化解析，明细按ID排序，每页20条 |
| `/logan/task/query/details.json` | 保留请求ID顺序、缺失位置null和重复ID的原OrderUtil规则 |
| `/logan/task/{detailId}/detail.json` | 返回原始内容及formatContent，缺失时success null |

加上既有移动端上传/下载3个和Web10个映射，全部20个旧接口都有实现。功能仍按配置启用；这不是生产切流验收。`/readyz`继续503，原因改为集成与生产验证未完成。

## 启用和配置

先按既有流程配置 `LOGAN_NATIVE_ENABLED=true`、MySQL、OSS和移动端AES参数。查询与解析单独用 `LOGAN_NATIVE_QUERY_ENABLED=true` 启用，避免旧上传配置在未部署任务表时改变启动行为。关闭时7个新映射仍501，原3个移动端接口保持可用。

显式审查并执行已有 [001_web_processing.sql](../migrations/001_web_processing.sql)。同一内部任务表支持kind=web/native，无需第二份建表脚本。启动只校验表/列及logan_task、logan_log_detail、logan_processing_job的InnoDB引擎，不执行AutoMigrate或其他迁移。

| 变量 | 默认值 | 含义 |
|---|---|---|
| LOGAN_NATIVE_QUERY_ENABLED | false | 要求LOGAN_NATIVE_ENABLED=true |
| LOGAN_NATIVE_QUERY_WORKERS | 4 | 每实例后台解析并发，1～64 |
| LOGAN_NATIVE_QUERY_MAX_IDS | 200 | 单次详情ID或摘要类型列表数量预算 |
| LOGAN_NATIVE_QUERY_MAX_PLAIN_BYTES | 134217728 | 单任务解压明文及单次详情正文预算，128MiB |
| LOGAN_NATIVE_QUERY_MAX_STAGED_BYTES | 268435456 | NDJSON明细暂存总量，256MiB |
| LOGAN_NATIVE_QUERY_MAX_LINE_BYTES | 4194304 | 4MiB，不能超过明文预算；不受Scanner默认64KiB限制 |
| LOGAN_NATIVE_QUERY_MAX_RECORDS | 100000 | 单任务非空日志行数预算 |
| LOGAN_NATIVE_QUERY_TIMEOUT | 30s | 每次后台执行及查询处理的预算 |
| LOGAN_NATIVE_QUERY_WAIT | 跟随 QUERY_TIMEOUT，默认 30s | 首次摘要同步等待上限；显式 5s 等配置仍有效，调用方和 HTTP 外层超时仍约束请求 |

压缩对象下载上限沿用LOGAN_HTTP_MAX_BODY_BYTES；临时目录沿用LOGAN_NATIVE_TEMP_DIR。所有临时文件0600，成功/失败/超时/取消后清理。按默认预算，单worker可能同时使用约32+128+256MiB暂存空间；部署应按并发数和实际历史大小留足磁盘，不能把这些实验预算当成内存或容量验收。查询正文、索引数量和元数据行数另有总量限制，超限整次拒绝，不截断后伪装成功。

## 兼容细节

- info接受Java long范围；brief/detail仍按Java int范围，超出范围HTTP400。非法正负ID的业务错误仍为HTTP200/code400。
- search平台只有1/2触发SQL过滤，其他值表示不加平台条件。beginTime缺失时取调整后endTime减7天。
- 原 `LoganTaskRequest.ready()` 会先对endTime加一天；deviceId有效而endTime缺失/空白会抛NullPointerException。本阶段保留失败，未修成“默认当前时间”。Java独立MVC没有真实容器错误页，因此此类场景只验证抛异常及500失败语义，不声称其HTML错误正文相同。
- 数字参数保留Spring的空白/十六进制绑定规则；重复String参数按逗号连接，重复数值参数按原转换结果处理。列表内部非法数字按NumberUtils回退0，移动端详情ID不会像Web接口一样先过滤0。
- 摘要关键词沿用MySQL `LIKE CONCAT('%', keyword, '%')`，包括原排序规则、百分号/下划线通配符与反斜杠转义；不替换为Web的二进制字面包含判断。真实数据库排序规则仍须联调。
- 逐行读取CR/LF/CRLF分隔及Java空白规则，c/f/l按原Bean字符串转换和数值回退；未知日志类型归0。大写字段、混合大小写重复字段、嵌套结构的null/数值/顺序均有原Java执行样本。
- 四种内容Handler目前都是原样返回：列表只填simpleContent，单条只填formatContent，另一字段显式null。JSON写出不额外HTML转义，保留Java对照的响应字节。

支持标准JSON；Fastjson扩展语法（注释、单引号、未加引号字段）不在当前支持范围。嵌套转换深度最多128，原Java HashMap极端碰撞树化遍历未完整复刻。这些边界需用真实历史SDK样本继续核对。

## 解析与一致性

OSS压缩数据先有界下载到文件，解压到有界明文文件，再逐行转换并写入有界NDJSON暂存文件，不把整份明文或所有明细保存在数组中。OSS下载正文取消时主动关闭；租约失效、任务超时和服务停止会传递取消，发布后关闭并删除暂存流。

首次摘要幂等创建native内部任务；worker使用服务级context，调用者取消不删除任务。启动/运行中扫描pending、retry_wait和过期running任务；数据库UTC时间判定租约，CAS领取增加fence，解析期间续租。失败最多3次、间隔1秒，终态failed保留通用错误码。

任务源按不可变OSS文件名做digest；不支持在同一对象键下任意替换内容后仍把旧解析当成新源。提交先锁内部任务再锁原任务，校验owner/fence/revision/digest及起始租约。在事务外解析，在事务内按最多200行/约1MiB内容分批插入；单条大记录仍受行预算和数据库原列限制。持锁事务内续期后再发布，避免合法长事务仅因固定30秒租约而反复回滚。明细、原任务status=1、内部done一次提交。

已完成任务不重建详情；提交确认丢失后的重试先检查状态，防止改变已发布ID。失败不提交残留明细，不把失败任务标为已解析。Java不遵守内部租约，不能与Go并发处理同一数据域。

## 单列的故障边界

原gzip实现有恢复语义：损坏尾部保留完整换行，非法起始gzip可能得到空明文。Go继续按原协议结果解析；若恢复结果是有效行或空结果，会正常完成，不能把“status=1”解释为压缩文件完全无损。协议Java向量和新增空解压完成测试记录了这个边界。

OSS访问/下载错误、超限、日志JSON错误、空content导致原NOT NULL约束失败、数据库插入/提交失败，则整任务失败或重试，摘要返回HTTP500空响应，而不是原Java部分异常路径的success空数组或残缺已解析状态。这是明确记录的故障保护差异；正常响应和已录制参数场景按Java验收。

超时/饱和不会改成202或要求SDK轮询；后台任务可继续重试，下一次摘要请求仍等待当前状态。未实现完整告警、保留期清理、孤儿OSS对象回收和生产压测，这些仍属于后续运维工作包。

## 当前验证证据

- 47个原Controller+原服务+原过滤器场景，mapper使用隔离替身。44个确定性响应比较状态、头和JSON字节；3个原异常场景只比较失败语义。
- 23个原LoganLogItem/逐行读取向量与20个原内容Handler样本；记录合成数据，无真实用户日志/密钥。
- 真实官方OSS SDK与本机HTTP服务串联上传、下载、解压、逐行解析、暂存、发布替身和7个查询接口；真实TCP验证绑定、CORS、长ID与连接复用。
- SQLmock验证领取/续租失败、版本/fence不符、原子回滚、提交确认丢失、过期恢复候选、稳定ID、分批大小与查询资源上限。单元测试覆盖等待取消后继续、OSS阻塞关闭、临时文件清理、gzip恢复与各预算。
- 大对象转换采用桶计数/摊还重哈希/稳定计数排序，加入10000属性转换的早/中/晚取消测试，避免二次方工作绕过超时。

这些证据不等于真实MySQL多实例、死锁/网络提交丢失、进程强杀恢复或SDK历史数据已经验收。未执行真实迁移、部署或切流；生产readiness仍未开放。

分段计时、上传至全部明细核验的测量口径及阶段 23 实测见 [大日志延迟报告](native-large-latency.md)。
