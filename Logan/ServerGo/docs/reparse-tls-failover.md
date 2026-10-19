# 补数/回滚的 MySQL TLS 与主从提升验收

本文记录第十六阶段未授权跨UUID的拒绝行为。后续单操作源端见证及显式授权恢复见 [受控身份迁移](reparse-identity-migration.md)；原UUID保护仍保留，未授权/无见证请求仍拒绝。

本阶段只扩展隔离验收工具，不修改原Java、Go业务接口或补数身份校验，不执行生产数据操作和切流。入口为 `script/verify-reparse-tls-failover.py`，复用已有CLI、完整数据库快照和真实Java/Go读取探针。

## 必须区分的恢复能力

现有计划及审计绑定 `server_uuid/database`。真实从库提升会改变UUID，旧计划的apply和旧审计的rollback因此安全拒绝；这是现有保护边界，不是已实现的跨主自动幂等恢复。测试禁止复制旧主UUID、篡改计划指纹、改写审计身份或删除检查。已经在旧主补数且复制到新主的审计，目前无法直接在新主回滚；需未来独立设计受控身份迁移，不能把重新dry-run冒充原操作的幂等重试。

本轮验证两条路径：同一UUID下TLS中断后的原操作恢复；主从提升后完整数据/审计保留、旧操作拒绝零写入，以及未提交任务在新主重新dry-run得到新操作后的补数/回滚。后者是受控新计划恢复，不是旧计划续跑。

## TLS 检查

生成两天有效的独立测试CA及服务证书，SAN包含两个真实容器DNS名。CA私钥只用于签发，MySQL私钥通过专有命名卷、mysql UID999、0400权限提供；不会写入原服务配置。工具使用DSN `tls=true` 和只读CA信任文件，Java使用 `sslMode=VERIFY_IDENTITY` 加测试PKCS12信任库，Go读取服务使用 `tls=true` 和 `SSL_CERT_FILE`。不使用skip-verify或优先TLS后退回明文。

MySQL强制 `require_secure_transport=ON`，工具repair账户和服务reader账户均REQUIRE SSL，仅授权随机隔离库；复制账户单独REQUIRE SSL。复制连接启用证书及服务名校验。管理员验证连接与业务连接分开：通过Performance Schema关联具体repair/reader/repl连接的ID、TLS版本和cipher，不用管理员自身的加密状态冒充客户端证据。

- 正向：dry-run、补数、原操作重复noop、回滚及重复noop；提交前真实持锁事务中KILL CONNECTION后，原操作重试恢复，完整业务/审计/对象快照一致。
- 负向：明文、错误CA、错误DNS主机名均必须失败，完整数据库及对象快照不变。
- 每个实际Java/Go读取进程在业务探针成功后、停止前，都必须观测其reader会话TLS版本和cipher。

故障夹具连接读取预算显式90秒、触发器暂停120秒，CLI总预算仍2分钟。未修改生产默认超时；没有使用明文COMMIT代理解析TLS流量，不声称本轮覆盖TLS中“提交OK丢失”的精确切点。

## 主从提升的故障点

使用两个真实MySQL8.4.11节点、独立持久卷、不同server-id和真实不同UUID，GTID及log-replica-updates启用，副本追随源端。只复制随机 `logan_reparse_it_` 库的全部表，包含补数审计；不是只同步业务详情。测试账户在两节点本地显式创建且不写binlog，避免账户DDL重复；该夹具不是全实例备份还原或生产复制配置。

准备三类任务：尚未补数、已补数且审计applied、已回滚且审计rolled_back。先确认复制双线程健康、无错误、TLS校验开启，等待已确认源GTID集合全部应用。接着同时暂停未补数任务的INSERT和已补数任务的DELETE，确认真实事务持锁且外部看不到未提交变化，再SIGKILL旧主并保持停止，两个CLI均须失败。

提升前再次确认已知GTID到齐，停止复制线程后解除新主只读；必须先证明旧主已退出137且非OOM，不能出现双写。比较提升后的完整业务表、审计表及对象快照与故障前已确认状态，确认两个进行中事务均未部分发布。

新主对三类旧计划及已有两类旧审计均要求拒绝，拒绝前后全快照不变。随后对尚未提交任务在新主生成全新计划，补数追加两条重复尾记录，审计before/after原始JSON摘要、旧字段和新增ID核对；重复apply全快照不变；rollback恢复除保留审计外所有原业务表，重复rollback全快照不变。

