# 主从切换后的补数身份迁移

本页记录身份迁移实现与第十七阶段验收。后续重建镜像、在授权/补数/回滚三个状态执行全20接口与真实SDK的记录见 [完整回归](identity-full-regression.md)，两轮证据范围分别保留。

本功能是离线工具的显式单操作授权，不是自动HA。原Java、HTTP接口和原始业务表结构保留；不修改旧计划、原审计的database_identity、原操作ID或原详情ID。所有入口仍只允许 `logan_reparse_it_` 隔离库，没有生产执行资格或切流。

## 授权如何建立

新增显式迁移 `004_native_reparse_identity.sql`，只创建 `logan_native_reparse_identity_authorization`。每行以原operation_id和目标identity唯一绑定原计划指纹、原始身份、目标身份、见证ID、见证digest及安全见证JSON，并记录整行授权digest。工具只追加，重复相同授权为noop，不覆盖原授权或原审计；表不是防恶意DBA的不可篡改存储。

源端所有写入和维护停止时，针对原计划导出一个0600私有见证：计划及对象绑定、源UUID/database、当前任务/详情快照摘要、原审计摘要和状态、源GTID、随机见证ID。没有日志正文、原始审计正文、密码或OSS密钥。导出使用只读事务，GTID在读取前后必须完全相同；高写入或共享MySQL没有稳定窗口时会保守拒绝。

源审计摘要校验原before/after存档SHA、大小、原字段及新增ID边界；applied状态的实际尾ID、rolled_back存档中的原ID前缀和新增ID后缀也必须匹配。未知状态、源归档损坏、源数据变动或身份不匹配无法导出有效见证。

源端stdout返回 `operation_id`、`source_identity`、`digest`。操作者通过可信渠道独立记录digest，目标端显式提供 `--witness-sha256`。这个值是规范化见证内容（digest字段清空）的SHA256，**使用源端返回的digest，不是对整个JSON文件运行sha256sum**。它是操作者信任的完整性锚，不是数字签名；不能防止恶意管理员同时伪造证据和可信digest。源端见证需要在原主仍可核验时预先导出；没有可信见证不能事后仅凭新主内容追认。

## 目标端检查

只支持单次由原计划UUID到不同UUID、同名数据库的授权。不支持多跳授权链、自动发现目标、改库名、无源见证的恢复或批量改写身份。

目标预检/迁移要求显式输入源和目标identity、可信见证digest、offline声明；写授权还要求 allow-isolated-writes、allow-identity-migration、source-fenced。工具会检查实际连接身份、事务引擎、源GTID已执行、目标完整任务/详情及审计与源见证一致。GTID_SUBSET不证明整个目标库没有额外写入，针对本操作的精确快照校验才是额外保护；相关表以外的数据、整实例权限复制和一般RPO=0不在此承诺内。

授权写入后在同一事务回读完整授权行并逐字段比较，再复核目标业务和审计状态/摘要未变，字段被转换或目标数据被触发器改写时整笔回滚。核心校验范围是目标任务及其审计，不是任意数据库触发器对无关表副作用的通用探测器；本轮验收另外比较全隔离业务库和对象快照。

dry-run使用只读事务。重复相同见证的迁移可在原操作合法补数、回滚后继续返回noop，但当前业务和审计必须一致；已有源审计不得消失、归档变形或状态倒退。原状态、指纹或授权行不符均拒绝，不能用新见证覆盖旧授权。

## 旧操作如何继续

正常apply/retry/rollback依旧先核对UUID。同UUID无需004，原003计划和v1格式继续有效；不同UUID必须找到原操作、原指纹、源/目标精确匹配的有效授权，并重新校验当前业务和审计。进入事务后还会重新读取实际连接身份，防连接重建后落到其他节点。

- pending旧操作：授权后仍使用原计划、原operation_id补齐尾部，创建的审计仍保存原计划的源identity；相同计划重试noop，原操作回滚只删除原审计记录的新增ID。
- applied旧操作：授权后原计划重试noop，可以按原operation_id回滚；原存档、指纹、append_ids不改写，回滚后state按既有语义变为rolled_back。
- rolled_back旧操作：授权后重复rollback为noop，不能把已回滚操作重新apply。

对象SHA、严格前缀、旧字段/ID保留、当前快照变动拒绝、归档大小上限及删除边界都继续生效。业务回滚保留授权和审计证据，不自动撤销身份授权。该机制不阻止具备DBA权限的人协调伪造/删除数据、审计和授权；相应访问控制仍必须独立保障。

## 使用顺序

