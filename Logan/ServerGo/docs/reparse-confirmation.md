# 补数提交确认丢失与真实读取验收（2026-10-16）

本轮验证独立补数工具的故障恢复及Native读取兼容，不改变原Java、Go业务协议、数据库迁移或工具核心。仅新增测试夹具、读取探针和隔离控制器；WAR构建器增加受限的隔离修复库前缀。没有操作生产数据或生产入口，原Java服务保留，Go readiness实际保持503。

## 故障点如何证明

测试使用MySQL明文协议代理，识别工具实际发送的COM_QUERY COMMIT。在收到MySQL返回的成功OK包后，将该包扣留，不转发给工具；不记录认证内容、SQL或日志正文。每个动作使用单独代理，只拦一次成功COMMIT；错误包会原样转发，不能当作已提交证据。

代理屏障到达后，通过绕过代理的独立数据库连接检查业务详情及该操作审计状态，证明提交已经对其他连接可见。工具进程此时仍运行且尚无成功输出，再SIGKILL得到137。随后启动新的CLI进程，使用同一计划或回滚操作ID重试，必须返回noop，完整表/审计/对象快照不得变化。

补数和回滚分别执行上述故障点，不能用之前“INSERT中途杀进程、事务未提交”的证据替代。本轮也会重跑原有提交前强杀、并发、幂等、数据变更拒绝、非严格SQL转换及事务失败用例。

## 真实Java/Go读取

对同一物理任务，依次验证三个状态：补数前1条、补数后3条（相同尾部日志保留为两个不同ID）、回滚后原1条。每个状态启动原Java源码编译的WAR/Tomcat9/JRE17，停止后再启动已验证的Go镜像；两端退出后才执行下一写入阶段，不经过生产网关。

探针核对任务信息、最新列表、搜索、日志类型、索引、批量详情、每个物理ID的详情、下载地址和实际签名地址下载字节。逐值比较完整JSON、HTTP状态、MIME和固定CORS；索引不能空或少记录，ID/内容/类型/时间必须与已独立核对的数据库记录匹配。回滚后还逐个查询本操作新增后已删除的详情ID，要求成功响应中的data为null，防止残留读取。

每个Java/Go进程运行前后，比较目标任务20及其全部详情字段，证明目标业务行不被读取流程改写。Go的完成任务查询可以创建/更新处理任务的done记录，不能把此处结论扩大为“所有数据库表零写入”。

签名地址中的有效期、签名值和主机包含运行时差异；本地夹具的Java虚拟主机路径 `/logan-logs/history.log` 与Go路径寻址 `/fixture/logan-logs/history.log` 单独记录并仅对这两种已验证地址形式归一化。任意其他前缀、非夹具主机或外部重定向均拒绝。实际对象必须逐字节相同，不能只凭下载成功判兼容。

## 环境与剩余限制

- 独立可丢弃VM、linux/arm64、Docker29.9.1/cgroupv2、真实MySQL8.4.11，无宿主TCP/UDP转发或业务端口。原Java WAR只配置隔离数据库及本地对象端点，原源码/配置不改；没有验证原JRE8镜像或amd64。
- 源任务仍是明确构造的Java形状已完成前缀，不是生产用户历史数据。真实Java执行的是读取路径，不是重新产生这些前缀；早期实际Java日志截断对照另见 [容器SDK验收](container-sdk-validation.md)。
- 使用本地HTTP存储夹具，实际OSS SDK读取/签名生成经过此夹具，夹具不验证签名密码学。用户没有隔离OSS凭据，真实云OSS读取、签名权限/失效及云端故障仍受阻，无云写入。
- 代理只支持本次显式禁用TLS的MySQL协议；它是测试夹具，不用于生产，也不是TLS链路或任意网络分区的认证。
- 第十四阶段当时不覆盖MySQL进程崩溃；后续提交前后mysqld强杀同卷重启及连接中断实测见 [数据库故障恢复](reparse-database-recovery.md)。主从切换、物理断电耐久性、真实云存储变动、大规模批量修复、Web补数、浏览器/设备、生产网关或生产切流仍未覆盖。客户端重试必须保留原操作ID/计划，不能重新生成计划来代替幂等重试。

## 复验入口

独立控制器为 `script/verify-reparse-confirmation.py`，复用原补数验收的路径、引擎和归属保护：

```sh
python3 script/verify-reparse-confirmation.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary <current-linux-reparse-cli> --go-image <verified-go-image> --out <fresh-private-evidence> --work-dir <fresh-shared-private-workdir>
```

