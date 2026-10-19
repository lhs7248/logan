# Go 全20接口混合并发与大日志验收

本轮仅运行Go，保留原Java、SDK、golden与原部署，不操作生产数据、不执行生产切流。沿用上一轮实际构建的最终镜像 `sha256:7099b810ac954644e6524666769e3c643d89ca6519c0d3eb5ac18e755520a759`，归档哈希和104个应用源码文件与上轮一致，没有通过改变服务器限制来让压力样本通过。

## 工作负载与数据口径

`cmd/soaktest --mode all20` 交替执行Native/Web完整流程，覆盖每端10个接口，包括上传、搜索、最新列表、元数据/例外、索引、任务、单条/批量详情和下载地址/实际下载。Native实际302及受限同一测试对象主机的签名GET都核对；Web实际HTTP201和原始日志数组核对。详情每批最多200个ID，完整ID集合和内容逐条匹配；最新列表绑定本流程刚上传的TaskID+device，不能用任意非空合成列表冒充通过。异步索引可按原语义轮询，完成工作流必须取得全部记录。

平均到达速率与突发并发分开：正式小日志阶段每秒4个交替工作流，每2秒突发8个、8工作线程，调度660秒并要求宿主采样扣除排空后的活动下界至少600秒。大日志每档调度220秒，每10秒突发2个工作流，2工作线程，要求宿主活动下界至少120秒。报告记录实际重叠的最大工作流数，不能只看线程配置。到达数、完成数、唯一工作流记录数必须精确匹配计划，拒绝错误、丢弃、重试、积压、内容缺失、OOM和MySQL1205/1213。

全20接口的HTTP统计包含Native签名跳转后的对象GET；该请求属于对象依赖，不是第21个Go业务接口。端到端HTTP QPS/分位数明确包含它，Go自身请求数另扣除此项。Native-download路由桶同样包含302和对象GET，不作为纯Go单接口时延。与此前8类调用的负载组成不同，不能据此给出框架/语言倍率。

小日志是重新生成并经OpenSSL独立核验的原CLogan/WebSDK产物回放，Native2条/Web1条。Web使用新生成2048位测试RSA，运行环境不设置 `GODEBUG=rsa1024min=0`，保留默认RSA保护。大日志是独立合成协议向量，不声称原SDK、真机或真实浏览器生成了这些大样本。

大日志每条内容4096字节，使用不同SHAKE内容，记录实际压缩比例，避免重复一个短字符串造成不现实的压缩。Native各档内容1/8/32MiB，对应256/2048/8192条、1/2/8个gzip/AES分帧；32MiB内容的原始JSON约32.26MiB，线上密文约18.2MiB。Web内容1/4/8MiB，对应256/1024/2048条，使用加密v1；线上体积及全部SHA以正式profile报告为准。完整分帧、CBC填充、gzip、CTR与所有内容均由独立OpenSSL/Python oracle核对，错误版本与体积/摘要篡改会拒绝。

## 资源与保护

专属Lima VM3CPU/4GiB、linux/arm64，internal网络，无业务端口发布。Go应用1CPU/1GiB、GOMAXPROCS1/GOMEMLIMIT768MiB；MySQL8.4.11为1CPU/1GiB、256MiB buffer pool。为容纳大fixture和依赖对象，本轮客户端0.5CPU/1GiB、对象夹具0.25CPU/512MiB，和上轮客户端/夹具预算不同；实际资源与节流均保存。服务器默认HTTP32MiB、Native解析/分行、详情ID和响应预算不抬高，readiness仍503。每档重置本次合成表并重启应用/MySQL/对象夹具，不保留前档对象造成测量干扰。

