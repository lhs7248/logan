# 大日志 MySQL 内存与磁盘预算复验

本轮只测Go，保留Java、原Web表字符集、接口、SDK/golden和原部署，不操作生产数据或切流。沿用最终7099b810镜像，不以改表、提高HTTP/解析上限或关闭提交落盘来取得通过。前轮1GiB OOM及2GiB宿主磁盘故障保留于[前轮报告](all20-large-load.md)。

## 原因对照

同一个MySQL8.4.11/2GiB/256MiB buffer pool配置，Go原生MySQL驱动逐个取得十个连接，执行8MiB操作后全部归还；分别保留10或2个空闲连接，再关闭整个池。每次采样使用相同的短连接SQL和cgroup采集，不混入业务性能统计。四次隔离实验均退出0；它们不是业务压测或泄漏认证。

| 操作 | 空闲10个：String / NET | 空闲2个：String / NET |
| --- | --- | --- |
| `SELECT REPEAT('x',8388608)` | 190176 / 180653 B | 58144 / 16423 B |
| 8MiB绑定参数 `SELECT ?` | 190176 / 83960276 B | 58144 / 16801909 B |
| utf8mb3 MEDIUMTEXT字段→utf8mb4连接，8MiB字段读取 | 335736240 / 180653 B | 67168432 / 16423 B |
| 字段和连接均utf8mb4的反向对照 | 191520 / 180653 B | 未执行，十连接反向对照已足以区分本路径 |