## Java/Go 读取范围

读取七个状态：同主TLS中断恢复补数后/回滚后、提升后的待补数/已补数/已回滚任务、新主新计划补数后/回滚后。每个状态依次启动原Java WAR/Tomcat9/JRE17、已验证Go镜像，强制TLS并观测目标连接。

核对任务、最新列表、搜索、类型、索引、批量和单条详情、下载地址及对象字节；回滚还查询已删除ID要求data:null。每个进程前后目标任务及详情所有字段不变；Go允许更新完成任务的辅助处理记录，不声称全库零写入。签名URL只对既有本地夹具地址和动态签名字段作受限归一化，不能扩展为真实云端签名认证；Go readiness仍为503。不是全部20个接口的重新认证。

## 复验入口

先执行原Java基线编译，准备正式linux/arm64 CLI及既有Go镜像、官方MySQL/Python/Tomcat镜像，再运行：

```sh
python3 script/verify-reparse-tls-failover.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary <current-linux-reparse-cli> --go-image <verified-go-image> --out <fresh-private-evidence> --work-dir <fresh-shared-private-workdir>
```

要求专用引擎、全新私有工作/证据目录和隔离合成库，不发布宿主业务端口。正常及失败退出均按名称/标签回收本次资源、临时证书/私钥/信任库、WAR和配置。报告含合成数据，只留本地私有证据。

## 环境和能力限制

没有真实云OSS凭据，只使用本地HTTP字节夹具。未验证云端TLS/签名/权限、生产用户数据、全部20接口、JRE8、amd64、真实浏览器/设备。复制是受控异步GTID追平后提升，不是自动选主、半同步/Group Replication、MySQL Router/ProxySQL、DNS自动漂移或生产HA验收。不覆盖未追平提升的数据丢失、复制延迟容量、网络分区/脑裂、物理断电/磁盘损坏、TLS证书轮换/过期、双向客户端证书认证或旧审计跨UUID的受控迁移。