独立SQL按每任务、每明细的ID、OCTET_LENGTH和SHA2核对完整内容，避免大JSON聚合超过MySQL包限制；Native按完整集合、Web按原顺序验证。任务ID与客户端ledger精确一致。所有普通负载job必须done、owner空、lease0、fence1、attempts1、source_revision1、无last_error且source_digest完整。周期采样和客户端退出后的末态均核对1205/1213；正常行锁等待另列，不能把零死锁写成零锁等待。

完整真实MySQL并发回归另覆盖续租、owner/fence替换、活动租约持锁不阻塞、旧owner拒绝、重复Ensure、空范围并发发布、501条历史替换与原有强杀解析恢复。普通负载没有人为制造租约过期，不能把正常运行的fence1等同于负载期间完成了失效主恢复。

HTTP保护探针在本次隔离库检查：Web已完成同身份16次重传与16次并发索引查询，任务/明细/job语义快照不变（不把原有updated_at同步当作重置）；Native同样内容两次上传仍按原接口语义创建两个不同任务/对象，对每任务重复并发解析不重复明细。Native没有新增不存在的全局上传去重。超限Native由完整、已独立核验的SDK分帧重复至超过32MiB，Web为合法加密日志加超大JSON元数据；分别验证原错误语义且业务/job表与对象PUT数均不变，负例不混入成功负载分母。

## 运行与证据

```sh
python3 tests/sdk/generate.py --out /private/tmp/<fresh-sdk> --node-modules <isolated-node-modules> --web-marker 'SDK_CHAIN_WEB_BMP_LOAD + percent%'
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags stdjson -o /private/tmp/<loader> ./cmd/soaktest
python3 script/verify-all20-large-load.py --docker <absolute-docker> --host unix://<dedicated-logan-socket> --allow-isolated-runtime --binary /private/tmp/<loader> --go-image <verified-go-image> --sdk-fixtures /private/tmp/<fresh-sdk> --out /private/tmp/<fresh-evidence> --work-dir /private/tmp/<fresh-shared-work>
```

先加 `--diagnostic` 做短诊断，不能当作正式窗口。所有路径/目标须符合专属临时目录和容器命名保护；离线network-none预检先于业务写入。运行前后冻结源码、二进制和样本摘要，执行期间变更会拒绝验收。测试密钥、DSN不进入版本化夹具。

2026-10-19 实测结论：工具与短诊断完成，正式矩阵未全部通过。不得把阶段通过合并成整轮通过，也不能据此批准上线。证据为 `/private/tmp/logan-phase21-formal`、`/private/tmp/logan-phase21-formal-work`；私有临时目录可能被系统清理。

| 项目 | 实测结果 |
| --- | --- |
| 全20接口SDK小日志混合 | 调度660秒，宿主活动下界630.344秒；2640工作流、27720次端到端HTTP全部成功，实际重叠8；零丢弃、零重试、1205/1213均0。Go自身26400请求，约40QPS；另有1320次对象GET，合计约42QPS。HTTP P95/P99为27.591/35.985ms，包含对象GET。 |
| 数据与任务 | Native1320任务/2640条、Web1320任务/1320条，逐条ID、长度、SHA与客户端账本一致；全部done，owner/lease清空，fence/revision/attempts均1，无错误。 |
| 小日志资源 | Go平均CPU约4.33%单核，RSS峰值22.97MiB；MySQL RSS峰值550.92MiB。无OOM；记录到4301次正常行锁等待，不能写成零锁等待。 |
| Native/Web各1MiB内容 | 调度220秒，宿主活动下界150.031秒；44工作流、506次HTTP成功，实际重叠2，零丢弃/重试/1205/1213；两端各22任务/5632条，全内容及job终态通过。HTTP P95/P99为196.337/239.143ms；Go RSS峰值43.39MiB。 |
| Native8MiB/Web4MiB内容 | 正式阶段失败：MySQL 1GiB硬限额下OOMKilled=true、退出137。最后保留的客户端180秒快照36工作流无错误，不是完整阶段验收，缺失末态SQL核对，不能声称数据完整或稳定性通过。 |
| Native32MiB/Web8MiB内容 | 最终短诊断v4通过完整内容/任务/全20接口检查；1GiB正式矩阵因前档OOM未到达此档。随后改为MySQL2GiB的独立220秒内存诊断，宿主磁盘耗尽、控制器退出120且无最终summary，仍不构成正式通过。 |
| 幂等、超限和隔离保护 | v4短诊断的重复上传/并发解析、合法协议超32MiB拒绝及拒绝前后数据库/对象PUT不变检查通过。Native重复上传仍创建不同任务，未新增全局去重。 |
| 租约与fence | 真实MySQL并发回归通过续租、失效owner、fence替换、空范围发布、重复Ensure、501条历史替换及强杀恢复。负载阶段正常job状态检查通过的范围限于已完成阶段，没有在混合压力期间人为注入失效owner。 |

