# 已完成移动端任务的受控重解析与补数

本工具针对已完成(status=1)、但因历史gzip成员截断而仅保存前缀详情的Native任务。原Java、服务API和SDK不变；不会自动扫描或重置完成状态，不执行生产迁移或切流。本阶段CLI只允许明确的隔离数据库 `logan_reparse_it_<lowercase-alphanumeric>`，且要求 `LOGAN_REPARSE_ISOLATED=YES`，其他库名会被拒绝。库名前缀只是命令门槛，本轮另用专用虚拟机和内部网络保证实际环境隔离。

## 方案与边界

选择保留原详情ID并追加缺失尾部。删除再全量重建会改变外部引用ID；复制成新任务会分裂任务身份，因此本轮不采用。已有详情按ID排列后，内容、Java类型投影和日志时间必须是严格解析结果的逐条前缀；重复内容按序号和全部语义字段比较，不能用内容集合去重。非前缀、不完整源数据、活跃处理任务、非InnoDB表均拒绝。

默认dry-run，读取指定单个任务和原对象，严格校验全部gzip成员CRC、长度、尾部、JSON和资源预算。它保持服务端既有宽容解析不变，但修复工具不接受“部分可读”对象，以免形成误导性补数计划。计划包含随机操作ID、服务实例/数据库身份、对象SHA、完整数据库快照摘要、解析摘要、版本与新增数量；不包含正文。输出文件私密且不能覆盖已有文件。

执行必须显式允许隔离写入，并声明其他写入者、维护任务已停。它重新验证源对象和计划，在一笔事务内锁定审计操作、处理状态、任务和详情，核对原快照，只插入缺失尾部并保存新增ID及完整前后快照。不会更改任务元数据、状态、旧详情ID、时间或存储对象。相同操作重试验证已发布快照后返回no-op，避免提交确认丢失时重复补数；其他计划若已过期则拒绝。

回滚不依赖OSS可用性。只有当前数据库仍与该次补数后完整快照一致，才删除本操作新增ID并更新审计状态。再次回滚核对原快照后no-op；发生任何字段变更、任务删除或后续写入时拒绝，不覆盖新数据。回滚仅恢复本次数据库详情，不回滚外部存储对象的其他变动。回滚不恢复数据库自增计数器；会留下未使用ID，这是正常行为。已回滚操作不能原计划再次apply，需重新dry-run。

快照保存在新增的审计表中，包含原始日志，应使用与业务日志相同的访问限制和备份策略；本轮不提供自动删除审计记录的任务。计划摘要不是数字签名；访问数据库的工具凭据和计划文件仍须受保护。

## 使用

源码入口为 `cmd/logan-reparse`，与HTTP服务入口独立，不给线上接口新增补数路由。正常Go服务启动不建表、不执行补数。显式在隔离库依次应用原表、001、002和003迁移后使用。

```sh
go build -tags stdjson -o /tmp/logan-reparse ./cmd/logan-reparse
# 使用正常LOGAN_MYSQL_DSN等私密配置；不把凭据写入命令历史。
export LOGAN_REPARSE_ISOLATED=YES
/tmp/logan-reparse --task-id 123 --plan /tmp/private-reparse-plan.json
/tmp/logan-reparse --action apply --task-id 123 --plan /tmp/private-reparse-plan.json --allow-isolated-writes --offline
/tmp/logan-reparse --action rollback --operation-id <dry-run-operation-id> --allow-isolated-writes --offline
```

配置采用现有 `LOGAN_MYSQL_DSN` 与 `LOGAN_OSS_*`；回滚不要求OSS/AES配置。源对象上限取 `LOGAN_HTTP_MAX_BODY_BYTES`，解析上限取 `LOGAN_NATIVE_QUERY_MAX_PLAIN_BYTES`、`LOGAN_NATIVE_QUERY_MAX_LINE_BYTES`、`LOGAN_NATIVE_QUERY_MAX_RECORDS`。完整数据库快照额外限制为明文上限4倍、最高256MiB；超限拒绝而不部分处理。命令总超时默认2分钟、最多1小时，可用 `--timeout` 调整。

计划文件要求绝对路径、当前用户所有、普通非符号链接文件、权限0600且不超过64KiB，未知JSON字段拒绝。dry-run不能覆盖已有计划。写操作的 `--offline` 是调用者声明，不是分布式停机证明，隔离演练会停止其他写入者。

当前实现阶段结果不代表真实云端或生产验收通过。

## 隔离实测（2026-10-16）

本轮使用独立VZ虚拟机的Docker29.9.1、linux/arm64、MySQL8.4.11及本地HTTP对象存储。当前源码编译的独立CLI绑定到上一轮已验证的Go基础镜像内运行；本轮没有重新构建HTTP服务镜像，也没有把CLI自动加入线上启动入口。测试库随机命名且无宿主业务端口。