源端只读导出，`--plan`必须是原计划，`--witness`必须是全新私有绝对路径：

```sh
logan-reparse --action identity-export --task-id <id> --plan <old-private-plan> --witness <new-private-witness> --offline
```

确认源端digest、复制追平、旧主已被基础设施隔离且其他写入停止后，切换工具连接到明确目标。先预检：

```sh
logan-reparse --action identity-dry-run --witness <private-witness> --witness-sha256 <source-returned-digest> --expect-source <source-uuid/database> --expect-target <target-uuid/database> --offline
```

实际只追加授权：

```sh
logan-reparse --action identity-migrate --witness <private-witness> --witness-sha256 <source-returned-digest> --expect-source <source-uuid/database> --expect-target <target-uuid/database> --offline --allow-isolated-writes --allow-identity-migration --source-fenced
```

之后使用原有apply或rollback命令，不生成替代计划、不改旧计划JSON。三类identity动作不加载OSS/AES配置；真正pending补数仍按原行为读取OSS并验证对象。文件权限/所有者、独占创建、符号链接、未知字段、尾随JSON和64KiB上限沿用原保护；见证内部还有更小的安全元数据上限。

source-fenced/offline是明确的操作者声明，工具本身不能证明外部旧主已断电或网络已隔离，也不能代替分布式fencing。授权按目标UUID生效，不提供跨独立复制分叉的全局一次性见证消费或选主共识；操作者必须确保所有可能的旧写节点被隔离。实际验收先SIGKILL旧主exit137且保持停止、确认已知源GTID应用后才提升，不允许双写。

## 隔离验收入口

先编译当前正式linux/arm64 CLI，原Java基线编译保持源码不变。专用Docker引擎预加载MySQL8.4.11、Python3.12-alpine、Tomcat9/JRE17和已验证Go服务镜像：

```sh
python3 script/verify-reparse-identity-migration.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary <current-linux-reparse-cli> --go-image <verified-go-image> --out <fresh-private-evidence> --work-dir <fresh-shared-private-workdir>
```

控制器复用真实TLS及GTID提升；源端导出pending/applied/rolled_back见证，验证只读。目标未授权仍拒绝；危险标志缺失、可信digest错误、源/目标身份错误、未执行GTID、数据变更、原审计档案损坏、冲突见证及授权/业务触发器改写均需拒绝且全快照不变。授权INSERT暂停时强杀新主，恢复同卷后不得有部分授权或业务改动；原见证重试成功，重复noop。并发授权只收敛为一行，可能的事务死锁安全失败后可重试。

恢复三个旧操作及回滚，逐次检查完整业务/审计/授权/对象快照、原计划字节、原audit身份、原操作ID和新增ID边界。十个状态串行执行原Java和Go实际TLS读取，逐进程核对目标行不变，回滚后新增ID返回null；Go readiness503保持。不是全部20接口重新认证。

## 已实测与限制

最终第二轮正式CLI容器验收退出0，结果如下：

| 验证项 | 实测结果 |
| --- | --- |
| 源端见证 | pending30/applied40/rolled_back50三个原计划及一个替代见证只读导出，源全表/对象快照完全不变，见证不含日志正文；源digest独立重算一致 |
| 原身份保护 | 未授权跨UUID旧操作仍拒绝；迁移只读预检3项全快照不变，13类危险标志/信任值/身份/GTID/业务/审计/发布改写/冲突见证负例均拒绝且全快照不变 |
| 授权写入原子性 | 授权INSERT持锁暂停时SIGKILL新主exit137非OOM，原卷原UUID重启，CLI退出1，全部表/审计/对象无部分发布；原见证重试创建授权、再次noop |
| 写后校验 | BEFORE INSERT触发器分别改写witness_digest与目标task字段，两种情况整事务拒绝，授权及目标改写均回滚，全库/对象不变 |
| 幂等及并发 | 同见证重复授权/合法业务状态推进后重试均noop且全快照不变；并发迁移收敛到一条授权；最终仅三个原操作各一行授权，授权整行digest与见证digest均独立重算一致 |
| 旧pending恢复 | 原30计划/operation_id在新UUID补数、重复apply noop、按原ID回滚、重复rollback/迁移noop；原计划字节不变，新审计保留原identity |
| 旧applied恢复 | 原40计划重试noop，回滚旧append_ids成功；授权后故意改业务仍拒绝回滚，恢复正确数据后仅删除原追加ID，原快照/指纹/原始identity不变 |
| 旧rolled_back恢复 | 原50操作重复rollback noop，原审计档案与业务不变；不存在重新apply已回滚操作的捷径 |
| 实际读取 | 十状态、原Java/Go共216端点响应（196 JSON、20下载302）及20次对象GET200；20个读取运行时均观测实际reader TLS会话，目标行不变，旧ID/重复尾和已删ID检查通过 |
| 回归 | 当前Go full的test/race/vet/build、原Java基线、102项工具回归、Harness检查及17自测通过；核心、CLI和写后校验先失败再通过的测试日志保留，独立安全评审无阻断 |