1GiB失败保存于 `command-1279.json` 的容器末态，采样峰值MySQL RSS约1017MiB，Go最后RSS约44.7MiB。2GiB诊断的performance_schema显示十个连接保留约324MiB的 `String::value`，从约54秒到153秒保持平台；MySQL RSS约1.1–1.2GiB而cgroup使用继续增长。现有证据不足以断言内存泄漏，也不足以把增长全部归因于SQL字符串；文件缓存、分配器保留及瞬时峰值仍需进一步分解。增加MySQL预算只是诊断变量，不是代码修复或2GiB生产容量建议。

宿主盘两次耗尽属于环境限制，不覆盖1GiB实测OOM。2GiB诊断末态MySQL退出2、OOMKilled=false，日志明确ibd写入I/O错误/磁盘空间不足；客户端退出2且报告写入失败。证据在 `/private/tmp/phase21/failure-cleanup.json`，没有伪造缺失summary。已清除本轮可再生Go构建缓存，并将旧合成fixture无损压缩且逐个验证原始SHA；映射在 `/private/tmp/phase21/compressed-fixture-evidence.json`。原报告、失败和应用镜像归档保留。已停删核验归属的专属VM/socket回收空间；Docker逐项删除因故障只读受阻的剩余资源随该VM磁盘回收，未触碰其他项目。

后续需在有充分磁盘余量的隔离环境复跑全部大日志正式窗口，明确数据库内存/连接预算后才能补齐整轮稳定性验收。本轮Go完整测试、race、vet和构建通过；真实MySQL回归通过；Python工具142项通过。应用104文件及镜像指纹保持上一轮版本。测试工具增加all20模式、突发调度、内容账本和独立大协议oracle，服务器接口、保护限额及Java未改动。

诊断历史保留：v1是旧校验口径，仅供工具准备；v2全部负载档通过但保护脚本误挂载未共享源码路径，整体失败；v3负载和保护检查通过，但诊断中补到达数量门槛触发源码冻结保护，整体仍失败；v4最终冻结工具的全部短诊断通过。早期Native超限使用无效字节仅能证明拒绝，已替换为合法分帧超限，不用旧样本声称体积保护通过。

## 尚未覆盖

真实云OSS无凭据，使用内部字节夹具，不验证云签名鉴权/配额/真实网络延迟；SDK小样本是产物回放，大样本是合成协议，不是真机/浏览器端大日志测试。本轮为单机arm64、固定资源、小范围有界并发，内部HTTP/MySQL明文；未验证TLS性能、多实例全20吞吐、amd64、近生产分布、24小时稳定性或生产容量上限。保留原Java不等于本轮执行了Java运行对照；原兼容基线与Go契约回归继续保留，不执行生产切流。

后续[MySQL内存与连接预算定位](large-mysql-budget.md)已复现字符集转换相关保留容量和大参数网络缓冲；磁盘恢复后2GiB三档正式已复跑且未OOM，最大档仍有brief等待失败，独立复核取得完整性证据；稳定性验收仍失败，见后续报告。
