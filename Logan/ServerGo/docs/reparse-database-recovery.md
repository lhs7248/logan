# 补数与回滚的数据库故障恢复验证

本阶段只扩展隔离验收工具，不修改原Java、Go业务协议、补数核心或生产配置。不操作生产数据，不切换生产入口。入口为 `script/verify-reparse-database-recovery.py`，复用已有CLI、MySQL协议代理和真实Java/Go读取探针。

## 验收矩阵和证据标准

补数apply和回滚rollback分别执行以下四类故障，共八个场景，每次使用原计划/操作ID恢复：

| 故障 | 切点证明 | 恢复要求 |
| --- | --- | --- |
| 提交前MySQL强杀重启 | 测试触发器暂停实际INSERT/DELETE；PROCESSLIST关联INNODB_TRX，确认唯一事务已持锁，独立连接看不到未提交变化 | 同容器、同持久卷重新启动；全部业务表、审计及对象快照与故障前一致；原操作重试真正执行，随后重试noop |
| 提交后MySQL强杀重启 | 协议代理收到真实COMMIT成功OK但不转发，独立连接已读到完整业务结果与最终审计状态，CLI仍阻塞且无成功输出 | 重启后与已提交见证全快照一致；原操作重试noop，重复重试不写数据 |
| 提交前连接中断 | 同一事务语句屏障，定向KILL CONNECTION，只中断已确认的工具连接 | MySQL仍运行，业务/审计全部回滚；同操作重试成功，重复noop |
| 提交后连接中断 | 同一成功COMMIT屏障，杀死仅负责该操作的代理，关闭客户端SQL连接 | MySQL仍运行，已提交业务与审计保留；同操作重试noop |

数据库使用独立、带归属标签的Docker命名卷，而非原验收的tmpfs数据目录。每次重启核对server UUID、容器ID和完整挂载信息，必须是同一数据库恢复；记录进程exit137、非OOM，以及 `innodb_flush_log_at_trx_commit=1`、`sync_binlog=1`、binlog启用。TCP正式服务就绪后才执行SQL，不把初始化阶段临时socket视为就绪。

提交前、恢复后第一次重试前、重试后及重复重试后均留完整表/审计/对象快照。补数必须保留原任务及原详情全部字段，恢复三条源记录，包括两个内容相同但物理ID不同的尾部记录；审计append_ids必须精确指向这两个新增ID。审计中before/after原始JSON的SHA256逐个核对，并与完整业务行对照；回滚只恢复原始前缀，不回收AUTO_INCREMENT间隙。

## 真实读取兼容

每个故障恢复并按原操作重试后，串行启动原Java源码编译的WAR（Tomcat9/JRE17）和既有Go镜像，验证同一任务和物理详情。核对任务信息、最新列表、带完整时间界限的搜索、类型、索引、批量详情、每条详情、下载地址和实际对象字节。回滚状态还逐个读取已删除的追加ID，要求data:null。每个运行时读取前后目标任务和全部详情字段不变，Go完成任务查询可能更新辅助处理任务，不能扩大为全库零写入。

Java/Go仅对已知本地OSS虚拟主机/路径寻址差异及签名动态字段做受限归一化，其余JSON、HTTP状态、MIME和固定CORS完整比较。Go readiness仍要求503。此轮范围是Native补数读取，不是重新认证全部20个接口。

## 复验

先执行现有Java基线，准备当前linux/arm64 CLI，以及MySQL8.4.11、Python3.12-alpine、Tomcat9/JRE17和已核验Go镜像。只允许专用Docker Unix socket及新的私有证据/工作目录：

```sh
python3 script/verify-reparse-database-recovery.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary <current-linux-reparse-cli> --go-image <verified-go-image> --out <fresh-private-evidence> --work-dir <fresh-shared-private-workdir>
```

数据库迁移只显式应用到随机 `logan_reparse_it_` 隔离库。无宿主业务端口，网络内部隔离；清理逐项核对名称和归属标签，只删除本次创建的资源。配置文件、WAR、测试RSA副本在退出时回收；报告含合成日志，应按私有数据保存。

## 尚未覆盖

mysqld进程SIGKILL和本地连接中断不等于物理断电、宿主磁盘损坏、VM掉电、MySQL主从切换或任意网络分区。第十五阶段当时未验证TLS及复制提升，后续证书校验与受控GTID提升验证见 [TLS与主从提升](reparse-tls-failover.md)，其中跨UUID旧操作自动恢复仍不支持。真实用户历史数据、大规模批量修复、Web重解析、amd64或原JRE8运行时仍未验证。用户没有隔离OSS凭据，真实云OSS上传下载、签名密码学校验、权限与云端故障仍受环境限制；当前仅本地对象夹具。原Java服务保留，未进行生产切流。

## 本轮执行记录