需预加载MySQL8.4.11、Python3.12-alpine、Tomcat9/JRE17与经核验Go镜像，先通过现有legacy-java工程编译原Java。真实业务接口使用既有Go镜像；本轮没有重建HTTP服务镜像。当前CLI二进制以只读挂载运行。缺依赖、接口500、空索引或无法证明提交都不算通过。

## 本轮实测结果与证据

最终独立容器运行退出0，具体结论如下：

| 验证项 | 已实测结果 |
| --- | --- |
| 补数提交确认丢失 | 代理收到7字节、序号1的COMMIT成功OK且未转发；独立连接看到3条详情及applied审计；客户端仍阻塞、无成功输出，SIGKILL退出137。原计划新进程重试noop，完整表/审计/对象快照不变 |
| 回滚提交确认丢失 | 新代理同样扣留7字节成功OK；独立连接看到原1条详情及rolled_back审计；无成功输出的客户端退出137。相同操作ID重试noop，完整快照不变 |
| 补数前读取 | 原Java与Go各9个端点响应、各1次对象下载通过，原详情ID2000不变 |
| 补数后读取 | 两端各11个端点响应、各1次对象下载通过；详情ID2000/2001/2002与已核对的数据库一致，重复尾部日志未被去重 |
| 回滚后读取 | 两端各11个端点响应、各1次对象下载通过；只剩原ID2000，新增后已删除的2001/2002逐个返回data:null |
| 兼容比较及不变性 | 三阶段均完整JSON/状态/MIME/CORS比较通过，目标任务20及所有详情完整字段在每个读取进程前后不变；Go均readiness503，按顺序退出 |
| 回归 | 当前Go完整test/race/vet/build通过；原Java基线通过；81项工具测试、Harness检查和17项自测通过。独立最终复核无阻断问题 |

合计62个端点响应（56个JSON响应和6个下载302）及6次对象GET200。启动健康探测和Go readiness检查不混入此计数。本轮不重新执行SDK上传或全部20个业务接口，不能将Native读取验证扩大为全部接口重新认证。

证据位于系统临时目录，可能被系统清理：

- `/private/tmp/logan-reparse-ack-3/summary.json`：最终退出0，既有补数验收及本轮故障/读取检查全部通过。
- 同目录 `lost-confirmation-apply.json`、`lost-confirmation-rollback.json`：代理已收到成功OK、未转发、独立提交见证、客户端退出/无输出和重试结果。
- `ack-apply-committed-before-retry.json`、`ack-apply-after-retry.json`、回滚对应快照：完整数据库表/审计/对象摘要一致。
- `pre-java.json`、`post-java.json`、`undo-java.json`、对应 `*-go.json`、`*-diff.json` 和 `*-events.json`：实际接口捕获、签名地址样式、逐进程目标行不变证明和串行启动/退出账本；`real-reader-summary.json`汇总三阶段。
- `/private/tmp/logan-ack-source-manifest.json`：新夹具/控制器/探针摘要、实际镜像与CLI身份；原CLI源码与上一阶段摘要核对不变，使用先前正式源码二进制，无诊断overlay。
- `/private/tmp/logan-ack-runtime-ready.json`：实际引擎版本、镜像ID/digest及TCP/UDP转发隔离信息。
- Go full：`/var/folders/6w/c5rms8nd1z10_pdghq0j31bm0000gn/T/logan-agent-6xfq26o8/summary.json`；Java基线：同级 `logan-agent-_j3ma6mw/summary.json`。基线中默认跳过的集成入口不计真实运行，实际读取以本轮HTTP证据为准。
- `/private/tmp/logan-ack-tools-final-2.log`：81项通过；`logan-ack-harness-final-2.log`：17项自测通过。

首轮失败保留在 `logan-reparse-ack-1`：搜索探针漏传原Java必需的endTime导致500，已按合法查询补齐时间界限，没有修改Java或放宽成功断言。签名下载的已知虚拟主机/路径寻址差异使用精确夹具路径规则处理并保留原地址观察，不忽略任意路径差异。工具回归曾因测试用一次recv假设整包到达而失败，改为有界读满预期字节；代理实现没有为通过测试而改变，随后全量通过。`logan-reparse-ack-2`为已通过的中间运行，最终第三轮额外验证了已删除详情ID读取。

最终清理见 `/private/tmp/logan-ack-cleanup.json`：容器/专用网络/卷余量0，临时VM停止并删除、socket不存在，三个运行的env/WAR/RSA副本均无残留；报告、计划和源码摘要保留。六个实际下载地址的签名查询字段另行核对，见 `logan-ack-signed-query-observations.json`，不等于真实云端签名有效性验证。