原40/50以及其他旧审计的原始字段、before/after档案和append_ids均独立按完整旧行核对；40回滚只按既有语义将state推进为rolled_back。最终原30/40/50均为rolled_back，所有任务完整行与初始相同；初始详情中仅原40的两个追加ID被删，其他旧详情逐字段保留，新30追加后也按本操作ID完整删除。对象字节摘要不变。授权原记录及见证继续保留，没有将业务回滚当作撤销授权。

本轮只重建正式离线CLI，最终SHA256为 `0794a0f99445d2749a279ca5d7c3da175fa216d1c258c8e456bfcaf34e66406c`，无诊断overlay。Java/Go读取仍使用原源码构建WAR/Tomcat9/JRE17及既有已验证Go HTTP镜像；HTTP服务不导入新增离线迁移能力，没有重建或改变原Java/SDK/部署/golden。隔离环境是Docker29.9.1/cgroupv2、linux/arm64、MySQL8.4.11，合成Java形状已完成Native前缀、本地OSS字节夹具，readiness503保持。

证据在系统临时目录，可能被系统清理：

- `/private/tmp/logan-reparse-identity-2/summary.json`：最终退出0及全部归属资源回收通过。
- 同目录 `source-witnesses.json`、`before-source-witnesses.json`、`after-source-witnesses.json`：只读导出、原计划SHA及源端digest；私有见证/原计划在对应work目录。
- 各拒绝项 `*-before.json`/`*-after.json`，`identity-preview-*.json`：拒绝和预检不变证据；`coerced-authorization-publication-*`、`identity-trigger-business-publication-*`：真实触发器原子回滚。
- `identity-grant-crash.json`、`identity-crash-*.json`、`identity-30/40/50-*.json`：新主崩溃同卷恢复、授权重复及并发全快照。
- `identity-recovery.json`、`old30/40/50-*.json`：三个旧操作恢复、原身份/计划保持、授权后快照冲突拒绝及重复重试；基类 `ha-*.json` 和 `replication-status.json`保留真实TLS复制、旧主停止和不同UUID证明。
- 十个phase的 `*-java.json`、`*-go.json`、`*-diff.json`、`*-events.json`、`real-reader-summary.json`：真实接口与逐进程目标行/TLS见证；`matrix-audit.json`为独立快照、归档SHA和授权摘要重核。
- `source-manifest.json`：核心/CLI/004/控制器/测试摘要与正式构建参数；`/private/tmp/logan-identity-runtime-ready.json`：实际镜像digest和隔离配置。
- Go full：系统临时目录 `logan-agent-yh1mv7c3/summary.json`；Java基线：`logan-agent-seargrjx/summary.json`；`/private/tmp/logan-identity-tools-final.log`（102项）、`logan-identity-harness-final.log`（17项）。
- TDD：`logan-identity-core-red.log`、`logan-identity-cli-red.log`、`logan-identity-publication-red.log`及对应green日志。
- `/private/tmp/logan-identity-cleanup.json`：容器/卷/专用网络及临时env/WAR/PEM/P12/KEY残留0，专用VM及socket删除，私有证据、计划/见证和镜像/CLI归档保留。

第一轮 `logan-reparse-identity-1` 已通过当时全部恢复/读取，使用publication回读补强前的CLI，仅保留为中间证据；最终第二轮重建并完整复验，还新增了两个真实字段/业务改写负例。没有把第一轮旧二进制结果替代最终源码验证。

设计与步骤见workspace的 `docs/superpowers/specs/2026-10-17-reparse-identity-design.md` 和 `docs/superpowers/plans/2026-10-17-reparse-identity-migration.md`。

用户没有真实OSS凭据，仍仅使用本地字节夹具，真实云端上传下载/签名权限/云故障受限。未操作真实用户数据或生产系统；不覆盖自动HA/自动fencing、恶意管理员、无见证追认、多跳迁移、任意复制丢失或脑裂、跨地域容量、物理断电、JRE8/amd64/真实设备浏览器。历史已完成任务仍不会自动批量重解析。
