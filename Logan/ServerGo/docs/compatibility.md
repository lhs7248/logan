# 接口兼容基线

## 证据分层

| 层级 | 当前证据 | 范围 |
|---|---|---|
| 原始源码 | 20 个路由、33 个初始用例、72 个源码/SQL/配置文件SHA-256 | 路由、Controller、模型、Mapper及已知行为 |
| 原Java Controller执行 | 14 个确定性用例 + 5个模型序列化样本 | Spring MockMvc/Jackson，service为隔离替身 |
| 原Java完整HTTP应用 | 尚未采集 | 需要隔离部署、实际容器和数据库 |
| SDK协议 | 12个原Java执行的移动端合成向量，尚无真实SDK日志 | AES填充探测、多帧、大帧、gzip损坏和截断 |
| MySQL/OSS集成 | 尚未执行 | 不将静态映射和Controller替身算作集成通过 |
| Go业务接口 | 配置启用后3个移动端接口实现，另有10个Web与7个移动端查询接口可配置启用，全部20个映射有实现 | 不具备生产替换条件 |

控制器实测 `Content-Type: application/json`，JSON默认保留null；业务 `code=400` 的参数错误仍可使用HTTP 200。Go兼容写出函数已经与录制样本比较。

`cases.json` 的runtime_verified=false指完整部署链路未验证，Controller隔离证据单独存放在golden目录及其run索引中。Go测试会检查路由覆盖、完整Java源码指纹、GORM表映射、DTO与原Java模型输出；原Java变更后需重新审查基线，不能自动接受。

## 路由清单

下列为Controller路径。部署上下文默认 `/logan-web`，前端nginx将外部 `/logan/` 转发到后端 `/logan-web/logan/`。

| 方法 | 路径 | 原实现行为 |
|---|---|---|
| GET | `/logan/latest.json` | latest 20 by id then DTO ordering |
| GET | `/logan/task/search.json` | inclusive add_time bounds; endTime adds one day |
| GET | `/logan/task/{taskId}/info.json` | task DTO; nonexistent task returns success null |
| GET | `/logan/meta/logtypes.json` | types 1/2/3; Tuple first/second fields |
| GET | `/logan/task/{taskId}/brief.json` | lazy parsing; id ordering; pages of 20 |
| GET | `/logan/task/query/details.json` | detailIds order including absent rows |
| GET | `/logan/task/{detailId}/detail.json` | formatContent and null fields |
| GET | `/logan/task/{taskId}/download.json` | relative /logan/downing?name= key |
| GET | `/logan/downing` | 302 no redirect follow; 400 missing name, 404 signing failure |
| POST | `/logan/upload.json` | binary upload headers and raw body |
| GET | `/logan/web/search.json` | endTime plus day; device/date group |
| GET | `/logan/web/latest.json` | maxId-200 inclusive range; aggregate and cap at 20 |
| GET | `/logan/web/detailIndex.json` | lazy parsing; minute offsets; pages of 20; SQL currently ignores keyword |
| GET | `/logan/web/taskDetail.json` | representative selected by pageNum |
| GET | `/logan/web/details.json` | IN query ordering not guaranteed |
| GET | `/logan/web/logDetail.json` | NumberUtils invalid ID becomes zero |
| GET | `/logan/web/exception.json` | success exception string; emits error log |
| GET | `/logan/web/getDownLoadUrl.json` | relative /logan/web/download.json?tasks= value |
| GET | `/logan/web/download.json` | HTTP 201; concatenated content sorted by pageNum |
| POST | `/logan/web/upload.json` | JSON v0/v1; page overwrite does not reset status |

## 初始用例范围

每个接口都有正常用例定义，涉及数据库/日志的正常用例需要独立数据夹具。确定性控制器用例覆盖类型列表、多个缺失/非法参数、Web无效上报、缺少下载名称、固定Web下载URL。

下一阶段逐接口扩充正常、缺失参数、类型转换失败、不存在对象、空结果、排序、分页、跨日边界、OPTIONS/CORS与异常容器响应。当前33个初始用例不是完整边界测试集。