参考MySQL官方说明：[会话SSL状态](https://dev.mysql.com/doc/refman/8.4/en/sys-session-ssl-status.html)、[GTID复制](https://docs.oracle.com/cd/E17952_01/mysql-8.4-en/replication-gtids-howto.html)、[复制过滤规则](https://dev.mysql.com/doc/refman/8.4/en/replication-options-replica.html)。实际结果必须以本轮证据为准。

## 实测记录

最终第五轮完整隔离容器矩阵退出0。实际结果如下：

| 验证项 | 已实测结果 |
| --- | --- |
| TLS拒绝项 | 明文、错误CA、错误DNS名三项拒绝，完整数据/审计/对象快照不变 |
| TLS提交前连接中断 | apply/rollback各一个持锁屏障，目标repair会话的TLS版本/cipher均实际观测，KILL后CLI退出1，原操作重试noop:false，再次重试noop:true，全表重复快照不变 |
| 真实复制 | 两台独立MySQL UUID、GTID复制双线程正常、错误码0，复制TLS证书/主机名校验开启且观测真实repl会话；确认已知源GTID到齐 |
| 提升故障点 | pending任务30的INSERT与applied任务40的DELETE同时持锁暂停，均观测真实repair会话TLS；旧主exit137非OOM并保持停止，两个CLI均退出1，之后才提升副本 |
| 提升一致性 | 新主UUID不同；完整隔离业务库、审计及对象快照与故障前已确认状态相等，没有两个进行中事务的部分写入 |
| 跨UUID边界 | 任务30/40/50的旧apply计划及任务40/50的旧rollback审计共5次拒绝，各次全快照不变；明确不支持原操作跨主自动恢复 |
| 新主新计划恢复 | 对原未提交任务30重新dry-run，追加两个重复尾记录；完整before/after审计JSON摘要、旧字段和物理ID一致；重复apply全快照不变；rollback恢复全部原业务表，重复rollback全快照不变 |
| 实际读取 | 七状态、原Java/Go共150端点响应（136 JSON及14下载302）、14次对象GET200；14个运行时都实际观测reader TLS会话；目标业务行前后不变，旧ID/重复尾/已删除ID读取核对通过 |
| 回归 | Go full test/race/vet/build、原Java基线、99项工具测试、Harness检查及17项自测通过；独立复核无新增阻断 |

使用已有已验证Go镜像 `sha256:c20df6288ab67bb2208cce7f94b5a29a6c583c22e8427664fac0ebe204a898d4` 和正式CLI；核对CLI/协议源码摘要未变，没有重建业务镜像或更改生产Go代码。环境为Docker29.9.1/cgroupv2、linux/arm64、MySQL8.4.11、原Java源码构建的Tomcat9/JRE17 WAR。真实Java使用临时TLS配置副本，原源码、配置、SDK、部署及golden保留，未放宽readiness503。

测试工具还做了三处共用调整：初始化允许显式传MySQL服务端选项，默认旧入口不变；审计校验从原任务行读取任务ID，覆盖非20任务；CLI将环境文件与覆盖项合并成唯一键的0600文件后启动，避免环境键重复。以上不改变服务接口或补数身份保护。

证据在系统临时目录，可能被系统清理：

- `/private/tmp/logan-reparse-tls-ha-5/summary.json`：最终退出0、全部场景及归属资源回收通过。
- 同目录 `tls-negative.json`、`t-a.json`、`t-r.json`、`tls-before-kill-*.json`：TLS负例、真实连接证据、原操作中断恢复及完整快照。
- `replication-status.json`、`ha-promotion.json`、`ha-before-fault.json`、`ha-paused.json`、`ha-promoted.json`、`ha-refused-*.json`：GTID、复制SSL、旧主fencing、不同UUID和完整业务/审计快照；`ha-cli-tls-*.json`、`ha-barrier-identity-*.json` 证明两个实际事务及认证用户。
- `new-primary-audit.json`、`new-primary-*.json`：新主全新操作的审计JSON摘要、apply/rollback及重复全快照。
- `t-a/t-r/h30/h40/h50/n-a/n-r`对应 `*-java.json`、`*-go.json`、`*-diff.json`、`*-events.json` 和 `real-reader-summary.json`：真实TLS读取及目标行不变证据。`matrix-audit.json`独立重核150响应/14下载和目标会话观测；健康探测与readiness不计数。
- `source-manifest.json`：本轮控制器、读取探针/测试及共用工具SHA256、正式CLI与未变源码摘要；`/private/tmp/logan-tls-runtime-ready.json`记录实际镜像digest/引擎和隔离配置。
- Go full：系统临时目录 `logan-agent-4gxzg5jv/summary.json`；Java基线：`logan-agent-cbtf4_u5/summary.json`；`/private/tmp/logan-tls-tools-final-2.log`为99项工具回归，`logan-tls-harness-final.log`为17项自测。
- `/private/tmp/logan-tls-cleanup.json`：容器/卷/专用网络余量0，14个指定目录中env/WAR/PEM/P12/KEY残留0，专用VM及socket删除；证据、计划、镜像归档保留。

首四轮失败保留且均未计提升成功：第一轮复制夹具密码超过SOURCE_PASSWORD的32字符限制，改为独立32字符复制密码并扩展日志脱敏；第二轮状态采集使用skip-column-names导致垂直结果缺字段名，改为保留名称并严格解析、增加失败负例；第三/四轮按INFORMATION_SCHEMA.PROCESSLIST.USER筛选repair导致错过触发器中的目标事务。触发器执行期间该视图显示DEFINER root，而Performance Schema保留认证repair；最终实测两连接分别记录root/repair、User sleep及5/10行锁，并核对同ID的TLS。因此改用认证连接身份关联事务，没有移除持锁/暂停条件。

诊断曾先怀疑重复环境变量；去重后仍复现，证明它不是此屏障超时的根因，保留去重只是稳健性改进。两个 `logan-reparse-tls-ha-debug-*` 运行跳过读取，仅用于诊断，不计验收通过。MySQL8.4.11官方源码的 [processlist取当前安全上下文](https://github.com/mysql/mysql-server/blob/mysql-8.4.11/sql/sql_show.cc) 和 [触发器切换definer上下文](https://github.com/mysql/mysql-server/blob/mysql-8.4.11/sql/sp_head.cc) 与实际身份差异一致。

本轮结论是所列切点的隔离验证通过，并且明确验证了跨UUID自动恢复不受支持；不能称为已经实现无感主从切换或获得生产上线资格。