| 验收项 | 实际结果 |
| --- | --- |
| 默认dry-run | 成功生成私密计划，完整业务表、审计表和对象摘要前后完全相同 |
| 追加补数 | 一条已完成前缀补为三条，两个相同尾部记录均保留；旧详情ID/全部字段、任务全部字段不变，status仍1 |
| 幂等与并发 | 相同计划再次执行返回noop、全快照不变；同操作并发及不同操作竞争均不重复插入 |
| 回滚 | 删除本操作新增记录，原业务数据和对象恢复为执行前状态；审计保留。再次回滚noop；人工改动后拒绝回滚且不再写入 |
| 计划与源校验 | 缺少写入授权、计划篡改、陈旧数据库快照、非前缀详情、对象内容改变或任务对象名改变均被拒绝 |
| 数据与资源保护 | CRC损坏、记录上限拒绝；完整快照还有字节/行数和SQL LIMIT保护，单元测试覆盖溢出配置 |
| 数据库事务失败 | 第二个尾部INSERT被触发器拒绝后，首个INSERT和审计也回滚，完整快照不变 |
| 数据库静默改写 | 触发器改内容、非严格SQL模式下70KB TEXT被截断，均由回读校验拒绝，业务和审计没有残留写入 |
| 提交前强杀 | 实际CLI容器在INSERT的SLEEP屏障进入后SIGKILL，exit137；等待数据库结束该连接后，完整快照不变。相同计划重试成功一次，再次执行noop |
| 回归与清理 | Go完整test/race/vet/build通过；62项工具测试、Harness检查及17项自测通过。测试容器/网络全部回收，临时VM停止删除，合成env回收 |

这些任务是明确种植的“Java形状”已完成前缀，不是本轮由Java服务生成。原Java未执行、未修改；上一阶段实际Java1条/Go3条的行为对照见 [容器SDK验收](container-sdk-validation.md)。本轮验证的是补数工具真实SQL事务与对象读取的一致性，不能据此声称真实云端或真实历史用户数据已验收。

本地证据（系统临时目录可能被清理）：

- `/private/tmp/logan-reparse-final-2/summary.json`：正式源码CLI完整验收退出0；同目录保存各场景完整前后表/对象快照和脱敏命令。`before-interrupted-apply.json`、`after-interrupted-apply.json`为真实SIGKILL前后对照。
- `/private/tmp/logan-reparse-cli-final`：本轮实测linux/arm64独立二进制，SHA256 `e859d39417c0c1a0782eb1754e3ab003dd2d4f003df5dcd81b4836d4f2013bad`。不是正式签名发布产物。
- `/private/tmp/logan-reparse-source-manifest.json`：18项源码/迁移/控制器摘要、实际构建参数、二进制摘要已复核，最终构建未使用诊断overlay。
- Go full：`/var/folders/6w/c5rms8nd1z10_pdghq0j31bm0000gn/T/logan-agent-s21mhmjt/summary.json`。工具：`/private/tmp/logan-reparse-tools-final-2.log`；Harness：`/private/tmp/logan-reparse-harness-final-2.log`。
- `/private/tmp/logan-reparse-cleanup.json`：归属清理通过、VM和socket不存在、合成配置已回收。

失败记录保留：控制器早期SQL注释拼接及只读临时路径配置、CLI占位AES长度错误均已定位修复；独立测试先失败后通过。诊断overlay仅用于定位，不计最终通过。宿主/虚拟机共享文件缓存曾让对象变更测试读到旧内容，最终改成存储容器内修改及同视图摘要后，正式CLI重跑通过。独立审查还补齐了快照/SQL行数预算、发布后防数据库转换校验、新增ID核对及SIGTERM清理保护，未放宽拒绝条件。

## 剩余门槛

真实OSS无凭据，未执行云端读取或任何云写入；未操作生产数据、原服务或生产入口。第十三阶段当时未覆盖提交已成功但客户端确认丢失的实际故障点；后续两类操作及真实Java/Go读取实测见 [确认丢失与读取验收](reparse-confirmation.md)。后续mysqld提交前后强杀重启及连接中断实测见 [数据库故障恢复](reparse-database-recovery.md)。任意网络分区、物理断电、大规模批量修复或真实用户历史对象仍未覆盖。此次CLI仅Native、单任务、严格前缀追加，未实现Web重解析、任意差异合并或自动修复非前缀数据。

允许正式环境使用前，仍需单独明确环境授权、受限账号、对象保留/版本策略、审计备份及停写流程。本阶段命令会拒绝非隔离库名，也不会替用户停止任何原Java或Go服务。