全部关闭后String降至约25–26KiB、NET约16KiB。现有Web表使用历史 `utf8`（utf8mb3），Go连接使用utf8mb4；第三组复现了前轮约32MiB/连接的String容量。MySQL [转换实现](https://raw.githubusercontent.com/mysql/mysql-server/mysql-8.4.11/sql-common/sql_string.cc) 按目标字符集最大字节数乘输入长度预留空间，量级为4×8MiB。[协议实现](https://raw.githubusercontent.com/mysql/mysql-server/mysql-8.4.11/sql/protocol_classic.cc) 选择字符集转换路径；本轮没有证明具体哪个String对象长期持有，也没有证明泄漏。

已确认两个放大因素：大参数网络缓冲随连接保留，以及字符集转换相关String容量随空闲连接数放大。P_S记录的是分配容量，不等于物理RSS，不能与RSS相加。关闭连接后RSS未立即完全下降，仍有分配器保留等影响；前轮固定开销、瞬时语句/参数副本、文件缓存和内核占用共同挤压1GiB限额，不能声称每个OOM字节均已归因。本轮新增anon/file/kernel分项和P_S采样进一步界定实际预算。

## 显式预算

| 组件 | 本轮隔离预算 |
| --- | --- |
| VM | 3CPU/4GiB；8GiB虚拟盘，实际guest根分区约6.8GiB |
| MySQL | 1CPU/2GiB硬限额且无额外swap；buffer pool256MiB，redo128MiB，max_connections16 |
| 单Go实例 | 1CPU/1GiB；GOMAXPROCS1/GOMEMLIMIT768MiB；实际显式 `LOGAN_DB_MAX_OPEN=8`、`LOGAN_DB_MAX_IDLE=4`；最大档HTTP等待失败，尚非稳定性合格配置 |
| 客户端/对象夹具 | 0.5CPU/1GiB，0.25CPU/512MiB；独立列明，不能当服务RSS |
| 磁盘提前拒绝 | 控制器启动宿主≥4GiB；运行采样宿主≥3GiB、guest≥1GiB；低于门槛直接失败保留证据 |

原应用默认20/10不变，当前预算通过显式环境参数设置。max_connections16包含观察/维护余量，不代表允许16个重型业务连接同时占满预算。生产多实例必须合并计算全部连接池、实际大字段分布及瞬时并发，再重新测量；本轮不承诺2GiB适用所有流量。

binlog保持开启，sync_binlog=1、innodb_flush_log_at_trx_commit=1；测试库使用64MiB轮转和60秒自动过期，观察SHOW BINARY LOGS、数据目录大小和df。自动过期依赖轮转，大事务也可超过轮转大小，因此不是硬磁盘配额。60秒策略只适用于无副本、可丢弃的合成库，绝不是生产PITR/复制保留方案。前轮host/guest耗尽的I/O错误有直接证据；历史binlog总量未保存，不能把全部磁盘增长断言为binlog。

## 执行与验收

```sh
python3 script/verify-all20-large-load.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary /private/tmp/<loader> --go-image <verified-image> --sdk-fixtures /private/tmp/<fresh-sdk> --out /private/tmp/<fresh-evidence> --work-dir /private/tmp/<fresh-work> --large-only --mysql-memory-mib 2048 --pool-max-open 8 --pool-max-idle 4 --bounded-mysql-profile
```

先加 `--diagnostic` 执行三档20秒短诊断；正式三档各220秒、平均每秒0.2工作流、每10秒突发2个。每档均调用完整20接口，Native内容1/8/32MiB，Web内容1/4/8MiB，逐条ID/长度/SHA、客户端账本、任务done及owner/lease/fence/revision/attempts、1205/1213和资源采样均需通过。重复解析/上传和合法协议超限拒绝独立验收，未在负载过程中人为制造失效owner。小SDK样本重新生成并用OpenSSL独立验证2048位RSA；大样本仍是合成协议，不是真机大日志。

磁盘恢复后，全部三档正式负载均已执行，各220秒、44工作流、实际并发峰值2；整体验收仍失败。未修改应用、原表、5秒brief等待或成功判定。

| 正式档（内容大小） | HTTP数/错误数 | HTTP P95/P99 ms | MySQL cgroup峰值 | 结论 |
| --- | --- | --- | --- | --- |
| Native1MiB/Web1MiB | 506 / 0 | 218.893 / 269.501 | 0.843GiB | 通过；11264条详情完整 |
| Native8MiB/Web4MiB | 792 / 0 | 693.396 / 1202.603 | 1.299GiB | 通过；67584条详情完整；前轮1GiB在此档OOM |
| Native32MiB/Web8MiB | 1102 / 10 | 725.793 / 5328.837 | 1.550GiB | 失败：10次Native brief HTTP500，未OOM |
| 最大档独立复核轮 | 550 / 22 | 5308.993 / 6044.712 | 1.619GiB | 仍失败：22次Native brief HTTP500；事后225280条详情完整 |

HTTP分位数包含失败请求和对象GET，未使用success-only分位数掩盖等待错误；失败会中止该工作流后续请求，因此不能把较少HTTP数理解为吞吐下降的单一比值。三档固定每秒0.2工作流，详情分批数量随样本变多，HTTP QPS不是服务容量上限。首轮132工作流中122成功/10失败，实际2400HTTP中10失败，零调度丢弃；复核轮44工作流中22成功/22失败。每档客户端调度220秒，宿主活动下界分别150.500、150.820、150.470秒；复核轮160.386秒。它们不是一小时或24小时长稳。

首轮最大档因HTTP失败，原控制器在最终逐条SQL校验前退出并清理容器；该轮完整哈希证据缺口保留。独立单档复核仅增加清理前的SQL核对和有界排空观察，HTTP失败依旧导致整体退出1，没有重试HTTP、提高超时或替换首轮结果。原始SQL返回的任务ID、明细ID、OCTET_LENGTH、SHA2与fixture/客户端账本由独立审计再次重算：前两档78848条，加上复核轮最大档225280条，共304128条一致。这是跨所列运行的完整性证据，不是同一轮全矩阵通过；审计明确 `audit_status=passed`、`overall_load_acceptance=failed`。

复核轮两端各22个job均done、owner空、lease0、fence/revision/attempts均1、无last_error且源摘要长度64；没有重复或缺失详情，没有任务重试。首轮及复核轮所有采样/末态1205、1213均0，相关容器OOM事件及故障末态OOMKilled均false。正常行锁等待仍有，不能写零锁等待。真实MySQL套件本轮从当前源码重新编译并执行通过，包括续租、失效owner/fence、并发Ensure/发布、501历史替换和强杀恢复；这与正常负载下的fence1证据分开，未在混合压力期间注入失效主。

8/4最大档20秒短诊断曾通过全20接口及重复上传/并发解析、合法协议超限无副作用探针；正式结果证明短诊断不足以代替稳定性窗口。Native同内容重传仍创建不同任务，对每任务的解析保持幂等，没有新增全局上传去重。

最大档两次220秒均未OOM，复核轮MySQL RSS峰值约972.6MiB、cgroup峰值1.619GiB；匿名内存采样峰值约941.1MiB、文件缓存约591.9MiB，分项读取时间不同，不能把各自峰值相加。2GiB是本次单实例、此负载下未OOM的已测限额，仍不保证长时收敛或生产余量；池8/4尚未通过查询稳定性，不能定为生产配置。失败请求约5.3秒与原Native brief 5秒等待相符，后台解析继续完成。复核末期指标中有22次Native处理调用落在5–10秒；调用指标还包含抢占未成功的空操作，不能把调用count当唯一任务数。剩余重点是分解最大档后台处理/存储与请求等待耗时，尚未完成根因定位或修复。

原4/2、8/2短诊断最大档失败保留；8/2受控单档20.11秒Connections增539，8/4短档增531（都含观测连接），不能宣称8/4消除了建连。宿主磁盘与其他构建压力前后也变化，尚不能将短档恢复完全归因于池参数。未继续盲目调整池或改变接口等待以追求通过。

前期磁盘不足的启动拒绝、Go缓存回收、十二个fixture无损归档与经tar.gz逐文件SHA核验的工具回收记录均保留。用户释放空间后，正式负载期间宿主最低约21.12GiB，guest最低约1.88GiB，均高于3/1GiB运行门槛；本轮未再因磁盘受阻。虚拟机恢复后使用 `root:lhs 0660` 的受限socket属组权限，没有采用所有用户可写权限。正式及复核容器/卷/验证网络均回收，专属VM停止，磁盘/镜像/config保留供后续性能定位；测试私钥在摘要验证后删除。

146项Python工具回归、Harness检查/17自测通过；应用104文件、原SDK、镜像/负载指纹保持，未重新运行Java。原始证据：`/private/tmp/logan-phase22-formal`（三档、整体failed）、`/private/tmp/logan-phase22-integrity-formal`（单档复核、整体failed）；独立审计在后者的 `independent-failed-matrix-audit.json`。资源/清理/工具与早期失败位于 `/private/tmp/phase22`；`formal-preflight.log`是已解除的历史磁盘拒绝，不代表当前状态。私有临时证据可能被系统回收。

真实云OSS无凭据，依赖内部对象字节夹具，未验证云签名鉴权/配额/延迟。未覆盖真实设备/浏览器、多实例吞吐、TLS性能、amd64、24小时稳定性或生产容量上限。原Java保留，本轮未运行Java对照，不执行生产切流。

后续阶段 23 已补齐分段计时、brief 等待修复和新的完整通过矩阵，见 [大日志延迟报告](native-large-latency.md)；不覆盖本报告记录的阶段 22 历史失败。