最终第三轮隔离执行退出0，八个场景和真实读取均通过。具体结果：

| 场景编号 | 故障 | apply重试 / rollback重试 |
| --- | --- | --- |
| 0-a / 0-r | 提交前mysqld SIGKILL，同卷重启 | 均noop:false，随后重复均noop:true |
| 1-a / 1-r | COMMIT成功OK被扣留后mysqld SIGKILL，同卷重启 | 均noop:true，完整快照不变 |
| 2-a / 2-r | 提交前事务连接被KILL CONNECTION | 均noop:false，随后重复均noop:true |
| 3-a / 3-r | COMMIT成功OK被扣留后代理连接中断 | 均noop:true，完整快照不变 |

四次MySQL进程均exit137且非OOM，前后同容器、同命名卷、同server UUID和刷盘配置。生命周期记录只有初次启动加四次崩溃重启，后续四个连接场景没有额外重启。八个被中断CLI均退出1，未输出成功结果；恢复后的第一次重试严格区分已提交与未提交，重复重试均noop且全表/审计/对象快照不变。所有审计before/after JSON摘要、完整旧行、新增物理ID和源记录语义核对通过。

八个恢复后的状态分别执行实际Java和Go读取，每端每状态11个响应：总计176响应（160个JSON和16个下载302），另有16次对象GET200；健康探测及readiness503不计入业务计数。四个回滚状态均逐个核对本轮已删除详情ID。每个读取进程前后目标业务行不变。Java地址观察为OSS V1查询字段，Go为V4查询字段；只验证SDK生成与本地传输字节，没有验证真实云端签名有效性或签名格式逐字节相同。

环境为可丢弃VZ VM、linux/arm64、Docker29.9.1/cgroupv2、MySQL8.4.11、Tomcat9/JRE17及已验证Go镜像。数据是合成Java形状的已完成Native前缀，原Java执行真实读取，不能称为真实用户数据。测试故障连接显式设置readTimeout=90s，事务暂停屏障为120s，CLI总预算仍2分钟；生产默认连接超时没有修改，此结论不扩展为所有超时配置已验收。

本轮未重建HTTP服务镜像，CLI源码与之前正式二进制摘要核对一致；重构旧控制器setup以复用初始化、增加可覆盖存储选项，原入口默认仍使用tmpfs。读取探针对任意阶段名支持显式已删除ID列表，不修改业务接口。Go完整test/race/vet/build、原Java基线、89项工具回归、Harness检查和17项自测通过；原Java/SDK/部署/golden均未改。

证据在系统临时目录，可能被系统清理：

- `/private/tmp/logan-reparse-db-3/summary.json`：最终退出0、八场景、八恢复读取状态及归属清理通过。
- 同目录 `0-a.json` 至 `3-r.json`：故障点、事务连接ID或真实COMMIT屏障、恢复业务/审计、重试结果、审计快照摘要；各 `*-before.json`、`*-recovered-before-retry.json`、`*-after-retry.json`、`*-after-repeat.json` 为完整快照。
- 各 `*-java.json`、`*-go.json`、`*-diff.json`、`*-events.json` 和 `real-reader-summary.json`：接口捕获、完整对比及逐进程目标行不变证据。
- `first-retry-snapshot-audit.json`：另行核对第一次重试的全快照、对象和无关表。最终控制器补入这些断言后，对全部8对真实容器快照重放通过，未为此重新启动容器；源码清单区分实际容器执行与最终控制器摘要。
- `matrix-audit.json`、`mysql-lifecycle-audit.json`：独立汇总核对计数、签名字段观察、同容器生命周期和连接场景未重启证明；`source-manifest.json`：CLI源码未改与本轮夹具摘要。
- `/private/tmp/logan-mysql-recovery-runtime-ready.json`：实际镜像ID/digest、引擎和VM隔离配置。
- Go full：系统临时目录 `logan-agent-73bk20rv/summary.json`；Java基线：`logan-agent-h8j2xd2a/summary.json`。
- `/private/tmp/logan-db-recovery-tools-final-2.log`（89项）、`logan-db-recovery-harness-final.log`（17项）及 `logan-db-recovery-cleanup.json`（最终资源清理）。

前两轮失败保留：第一轮断言未接受MySQL将启用binlog返回数字1，核对后同时接受明确启用的1/ON，仍拒绝0/OFF；第二轮对PROCESSLIST.INFO匹配外层INSERT过窄，客户端在默认10秒读取预算内退出，未计故障验收通过。最终使用唯一隔离库中User sleep且持锁的INNODB_TRX连接确定事务屏障，保存连接ID与实际SQL记录，并显式扩展测试连接预算。初次工具回归的本地进程启动受sandbox限制，在允许本地测试进程的环境重跑89项通过，失败未删除。独立子任务执行因用量限制未完成，本轮由主执行者实现并复核，不声称通过独立代理评审。