`golden/`中的结果来自原Java实际执行；`captures/`是被忽略提交的运行证据目录。采集工具不跟随302，保留原始二进制，最多读取64MiB响应，单次请求30s超时。比较严格保留null/缺失/空数组、数组顺序和大整数，不任意忽略动态字段。

## 已知差异与待决策行为

1. `LoganTaskRequest.ready()` 在默认值处理前对nullable endTime加一天；缺少endTime可能抛NPE。不能直接假定默认时间有效。
2. `WebLogTaskMapper.updateContent`只更新content，不重置status；已解析页覆盖后的行为需独立决策。
3. Web详情查询Mapper虽然接收keyword，但SQL未使用它；不能在兼容阶段悄悄改变过滤语义。
4. 详情批量写入是异步的，任务状态可能提前完成；可靠性修复需要独立完成条件与故障测试。
5. Java清理任务删除Web父任务但未实际清理详情；正式清理策略在后续任务实现。
6. 数据库有NULL字符串和NULL log_date；数据库映射保留NULL，对外Java primitive字段默认0的转换必须在业务层明确处理。
7. Java Web DTO的updateTime默认序列化成毫秒数，不能直接把Go time.Time的RFC3339字符串返回给前端；content不对外输出。
8. 上传返回的是固定相对 `/logan/downing?name=...` 链接；签名下载是302，Web文件下载是201。
9. 原搜索有未声明ORDER BY的SQL、Web compareTo整数溢出等行为；需要真实数据确认可观察的排序，不能为方便而全局排序响应。

## 使用完整夹具采集

在本地未提交JSON变量文件中提供字符串键：DEVICE_ID、NATIVE_TASK_ID、NATIVE_DETAIL_ID、NATIVE_DETAIL_IDS、WEB_TASK_IDS、WEB_DETAIL_ID、WEB_DETAIL_IDS、OBJECT_KEY、NATIVE_UPLOAD_FILE、WEB_UPLOAD_FILE。ID和对象key来自专用测试数据，上传文件为真实协议的脱敏样本。变量文件路径可用 `tests/contract/vars.local.json`，它已被忽略提交。

```sh
go run ./cmd/contract --mode capture \
  --base-url http://127.0.0.1:8080/logan-web \
  --suite all --allow-side-effects \
  --vars tests/contract/vars.local.json \
  --out tests/contract/captures/java-full
```

`all`会上传、触发解析并写入数据库，也会调用故意打错误日志的演示接口。仅对隔离部署执行；即使只是GET也可能写数据。启动原完整Java应用还会启动清理任务，测试环境不能指向生产资源。

所有夹具、变量及baseline文件在发请求前预检；缺失即失败。采集目录必须是新目录，失败运行不产生complete运行索引，后续验证拒绝使用不完整或指纹不匹配的证据。

Java/Go对比前恢复同一数据库快照与对象集；上传生成UUID、详情自增ID、当前时间和OSS签名URL不能直接逐值比较。完整动态用例的字段规则、身份对应关系与签名语义验证在业务实现阶段补充，当前工具不会自动掩盖这些差异。默认safe确定性用例可直接严格比较。

完整HTTP基线、SDK样本与真实数据验证没有完成前，不把第一阶段声明为完整兼容验收。

移动端另增加9个原Java元数据样本与9个含原过滤器的HTTP样本，覆盖状态码、业务错误、空响应、CORS及参数边界。OSS测试使用真实官方SDK访问本机模拟端点，数据库测试使用sqlmock；不宣称真实云端/数据库集成通过。详见 [移动端实现说明](native.md)。

Web新增34个原Controller/过滤器响应与原协议向量；细节和未覆盖边界见 [Web说明](web.md)。故障保护差异单列，真实SDK与真实数据库并发不在替身证据覆盖范围。

移动端查询另增加47个原Java场景、23个原日志记录解析向量和20个原Handler样本。资源/故障保护、缺失endTime的异常边界和证据限制见 [移动端查询说明](native-queries.md)。全部映射有实现不代表真实依赖或生产验收已完成。
