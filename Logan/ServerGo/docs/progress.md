# Go 重写进度

已完成基础骨架，并推进移动端协议与OSS链路；Web协议、其余业务和生产任务仍按主计划执行。

## 已实现

- 官方 hz 0.9.7 生成的独立Go模块，固定Hertz/GORM/MySQL驱动依赖。
- 20个旧接口登记与33个初始契约用例；72份Java源码、SQL和配置文件指纹。
- 原Java Controller的14个确定性响应样本，5个原Java模型序列化样本。
- 可采集/比较隔离HTTP服务的契约工具，预检、禁止默认副作用、拒绝覆盖证据、拒绝不完整基线。
- DTO与GORM四表模型分离；null、毫秒时间与大整数序列化测试。
- 环境配置校验、可选MySQL接入、连接池、驱动超时、敏感错误处理，不执行自动迁移。
- 可启动Hertz、健康端点、构建/启动脚本、Dockerfile、配置和基线文档。
- 默认业务端点501；启用移动端后3个接口提供实现，readiness仍为503，不将占位处理视为兼容成功。

## 待补充的验证

- 原完整Java应用HTTP基线、MySQL/OSS真实集成、生产路径与时区确认。
- 真实Android/iOS/Web日志样本及各版本协议已知答案。
- Docker镜像构建（当前环境没有Docker）。
- 实际性能容量；当前没有QPS或内存收益结论。

## 后续顺序

补充真实SDK协议夹具和云端集成，继续移植Web协议与任务逻辑。业务完成时逐接口替换501，使用Java证据验收。全部功能与依赖就绪之前保持readiness失败。

## 本轮验证结果

- Go 全量测试、`-race`、`go vet` 和构建通过。
- Java隔离测试5项通过：原14个Controller响应与5个模型样本，加12个协议向量、9个元数据和9个过滤器HTTP样本。
- 实际启动Go二进制：ping/liveness=200、readiness=503、业务占位路由=501。
- 契约工具正确拒绝将501当作Java兼容结果；不完整采集不会生成可用运行索引。
- SIGTERM测试正常退出；原Java、前端及原Compose没有修改。

这些结果仅证明本阶段基础能力，不代表真实数据库连接、完整应用或性能验收已完成。

## 第二阶段：移动端协议与 OSS（2026-10-15）

已配置实现移动端上传、任务下载地址和OSS签名重定向3个接口；默认关闭时仍保持501。其余17个接口尚未迁移，readiness保持503。

本轮增加流式AES帧解密、有界gzip解压、官方OSS SDK V2接入、GORM任务写入/查询、协议配置与超时/并发/临时文件保护。保留原Java响应、CORS和元数据语义。详见 [移动端说明](native.md) 与 [配置](configuration.md)。

新增证据为12个原Java协议合成向量、9个元数据样本和9个含过滤器HTTP样本。覆盖官方SDK本机模拟、SQL事务替身、完整上传下载路由链路以及真实TCP慢上传、提前失败和连接复用。没有修改原Java服务，也未执行数据库迁移或生产部署。

数据库写入失败时保留已上传对象，避免误删提交结果不明时已被引用的对象；持久化对账/孤儿回收仍待后续实现。真实SDK样本、MySQL/OSS环境、全量功能和性能容量仍待验证。协议短时模糊测试5秒通过（13,132次执行），全量Go测试、race、vet与构建通过。


## 第三阶段：Web协议与任务解析（2026-10-15）

完成Web v0/v1与明细解码，10个Web接口、页面聚合/覆盖/下载，以及持久化任务、租约/fence、续租、版本/digest校验和事务发布。保留已解析页覆盖时不重建明细的旧行为。新增显式迁移只作用于内部表，未执行真实数据库迁移。

新增Java协议、明细、34个过滤器HTTP样本，以及多日期聚合/覆盖执行证据；Go包含协议、接口、SQL替身、资源上限、真实TCP上传解析、并发等待/取消与ID稳定回归。最新结果见下方验证记录。详见 [Web说明](web.md)。

启用移动端与Web后共13个接口有实现，另外7个移动端查询/解析接口仍501，readiness仍503。真实SDK/MySQL/OSS与前端联调、完整任务生命周期（含移动端解析）、容量和生产部署仍未验收。单页内存数组暂存和故障保护的兼容差异已在Web说明中单列。

### Web阶段验证记录

- Go全量测试、全量race、vet和构建通过；真实TCP含大页面固定长度/chunked上传、首次解析、连接复用与正文超限拒绝。
- Java独立测试共8项通过，包含16个Web协议、7个明细向量及34个过滤器HTTP样本；原Java源码指纹保持一致。
- 最终复核的“索引预算恰好用尽，后续任务无匹配时被误拒绝”已增加失败复现并修复，零剩余预算会额外探测一行。
- Web协议5秒fuzz通过（404388次执行）；数据库实际迁移、InnoDB多实例/进程崩溃恢复与真实SDK样本尚未验证。


## 第四阶段：剩余移动端查询与解析（2026-10-16）

补齐7个查询GET映射，接通OSS下载、有界解压/逐行解析/文件暂存、持久化native任务、租约/fence/版本校验、事务分批发布与重试。已解析任务复用原明细ID。保留原分页、排序、null/重复ID、关键词LIKE、内容格式与缺少endTime的异常边界；故障保护差异单列。

启用相应开关后全部20个旧映射均有实现，不再有尚未迁移的业务映射；默认关闭功能仍501。readiness维持503，真实SDK/依赖/容量及生产生命周期尚未验收。原Java/SDK/前端/原部署未修改。没有执行真实数据库迁移或切流。

新增47个Java查询场景（44个确定性响应、3个原未捕获异常）、23个逐行解析向量、20个内容Handler样本，以及SDK链路、真实TCP和任务/资源/取消测试。详细证据、启用方式与限制见 [移动端查询说明](native-queries.md)。本节取代此前“尚余7个接口”的阶段状态。

### 移动端查询阶段验证记录

- 全量测试、race、vet和构建通过，覆盖47场景响应对照、真实SDK/TCP、流式解析与任务取消。
- Java独立测试10项通过，无失败或错误；原Java源码指纹及原部署差异检查保持一致。
- 5秒移动端记录fuzz通过（98087次执行）。
- 独立复核重要问题已修复：标准JSON转换、持锁发布续租、大对象转换复杂度与取消。
- 未执行真实MySQL迁移、云端OSS/历史SDK联调、生产部署或容量验收。

仓库Harness最终验证：`verify --profile full`（make test/race/vet/build）与`verify --profile java`（make java-baseline）均退出0。
本机证据保存在临时目录 `logan-agent-uuv_h7fz/summary.json` 和 `logan-agent-8cfaeie7/summary.json`；原始日志与摘要同目录。这些结果仅证明所列验证，不代表生产就绪。

## 第五阶段：清理、监控和退出（2026-10-16）

新增显式迁移 002：维护租约、对象登记与数据库 OSS 命名空间绑定。实现 Native/Web 保留期清理、详情联动删除、历史孤立详情回收、已登记孤儿对象对账及持久化删除重试。上传先登记，任务与 referenced 状态事务提交，防止提交结果不明时误删对象。不同清理阶段独立预算，Native 错误或积压不阻塞 Web/outbox。未执行真实迁移或对象删除。

临时文件进入独占锁保护的进程会话；支持活动实例跳过及 SIGKILL 后回收。新增可选 /metrics：HTTP、解析耗时/积压、DB、OSS、Go/进程及回收指标；固定标签与脱敏请求日志。SIGTERM/SIGINT 停止接收后排空 HTTP/任务，预算耗尽取消并关闭连接，等待处理逻辑结束后再关闭数据库、OSS 和临时目录。端口绑定失败不启动解析/清理，启动失败返回非零退出码。

20 个旧映射与已有错误码保持原契约，原 Java/SDK/前端/部署未修改，readiness 仍 503。维护和指标默认关闭。未知归属的历史 OSS 对象与旧散落临时文件不自动删除；真实 MySQL/OSS、多实例故障和容量仍待隔离环境验收。配置、恢复与限制见 [运维说明](operations.md)。

### 清理与运维阶段验证记录

- 最终 Harness `verify --profile full`（make test/race/vet/build）全部通过，退出 0；证据：系统临时目录 `logan-agent-85110lgg/summary.json`，各步原始日志同目录。
- Java `verify --profile java` 通过，退出 0；证据：系统临时目录 `logan-agent-_imnwmfw/summary.json`。原 Java 指纹检查保持有效。
- 新增 SQLmock 覆盖命名空间冲突、清理/解析协调、事务回滚、引用保护、删除确认丢失与租约恢复；分阶段预算和 Native 坏数据不阻塞 Web/outbox 有回归。
- 实际子进程验证活动临时锁保护及 SIGKILL 后回收；实际 TCP 验证排空响应、强制取消后等待处理结束、端口绑定失败、预先取消启动与指标端点兼容。Linux amd64 包交叉编译通过；Docker 镜像与真实依赖未验证。
- 一次完整验收暴露旧临时文件断言与后台重试的测试竞态：已用确定性用例确认活动重试会创建新文件，改为排空后检查，配对回归通过 30 次 race 重复；上述最终完整验收通过后才记为完成。

## 第六阶段：真实依赖与压测工具（2026-10-16）

补齐带 integration 标签的 MySQL/OSS 套件、独立执行与证据汇总脚本、可选隔离 MySQL Compose，以及标准库实现的限速压测 CLI 和合成 fixture 生成器。未改动业务接口、原 Java/SDK/前端或原部署。入口见 [集成验证](integration.md) 与 [容量说明](capacity.md)。

| 项目 | 本轮结果 | 证据/边界 |
| --- | --- | --- |
| 真实 MySQL | 8.4.11、macOS arm64 实测通过 | 新建随机测试库加载原四表及迁移 001/002；Native/Web 发布、引用/保留期、outbox、竞争租约与 fence |
| 多进程恢复 | 实测通过 | 真实 SIGKILL 对象领取进程和 Native 解析进程；替代进程等待数据库租约并恢复，验证无部分发布、详情 ID 稳定 |
| HTTP 健康冒烟 | 200 QPS、10 秒，2000/2000 成功，零错误/丢弃 | p95 2.814ms、p99 5.207ms；本机 Go 服务、数据库功能关闭，无固定资源隔离，仅证明 HTTP 冒烟 |
| 低 QPS/错误对照 | 实测通过 | 1 QPS/1秒恰好发送1次；关闭业务接口的5次501全部计为失败，工具退出1 |
| 真实 OSS | blocked | 缺少测试端点、桶、凭据与写入开关；套件编译/vet通过，未调用真实云存储 |
| 普通 Go 验收 | full 全部通过 | make test/race/vet/build，原兼容 golden 与源码指纹检查保持有效 |
| 其余环境/容量 | 未执行 | MySQL5.7/8.0、Linux Docker、两套完整 HTTP 服务、真实 OSS 全链路、Web 压测、Java/Go同条件容量与长期稳态 |

真实 MySQL 首轮摘要：系统临时目录 `logan-integration-p8un7kn1/summary.json`；扩展 Web 场景后的完整 JSON 日志与命令/退出码：`logan-mysql-it-real-expanded.log`（退出0，36.483秒）。7个测试/辅助入口均通过，其中包含6项业务/故障场景。临时 MySQL 已关闭，随机库清理已核对，生成的运行凭据文件已移除。

最终 Go full 证据：`logan-agent-yion0je_/summary.json`（退出0）。OSS blocked 证据：`logan-integration-x1k4svwd/summary.json`（退出2）。OSS 套件独立编译、integration包 vet 和 Python 证据分类/脱敏测试通过；编译不视为云端运行通过。另一次未放开本机TCP的 MySQL 执行受沙箱限制，随后在允许回环端口的隔离测试执行中通过，不记录为数据库产品故障。

最终负载结果：临时目录文件 `logan-health-load-final.json`、`logan-low-qps-load-final.json`、`logan-disabled-business-load-final.json`。独立审查发现并修复空 brief 被误算成功、窗口末尾少计请求和正文超时少计问题，补齐对应回归。没有业务容量或性能提升比例结论，readiness 仍503。

## 第七阶段：部署候选、告警与切换回滚（2026-10-16）

补齐独立 Go 候选 Compose 与固定镜像发布 overlay，保留原 Java/前端/原Compose。Dockerfile 非root、只读根及专属tmpfs；Compose含资源/日志/退出预算、私密配置挂载、回环端口、可选监控profile。候选worker=1、1CPU/1GiB/512MiB临时目录是未冻结的起点，不是业务容量。release overlay移除build，要求独立确认的镜像digest；没有构建、拉取或启动容器。

新增11条告警和14个合成场景，覆盖采集失败、指标缺失、HTTP错误/延迟、DB池、解析积压/失败、维护失败和OSS错误，包含触发/恢复及readiness503等负例。Alertmanager默认blackhole，没有外部接收地址，未投递外部通知。现有指标缺业务HTTP200错误码、队列年龄、维护最后成功时间和cgroup/磁盘可用量，未宣称对应告警已覆盖。

[发布手册](release-runbook.md)明确：当前readiness503阻断生产；短维护窗口整数据域切换，Java与Go不混合消费；先封全部入口（GET也可能写）、排空并停止旧写入者，再启用新写入者。回滚先停止全部Go再恢复Java，保留内部表/对象登记，不自动恢复快照丢弃新日志。原Java清理cron无现成关闭变量，不能只改网关就与Go并行运行。网关片段仅.example，没有include/reload。

| 验证项 | 本轮结果 |
| --- | --- |
| Compose候选/监控/release叠加 | 官方独立Compose5.6.0离线渲染与契约检查通过；固定镜像形态、移除build、端口/权限/挂载/资源均检查；未联系Docker Engine |
| 告警配置与规则 | promtool3.15.0语法、规则及14场景通过；amtool0.34.1配置通过；官方工具下载校验和一致 |
| 本机原生监控链路 | 真实Go指标被Prometheus采集，测试专用合成告警进入本机Alertmanager；readiness仍503，DB/OSS/业务开关关闭，全部自建进程已停止 |
| 脚本故障回归 | 四项Python测试通过；SIGTERM子进程残留先失败复现后修复，确认全部回收；优化模式下部署危险配置仍拒绝 |
| Go与构建 | Harness full（test/race/vet/build）通过；CGO关闭的Linux amd64交叉编译通过；原Java指纹与接口golden检查有效 |
| 未执行 | Docker镜像构建/拉取/扫描、Linux容器启动/挂载权限/OOM/网络、网关语法与reload、真实外部通知、生产切流及往返回滚；真实OSS和完整容量仍沿用上一阶段限制 |

最终离线证据：系统临时目录 `logan-deployment-acfkt3jw/summary.json`，8项检查通过。最终原生链路证据：`logan-monitoring-smoke-piotq_tn/summary.json`，5项检查通过且自建进程全部退出。Go full证据：`logan-agent-0rbdk3jj/summary.json`，退出0。脚本入口见发布手册；工具缺失返回blocked，离线退出0不等于生产验收。

没有执行生产迁移、对象删除、Java停机、网关变更或通知投递。`/readyz`继续503，原业务接口和Java部署保持原样。

## 第八阶段：实际镜像与隔离容器验收（2026-10-16）

新增[容器验收入口](container-validation.md)、实际 Dockerfile 构建/运行检查脚本、隔离往返演练及故障回归。只修改 ServerGo 和外部计划记录；原 Java、SDK、前端、原 Compose/nginx 及接口契约不变。没有生产切流或真实依赖写入，readiness 继续精确返回 503。

本机原先没有 Docker Engine。本轮从官方校验和验证的 Lima 2.2.1 创建临时 VZ VM（2 CPU、3GiB 内存、12GiB 虚拟磁盘），安装 Linux Docker Engine 29.9.1/cgroup v2，使用临时 Docker CLI 29.7.0 和独立 socket；没有设置全局 Docker context。实际平台为 **linux/arm64**，不是 amd64 容器实测。

| 项目 | 本轮实测结果与边界 |
| --- | --- |
| 实际 Dockerfile 构建 | 通过；Go1.27.0-alpine 构建、Alpine3.22.1 运行，CGO关闭/stdjson；记录基础镜像和本地镜像ID。未推送、签名或扫描 |
| 基础容器验收 | 14项通过：UID10001、0400合成文件、只读根/只读bind、capability清空、NoNewPrivs、实际cgroup1GiB/1CPU/128PID、512MiB tmpfs、存活/指标及精确503 |
| 退出与重启 | 两次SIGTERM均在90秒内退出0；重启恢复存活，重启前留下的tmpfs标记确认消失。业务开关关闭，不能证明活跃HTTP/worker在容器内排空 |
| 实际候选Compose | 随机项目实际启动/停止并清理；init、只读根、内存/CPU/PID和nofile4096通过；Linux VM内0700目录/0400合成文件归UID10001、只读bind可读不可写。不是macOS实际私钥或有效RSA/OSS验证 |
| 小范围资源故障 | 独立1MiB tmpfs填充返回ENOSPC且探针正常结束；独立64MiB Python容器分配128MiB后exit137、Docker确认OOMKilled=true。不是Go业务OOM恢复或512MiB生产临时目录容量测试 |
| 隔离往返演练 | 实际Go镜像 + Python Java模拟器 + 合成网关；入口关闭、旧端停止后新端启动、回滚顺序及路径/重复查询参数通过，含UTC时间记录；不发布宿主端口。Go阶段仅诊断ping开放，业务持续503 |
| 脚本和兼容回归 | tools16项、rehearsal15项通过；覆盖优化模式、socket别名、精确503、中断、归属与清理失败。Harness check、自测17项、full的test/race/vet/build通过；Java指纹/golden保持有效 |
| 环境清理 | 所有测试容器/专用网络/Compose项目已回收，临时VM已停止并删除。仅保留仓库外镜像归档和本地证据，没有操作原Java或生产入口 |

最终证据（系统临时目录，可能被系统清理）：

- 镜像与14项基础容器检查：`logan-container-evidence-actual-4/summary.json`，退出0，各Docker命令与输出同目录。
- 候选Compose：`logan-compose-runtime-evidence/summary.json`，退出0，5项检查通过；各命令含实际挂载与退出证据。
- 往返拓扑：`logan-rehearsal-evidence-actual-2.md`，PASS，原Java模拟范围明确写入报告。
- 有界资源故障：`logan-resource-fault-evidence-2/summary.json`及同目录原始命令，执行退出0。
- 最终脚本日志：`logan-container-tools-tests-final.log`（16项）、`logan-rehearsal-tools-tests-final.log`（15项），均退出0。
- Go full：`logan-agent-cv67b6gz/summary.json`，退出0。清理：`logan-container-runtime-cleanup.json`，容器/网络余量0、`vm_deleted=true`。

保留的本地测试镜像归档为 `logan-servergo-linux-arm64-image.tar.gz`（约17MB，0600），镜像manifest ID为 `sha256:e4e1a3d8f6cd21ec1201639b0223614ff9d3911171d79c9bbe44a6ca48d2c13a`，归档SHA256为 `6a4ee627174f91fb1877c3b0fce8db8b8987df4ba234bb165e2fcd24778ae1a0`。OCI manifest与每个blob散列已核对；该归档不是正式发布产物，尚无签名、扫描或组织批准。

保留失败证据：首轮registry EOF、第二轮基础镜像层下载超时；第三轮依赖源不可达后主动中断（退出130），随后仅为临时daemon/构建阶段配置代理，第四轮通过，最终镜像环境没有HTTP_PROXY/HTTPS_PROXY。首轮演练因测试容器无可用发布端口失败，修正为容器内部HTTP探针后通过。首轮空间探针将错误写入已满文件系统而断言失败，改为捕获外部stderr后重试通过。没有删除失败记录或把失败计为兼容通过。

**仍未验证**：真实Java/Go共享数据往返、真实OSS及Web私钥、真实nginx配置/reload、容器内活跃业务排空/SIGKILL接管、业务负载下OOM/空间不足的恢复与数据一致性、CPU压力/PID耗尽、容器化监控全链路/外部通知、amd64镜像运行、真实SDK全链路与长期容量。生产就绪门槛继续阻断；此阶段完成基础容器验收，不等于整项重写已经可以切流。

## 第九阶段：活跃排空、强杀恢复与真实往返（2026-10-16）

新增[故障与兼容演练](recovery-rehearsal.md)、runtimefault 测试二进制、真实容器控制器、Java/Go 数据层往返及实际 Tomcat/Go HTTP 读接口比较。只修改 ServerGo 和计划记录；原 Java 源码/配置、SDK、前端、原部署、golden 与指纹不变。业务实现和生产 readiness 没有放宽，没有生产切流、迁移、对象删除或原 Java 停机。

本轮重新创建可丢弃 VZ VM（3CPU/4GiB/18GiB），Docker29.9.1/cgroupv2、linux/arm64，使用真实 `mysql:8.4.11`。随机库和进程均在独立内部网络，不发布宿主业务端口。Native 下载使用明确的本地 gzip/屏障夹具，不是云 OSS；Web 运行实际解析与发布代码。

| 验证项 | 实测结果和精确范围 |
| --- | --- |
| 活跃 HTTP 排空 | 真实 RunContext/Hertz 中进入慢请求后 Docker SIGTERM；进程等待，释放后客户端收到完整正文，容器退出0。慢路由为测试夹具，不冒充上传流量 |
| Native/Web 活跃任务排空 | 持久化领取后设置屏障，SIGTERM后仍运行，释放后正常发布，准确内容/一条详情，退出0 |
| Native/Web SIGKILL | 真实容器exit137；领取后发布前零详情、running/fence1/attempts1；新容器等待实际30秒DB租约，完成后done/fence2/attempts2/owner空/一条详情；重复运行保持详情ID |
| 真实 Java→Go→Java 数据 | 原Java DTO/服务/MyBatis XML创建已解析和待解析Native/Web数据；Go保留旧ID/内容、解析待处理并产生新数据；原Java回读核对通过。Java两阶段各实际执行JUnit1项，Go显式PASS，均不是skip |
| 实际 HTTP 往返 | 实际Tomcat9原Java WAR→生产Go镜像→Tomcat回滚；Java已解析及Go新增两套数据，各8个读接口，三阶段共48响应通过。HTTP/code200、JSON、稳定字段、ID/内容/索引一致；索引必须含预期ID |
| 切换顺序与退出 | 内部测试入口不对外开放，旧端停止后才启动新端，完整时间账本含退出码。Go退出0，Java按0/143正常停止，不接受OOM或强制137；readiness精确503，OSS夹具存活且零对象请求 |
| 回归与清理 | tools37项+rehearsal15项通过；Go full通过；Java baseline11项中10通过、1个新集成入口按设计跳过，真实集成另行实测通过。所有自建容器/网络/随机库随容器回收，临时WAR/RSA/env回收，VM停止删除 |

Java测试工具链使用现有 legacy-java POM 编译原始源码（现代编译插件/Lombok），原 mapper XML 按字节复制。实际运行 JRE17，MyBatis3.5.0需测试进程打开 java.util 模块；没有重建或验证原JRE8 Docker镜像。临时WAR仅替换副本的DB/OSS配置，原日志/部署写目录采用专属tmpfs。原Web表utf8字符集保持不变；Web中文/BMP夹具与Native utf8mb4 emoji分别验证，不修改表来绕过原限制。

最终证据位于系统临时目录，可能被系统清理：

- 容器故障：`logan-runtime-recovery-actual-2/summary.json`，退出0，Linux Engine/5个故障场景/归属清理均通过；每个信号、退出和SQL快照有原始命令记录。
- 数据层单独往返：`logan-data-rollback-actual-3/summary.json`，退出0。
- 最终实际HTTP往返：`logan-data-rollback-http-4/summary.json`，退出0；`http-roundtrip.json`、三阶段HTTP JSON、`java-seed.json`、`go-produced.json`、`java-recheck.json`、完整 `event-ledger.json` 同目录。
- 脚本回归：`logan-phase9-tools-final.log`（37项）、`logan-phase9-rehearsal-final.log`（15项），退出0。
- Go full：`logan-agent-dq3j7c94/summary.json`，退出0；Java baseline：`logan-agent-vpdp4o0u/summary.json`，退出0，原golden未更新。
- 清理：`logan-phase9-cleanup.json`，确认测试容器/网络余量0、敏感测试夹具删除、VM删除。

失败记录保留：首轮任务夹具SQL与行注释拼接导致初始化失败；Java初轮遇JRE17/旧MyBatis模块访问异常，添加测试运行参数后通过；HTTP首轮遇超过DNS63字符的测试容器名及原Java可写路径不足，缩短名称并增加窄挂载后通过。独立审查同时补齐精确Web协议/内容断言、拒绝skip、失败前采集日志、索引非空ID证据、完整顺序账本与请求总体预算。没有将这些失败、占位501或readiness503计作兼容成功。

**仍受限制/未执行**：真实OSS上传下载/签名及SDK/RSA协议全链路、全部20接口端到端认证、原JRE8镜像、真实nginx reload/生产入口切换、备份还原与生产数据对账；强杀点限于领取后发布前，未覆盖发布事务中途/提交确认丢失/OSS进行中/维护任务，也未证明仍存活旧发布者的fence拒绝；业务OOM/空间耗尽恢复、amd64运行及长期容量仍待后续。当前证明合成数据和所列读接口可以隔离往返，不代表已获得生产切流资格。

## 第十阶段：全20接口与真实SDK验收（2026-10-16）

实际Java/Tomcat、Go镜像、MySQL8.4.11及本地OSS字节夹具完成合成协议全20接口对照，protocol-3退出0。真实WebSDK加密BMP产物在显式旧RSA配置下Web10接口通过；真实CLogan在原Java与Go均丢失目标日志，sdk-2整体退出1，失败覆盖不计成功。默认Go拒绝参考1023位RSA，未放宽生产配置。

真实OSS新增匿名/篡改/过期签名拒绝、私有桶条件及私密配置入口；用户确认没有凭据，仅编译/vet和blocked保护通过，未执行云写入。浏览器/设备和云端SDK全链路未认证。53项脚本回归、Go full和Java基线通过，不代表显式SDK失败通过。原Java/SDK/前端/部署/golden未改，readiness503，无生产切流。证据和范围见ServerGo/docs/e2e-acceptance.md。

Harness检查、17项自测通过；本轮容器/网络余量0，专属VM已停止删除，清理记录 `/private/tmp/logan-phase10-cleanup.json`。

## 第十一阶段：Go移动端SDK日志恢复（2026-10-16）

保留CBC帧完整明文，并在完整gzip成员之间有界识别旧零/PKCS#7填充。实际CLogan样本从只剩内部头恢复为精确两条；新增单字节短读、跨帧/连续gzip、64KiB大帧、全部padding长度、历史已存对象及损坏/资源边界回归。原Java和golden未改，2个解密字节差异和额外记录恢复显式记录；补齐gzip中间片段末尾真实0x01反例，避免误删。实际SDK本地上传/OSS SDK请求/解析/详情查询通过；重新生成SDK+独立OpenSSL及显式Go SDK测试通过。详情和证据见 [SDK恢复修复](native-sdk-recovery.md)。旧已完成任务不会自动补数；真实云端、容器20接口、真机和生产重解析本轮未执行，readiness503，无生产切流。

最终Go full：`logan-agent-mmjm1aa2/summary.json`（系统临时目录），test/race/vet/build通过，Harness检查通过；独立复核的真实0x01片段误删问题已解决，复核无新增问题。

## 第十二阶段：修复镜像及容器SDK/历史对象复验（2026-10-16）

实际Dockerfile重建新arm64镜像 `sha256:c20df6288ab67bb2208cce7f94b5a29a6c583c22e8427664fac0ebe204a898d4`，14项基础容器检查通过。实际Java/Tomcat9/JRE17、Go新镜像、MySQL8.4.11及本地OSS字节夹具中，协议模式最终Java20/Go20严格比较通过；本轮新生成真实SDK产物模式Go20全部通过，Java14正向通过、移动端brief暴露已知首成员日志缺失，后续覆盖不计成功，严格Java/Go一致仍失败。

两个历史解密对象（零/PKCS填充）同对象各3记录，Java基线副本各只恢复1条，独立Go待解析行各恢复3条；Java已完成部分任务/详情完整行前后不变，未重置任务或自动补数。下载逐字节匹配独立CBC预期，并完整核对语义；修正原Java独立填充探测预期，原样本及golden不改。修复测试MySQL bootstrap socket提前就绪竞态，仅TCP正式服务就绪后执行建库。59项脚本回归、Go full、Java基线、Harness检查及17项自测通过。详见 [容器SDK验收](container-sdk-validation.md)。

真实OSS仍因用户无凭据blocked；签名下载只在本地夹具验证生成/字节传输，不验证云端签名。Web旧1023位RSA显式测试选项、BMP范围、设备/浏览器/JRE8/amd64/新一轮活跃故障及长期容量限制保留。原Java/SDK/部署保留，readiness503，无生产切流。

最终清理：测试容器/专用网络/专用卷余量0，临时WAR/env/RSA副本无残留，专用VM已停止并删除；保留本地镜像归档及证据。见 `/private/tmp/logan-phase12-cleanup.json`。Git跟踪的原Java/SDK/部署文件无修改，独立最终复核未发现重要问题。

## 第十三阶段：已完成任务受控重解析与补数（2026-10-16）

新增独立 `cmd/logan-reparse`、审计迁移003与严格修复解析，默认dry-run、单个明确Native任务、隔离库白名单。完整前缀才能追加；保留旧详情ID/字段和任务status1，原对象不改。计划绑定对象/数据库/快照/版本；事务发布后回读拒绝截断/转换；相同操作幂等，回滚仅在完整后快照不变时删除本次新增ID。未接入HTTP入口/启动迁移/自动重解析。

正式CLI在隔离MySQL8.4.11和本地OSS字节夹具完成默认只读、重复与并发、陈旧/篡改/源变更/CRC/资源拒绝、触发器第二次INSERT失败回滚、非严格70KB TEXT转换拒绝、真实提交前SIGKILL恢复及同计划重试、完整业务回滚验证，最终退出0。seed为明确Java形状合成前缀，不声称本轮执行Java或真实云/用户数据。Go full、62项工具测试、Harness检查及17项自测通过；VM/容器/网络/合成配置清理完成。详见 [补数工具与证据](native-reparse.md)。

原Java/SDK/原部署不改，未操作生产数据、迁移、存储对象或切流；readiness保持503。真实云OSS无凭据、提交成功但确认丢失切点、生产数据及大规模修复仍未验证。

## 第十四阶段：提交确认丢失及实际读取兼容（2026-10-16）

新增MySQL协议测试代理扣留真实COMMIT成功OK；补数和回滚分别用独立连接见证业务/审计已提交，再杀死无成功输出的客户端exit137，原操作新进程重试noop、全快照不变。原Java WAR/Tomcat9/JRE17与已核验Go镜像在补数前1条/补数后3条/回滚后1条三个状态串行读取，完整核对62端点响应及6对象GET，原ID保留、删除ID2001/2002返回null。逐进程确认目标任务20及其详情全部字段未变，不宣称处理任务表无写入。

最终 `logan-reparse-ack-3/summary.json` 退出0；Go full、原Java基线、81项工具回归及Harness17自测通过，独立复核无阻断。Java/Go业务源码、原配置和SDK不改，无生产数据或切流，readiness503。真实云OSS、真实用户历史数据、数据库崩溃/断电/TLS及全部20接口本轮重验仍不在范围。复验命令、精确故障点和限制见 [故障与读取验收](reparse-confirmation.md)。

本轮最终清理记录 `logan-ack-cleanup.json` 确认测试容器/网络/卷余量0、临时VM与socket删除、env/WAR/RSA副本无残留；保留证据和计划。

## 第十五阶段：MySQL崩溃重启与连接中断恢复（2026-10-16）

新增独立数据库故障控制器及8项单元测试，复用现有CLI、协议代理和原Java/Go读取探针。补数/回滚各覆盖提交前与COMMIT成功OK扣留后的mysqld SIGKILL同持久卷重启，以及提交前KILL CONNECTION和提交后代理断连，共8个真实故障场景。独立事务屏障和提交见证、恢复前后全表/审计/对象快照、审计before/after JSON摘要及完整业务行/append_ids一致性通过；未提交操作重试真正执行，已提交操作重试noop，所有重复重试noop且完整快照不变。

最终 `logan-reparse-db-3/summary.json` 退出0，四次MySQL exit137非OOM、同容器/卷/UUID/刷盘配置；生命周期核对连接场景无额外重启。八个恢复后状态的实际原Java/Tomcat9/JRE17与Go共176端点响应、16对象下载比较通过，旧ID和重复尾记录保留，回滚已删ID返回null；读取前后目标业务行不变。Go full、Java基线、89工具回归、Harness检查及17自测通过。子任务执行遇用量限制，主执行者接手并复核，不声称独立代理评审完成。

只改ServerGo验收工具/测试/文档及计划，原Java、Go业务核心、SDK、golden和部署不改；使用既有已核验Go镜像和源码摘要一致CLI，没有生产数据操作或切流，readiness503。真实云OSS无凭据、物理断电/磁盘故障/HA/TLS/用户数据/大规模补数/全部20接口本轮重验仍未覆盖。测试连接使用明确90秒读取预算，不修改生产默认超时。细节、首两轮失败及精确证据见 [数据库故障恢复验收](reparse-database-recovery.md)。最终容器/网络/卷余量0，临时配置/WAR/RSA无残留，专用VM和socket已删除；证据保留。

## 第十六阶段：MySQL TLS与GTID主从提升（2026-10-16）

新增证书校验、目标会话TLS观测与GTID复制提升控制器，扩展真实Java/Go读取探针的TLS参数。隔离MySQL强制安全传输，明文/错误CA/错误DNS名三负例零写入；同UUID的TLS连接中断下apply/rollback原操作重试及重复noop、完整业务/审计/对象快照通过。两个真实节点TLS复制追平后，同时暂停任务30补数与任务40回滚事务，再强杀旧主exit137并保持停止，提升新UUID副本，完整业务库及审计快照与已确认切点相等。

原计划/审计绑定UUID的保护保留：新主对3个旧apply计划和2个旧rollback操作均安全拒绝且全快照不变，明确不支持跨UUID原操作自动恢复。原未提交任务在新主重新dry-run后补数/回滚、审计JSON摘要、物理ID及重复全表快照通过。七状态原Java/Go真实TLS读取共150响应、14对象下载，14运行时均观测真实reader会话；旧字段/ID、重复尾记录和回滚删除ID核对通过。Go full、原Java基线、99工具回归、Harness检查及17自测通过，独立复核无新增阻断。

最终 `logan-reparse-tls-ha-5/summary.json` 退出0；前四次失败和两次诊断保留，未计主从通过。屏障根因是触发器内I_S执行身份显示root，最终用P_S认证repair关联同连接的真实事务与TLS；环境去重是独立稳健性改进。原Java/Go业务代码、SDK、部署、golden及readiness503不变，无生产数据操作或切流。真实云OSS无凭据、自动HA/任意RPO0/脑裂/物理断电/证书轮换/mTLS及全部20接口本轮重验仍未覆盖。详见 [TLS与主从提升验收](reparse-tls-failover.md)。容器、卷和专用网络余量0，14目录临时配置/密钥/WAR无残留，专用VM和socket已删除，证据保留。

## 第十七阶段：旧补数操作受控身份迁移（2026-10-17）

实现源端只读identity-export、目标identity-dry-run与显式identity-migrate，新增手工004授权表。按原操作/指纹/源目标/可信见证digest追加授权，不改旧计划或原audit.database_identity；源GTID前后一致，目标GTID子集、完整业务和原审计档案/新增ID匹配，授权写后回读并复核目标数据。正常UUID仍不依赖004，跨UUID在事务内重查实际身份与有效授权。已有源审计的非法状态回退、归档/物理ID损坏或授权变形均拒绝；不取消对象SHA、严格前缀、快照冲突和回滚删除边界。

最终正式CLI第二轮 `logan-reparse-identity-2/summary.json` 退出0：真实TLS/GTID提升、旧主停止后，三类旧操作经源见证与授权恢复/回滚成功；原操作ID、原计划字节、原审计源identity及档案保留。13类迁移拒绝、3个只读预检、授权字段/目标任务触发器改写整事务回滚、授权INSERT期间新主exit137同卷恢复、相同见证重试/并发收敛/状态推进后重复noop通过。最终3条授权及见证digest、原审计档案SHA独立重算一致。10状态原Java/Go实际TLS读取216响应、20对象下载，旧字段/ID、重复尾与删除ID空值核对通过，readiness503不变。

Go full test/race/vet/build、原Java基线、102工具回归、Harness检查及17自测通过，独立安全复核无阻断。第一轮中间二进制也通过恢复场景，最终重建补强写后复核并完整第二轮，不以旧结果替代最终源码。正式CLI SHA256 `0794a0f99445d2749a279ca5d7c3da175fa216d1c258c8e456bfcaf34e66406c`，没有overlay。HTTP镜像仍为既有已核验版本，原Java/SDK/部署/golden未改，无生产操作或切流。

精确使用方法、canonical见证digest语义、TDD与完整证据见 [受控身份迁移](reparse-identity-migration.md)。这是需预先可信源见证、明确源目标和外部fencing的单操作/单跳恢复；不支持无见证追认、多跳授权链，不提供跨复制分叉全局一次性消费或自动HA/自动fencing，也不防恶意DBA协调伪造。真实云OSS仍无凭据；生产用户数据/主从脑裂/物理断电/全部20接口本轮重验等门槛保留。最终容器/卷/专用网络余量0，4目录临时env/WAR/PEM/P12/KEY无残留，专用VM及socket删除；保留私有计划、见证、报告和CLI/镜像归档。

## 第十八阶段：身份迁移后的全20接口与真实SDK回归（2026-10-17）

重新实际构建当前Dockerfile镜像 `sha256:0e65f268ee4d77dba63498475db724c86bad97fb65cf5d549ea67422a30a5b9d`，14基础容器检查通过。正式CLI、真实CLogan/WebSDK产物重新生成，独立OpenSSL核对Native2条/Web1条。隔离MySQL8.4.11真实TLS/GTID提升后，在身份授权、旧30补数、原30/40/50全部回滚三个边界，各串行运行protocol和SDK模式。

最终第二轮退出0：六组Go均20接口通过；三个protocol组Java也各20且严格一致；三个SDK组Java均14项正向、严格失败仅为已声明Native多成员日志缺失，未冒充Java20通过。每组原任务/详情/完整审计/授权/历史对象前后快照一致。十个旧数据状态实际Java/Go读取216响应、20下载通过，六组all20额外222个正向路由、12次reader TLS观察通过。源见证/授权digest、原审计档案SHA、旧计划SHA独立复核，旧任务字段/旧ID保持，仅原40已追加的4001/4002按回滚删除，原30本轮新增尾按本操作ID撤回，原audit身份不改写。13负例、授权事务崩溃恢复和重复/并发/跨状态幂等再次通过。

修复验收工具的准备失败私钥/WAR清理和固定对象GET计数问题；第一轮因错误计数失败保留，修正后在全新库完整重跑。要求每个已经通过精确内容校验的下载都到达对象夹具，protocol仍4GET，精确已声明SDK Java缺失场景3GET，未知差异或Go缺失不接受。Go full、Java基线、111工具测试、Harness检查及17自测通过，独立评审及实际证据复核无阻断。

完整矩阵、命令、已知差异和证据见 [身份迁移后完整回归](identity-full-regression.md)。真实OSS无凭据、真实浏览器/设备、JRE8/amd64、生产容量/自动HA等仍未验证，SDK弱RSA兼容只通过显式诊断参数启用，默认readiness503保持。原Java/SDK/golden/部署不变，没有生产数据或切流；最终专属容器/卷/网络与敏感副本残留0，VM和socket删除，镜像归档及脱敏证据保留。

## 第十九阶段：Go并发与有效一小时持续测量（2026-10-17）

新增有界开放负载工具、隔离控制器、每分钟/每路由分位数和365组cgroup/SQL/Go指标采样。原CLogan/WebSDK产物重新生成并独立解密核对，严格检查返回ID和全部内容；只允许固定合成身份及隔离目标，network-none预检先于业务写入。用户明确停止Java后已终止并清理，只执行Go专项。首轮Java因虚拟机内Go单调时钟与宿主不一致安全拒绝；最终Go调度3660s，宿主采样跨度减20s排空的活动下界3620.561s，实际有效一小时完成，不把时长完成混称验收通过。

Go持续段36600工作流、146370次HTTP，客户端约39.99 HTTP QPS，P95/P99 14.78/65.12ms；10次web-upload业务错误（HTTP严格错误率0.006832%）、0丢弃。独立最终SQL/完整ledger核对成功上传36590任务（Native18300/Web18290）与54890条明细一致、全部解析完成；Native4任务重试、最终job无last_error。捕获任务扫描范围UPDATE与完成事务间InnoDB死锁及processing scan failed，Web上传失败的具体底层原因因错误上下文不足尚未证实。**零错误/零重试稳定性门槛未通过，当前不能据此放行。**

读取50/200/500档分别约50/199.96/495.63 HTTP QPS，错误均0，调度丢弃0/1/131；protocol25工作流/秒短测约100 HTTP QPS、0错误/0丢弃，只测30s不能外推持续容量。Go应用平均单核6.70%，RSS峰值22.16MiB、goroutine峰值41，无采样OOM；依赖节流及数据库锁等待单列，未把端到端尾延迟解释成纯框架性能。

Go full test/race/vet/build、123工具测试、Harness check及17自测通过；计时余量、宿主硬门槛和Go单端选择均有红绿证据。冻结源码/二进制复核一致，生产Go/原Java/SDK/golden/部署未改，无生产数据或切流，readiness503。一次额外容器状态读取审批复核超时未执行，采用既有完整采样和后续库存/清理证据；最终专属容器/卷/网络残留0、VM/socket删除、临时env/WAR/PEM/P12/KEY残留0，证据保留。

完整数据、失败原始摘要、独立SQL复核、时钟口径和修复方向见[并发与一小时实测](capacity-soak.md)。真实云OSS无凭据、真机/浏览器、全部20接口混合并发、大日志/多规格/TLS性能/24小时等仍未验证。下一步优先在不改变接口、fence及幂等语义下补足错误观测、修复锁冲突并重建Go镜像后重新一小时复验，不继续Java压测。

## 第二十阶段：Go死锁与Web上传修复及一小时复验（2026-10-19）

真实MySQL屏障回归确认三类锁冲突：任务耗尽扫描范围UPDATE与发布顺序冲突，重复上传身份/Ensure的INSERT IGNORE共享锁升级冲突，以及不同任务空明细范围DELETE后的插入间隙锁冲突。改为有界候选+主键owner/fence/观察lease/数据库过期时钟CAS、重复键原值upsert，以及任务锁内按实际详情ID每批500主键清理；原事务、租约、fence、源摘要和幂等保护保留。HTTP错误格式不变，补有界脱敏错误类别与压测数值code。旧实现实际1213/活动租约阻塞/批次越界有红证据，新实现完整Go/MySQL矩阵通过，覆盖归属/续租竞态、并发空范围发布、501条历史替换、stale拒绝和原有强杀恢复。

首轮新镜像短档发现剩余间隙锁后中断保留失败，SQL语法及历史夹具问题也留存。最终源码重新构建镜像7099b810…，14项基础容器检查通过；仅Go完整正式流程退出0，调度3660s、宿主活动下界3620.215s，365组采样。36,600工作流/146,400HTTP、约40 HTTP QPS，P95/P99 14.259/27.034ms，错误/丢弃/解析重试/最终及全部周期1205和1213均0；36,600任务/54,900明细全部匹配、job done且attempts最大1。应用平均单核6.22%、RSS峰值21.93MiB，各容器无采样OOM。对照上轮Web上传失败10→0、Native重试4→0；不声称上轮10错误均已可追溯归因。读取500短档仍有4次调度丢弃，不外推生产容量。

最终Go full test/race/vet/build、126工具回归、Harness check与17自测通过，独立代码评审和测量复核无阻断，源码/二进制/SDK摘要一致。原Java/SDK/golden/部署不改，本轮未编译或运行Java，无生产数据操作或切流，readiness503。真实云OSS仍无凭据；真实设备/浏览器、全部20接口混合并发、大日志/TLS性能/多规格/24小时等限制保留。完整修复、精确测量和私有证据见[锁冲突修复报告](go-mysql-lock-recovery.md)。


## 第二十一阶段：Go全20接口混合并发与大日志（2026-10-19）

新增all20负载模式、突发调度、全部内容/ID账本、独立合成协议oracle及隔离控制器，沿用7099b810镜像，未修改业务应用或Java。正式小日志660秒/2640工作流/27720端到端HTTP全部成功，实际重叠8，Go自身约40QPS（含对象GET合计42），HTTP P95/P99为27.591/35.985ms，零错误/丢弃/重试/1205/1213；3960条明细和2640个job完整核对。1MiB档220秒/44工作流亦通过，两端合计11264条明细完整。

整轮正式验收未通过：Native8MiB/Web4MiB档MySQL1GiB OOM退出137，32/8MiB仅最终短诊断通过。随后MySQL2GiB最大档内存诊断受宿主磁盘耗尽影响，DB退出2且非OOM，控制器退出120、报告不完整。不能用短档、失败前零错误或加内存代替正式通过。SQL String缓存约324MiB后平台，其他cgroup增长仍未归因，不能宣称泄漏已定位/修复。

重复上传/并发解析、合法协议超限拒绝及无副作用短诊断通过；真实MySQL续租、失效owner/fence、幂等Ensure、501历史替换/强杀恢复回归通过，未在混合负载期间人为注入失效主。Go full/race/vet/build、本轮142工具测试、Harness17自测与检查通过。已完成两个正式阶段独立计数/资源/账本复核，整体仍标记failed。专属故障VM/socket已核验后回收，保留失败证据和归档。真实OSS无凭据、真机/浏览器、多实例/TLS性能、24小时及生产容量限制保留。只测Go，无生产数据操作或切流。详见[全20接口与大日志验收](all20-large-load.md)。


## 第二十二阶段：大日志MySQL内存和连接预算定位（2026-10-19，未完成）

四组隔离因果对照复现大参数NET连接保留及历史Web utf8mb3字段→utf8mb4连接的String容量放大：8MiB字段、10空闲连接约320MiB，2空闲约64MiB；相同utf8mb4字段反向对照10连接仅约0.18MiB，关闭后分配容量释放。P_S容量不是RSS，未证明泄漏或每个OOM字节来源，原表和接口保持。

控制器增加显式池预算、MySQL2GiB/conn16/redo128MiB/binlog轮转与过期、提交持久性实际值、anon/file/kernel和磁盘余量门槛，默认应用20/10不变。4/2和8/2三档短诊断前两档通过，最大档各两次Native brief 500，保留原5秒等待；8/2单档再现，并记录20.11秒内Connections增加539（包含观测连接）。8/4为待验证候选，不宣称已通过。

宿主初始9.8GiB后降至约2GiB；回收Go缓存、无损归档fixture、专属VM空闲块及经归档核验的闲置工具后仅约3.37GiB。正式启动退出2，因不足4GiB未创建业务容器/正式目录；没有降低门槛、删除其他项目或系统swap。等待释放至少额外8GiB或有余量磁盘，再做8/4对照、确认预算并复跑三档各220秒及真实MySQL租约/fence/幂等回归。这些本轮尚未执行，前轮回归不能代替本轮实测。

146工具测试、Harness检查/17自测通过，应用104文件、原SDK、镜像/负载指纹一致；只Go，保留Java，无生产数据操作和切流。详见[大日志MySQL预算定位](large-mysql-budget.md)。

本轮检查点：专属VM已停止且容器为空，磁盘/镜像/配置保留待恢复；停机后宿主约4.56GiB，并不证明运行期有足够增长余量。正式复跑建议VM启动后至少约6GiB持续余量，仍等待空间条件。


### 第二十二阶段恢复后收尾：三档正式已测，稳定性失败

磁盘恢复后，在同一7099b810镜像、原表/接口/5秒等待下，以MySQL2GiB、池8/4正式跑完三档各220秒。1MiB档506HTTP零错误、8/4MiB档792HTTP零错误，完整性通过；32/8MiB档1102HTTP中10次Native brief500，整体failed，无OOM/1205/1213。最大档独立220秒复核仍22次brief500（550HTTP），在保留失败判定下清理前核对225280条数据完整。独立重算前两档加复核轮的304128条原始SQL长度/SHA/ID、job状态通过，整体稳定性仍failed，不能合并为单轮全矩阵通过。

最大数据库cgroup峰值1.619GiB、RSS约972.6MiB；2GiB已测未OOM，8/4尚非生产稳定配置。真实MySQL租约/fence/幂等/501历史替换/强杀回归由当前源码新编译执行通过。146工具测试与Harness17自测/检查通过，原Java/SDK/应用104文件保持，无生产操作或切流。磁盘已不受阻，当前剩余问题是最大档后台处理时间超过brief等待窗口，未修复；真实OSS、设备、多实例/TLS性能、24h等未验证限制保留。

专属容器/卷/验证网络回收、VM已停止并保留runtime供复现。详见[本轮最终报告](large-mysql-budget.md)，结论为测试已执行而稳定性未通过。


### 第二十三阶段：32 MiB Native 延迟与 brief 等待

- 用回归测试确认并修复本地 worker 重复抢占，保留数据库租约/fence/revision/digest 及幂等发布；逐字节分行改为有界片段扫描，brief 持久状态查询降至 100ms。
- 默认 brief 等待跟随任务预算（默认30s），显式5s及调用方取消仍保留。受控6s对象读取：旧版约5.005s失败，修复版约6.104s完整返回，之后内容/ID/任务审计一致。
- 新增无正文分段计时及分 Native/Web 的上传至完整明细读取核验 E2E 样本；失败不从分位数账目中隐藏。
- Go-only 正式三档共132流程/2860 HTTP、0错误，独立核验304128条明细。32MiB Native E2E P95/P99=4.121/5.071s；最大档MySQL峰值约1.63GiB，无OOM/1205/1213。基线本轮也未复现历史失败；固定5s对照中完整读取P99未改善，不夸大性能结论。
- 全Go test/race/vet/build、真实MySQL回归、155项工具测试通过。Java保留且未运行，不操作生产数据、不切流；真实OSS/真机大日志/TLS性能/多机/一小时或24h长稳仍未验证。详见[报告](native-large-latency.md)。


### 第二十四阶段：实际 iOS 模拟器整体链路

实际 iOS26.5 模拟器原 SDK 写入/上传 → Go → 原 LoganSite 查询、过滤、详情、排序数据及浏览器下载通过。466字节真实上传含3 AES帧/3 gzip成员，10条标记及1条内部头与独立解密、API和SQL逐条一致；任务仅处理一次且无遗留租约，176个SDK文件指纹不变。

仅临时前端增加下载同源转发并适配Sass构建；原源码保留。既有排序文案及类型命名差异已记录。OSS为不校验签名的本地替身，非真实云端/真机/大日志验收，readyz503门禁保留。独立容器和页面保留供查看；无Java运行、生产数据操作或切流。详见[iOS链路报告](ios-simulator-e2e.md)。

### 第二十五阶段：iOS 连续上传与大日志（部分通过）

实际 iOS26.5 原 SDK 连续5次累计上传及1/8MiB正文档通过，正式任务8～14共3811条独立解密/API/SQL逐条一致；原Web任务12/13/14检索、实际下载与独立解密字节一致。上传至全部明细读回：连续0.212～0.229s，1MiB0.224s，8MiB0.735s，样本少不当生产分位数。7个job done、owner空/lease0/fence1/revision1/attempt1；死锁/锁超时0，无OOM/重启。

32MiB三份真实SDK原件均在4338完整帧后出现5字节零长度帧头0100000000，HTTP200/code500 ERROR_DECRYPT；原示例单次上传对照也复现。完整前缀可独立还原8193条，但未裁剪重传、未放宽校验，未创建任务/对象登记；32MiB端到端未通过，SDK内部产生路径尚未完整定位或修复。预检六个上传任务保留且排除正式计时，失败总账保留，不以7个成功替代全通过。

原Java/SDK/Go业务源码保留，未跑Java，无生产操作或切流。OSS仍本地替身，真机/真实云端/长稳/多实例限制及readyz503门禁保留。详见[完整报告](ios-continuous-large-e2e.md)。

### 第二十六阶段：CLogan 空帧尾修复及 iOS 整体复验

公开SDK API三条日志即可重现：完成gzip成员后预留的下一帧头，被显式flush误写。共享CLogan增加9行，仅排除INIT/零内容状态的未使用预留头；Go协议、严格CRC/预算和租约/fence/幂等实现不变。先红后绿回归覆盖重复flush、追加和32MiB；新增坏帧尾不得发布OSS/任务的拒绝测试。Go full/race/vet/build、Java源码指纹、真实MySQL竞争租约/fence/幂等/强杀恢复回归通过，未运行Java。

重建隔离Go镜像后，实际iOS26.5修复SDK三份32MiB及连续5次、1/8MiB共10次上传全部成功；28390条独立解密/API/SQL逐条一致，10个任务仅处理一次且无遗留租约。三份32MiB原Web尾条查询和实际下载字节一致。上传→全部明细读回8.483/2.898/2.724s，保留首个较慢样本；完整brief索引3.191/1.652/1.555s。样本少，不作生产分位数或容量承诺。

旧畸形原件仍被重建Go拒绝且无入库副作用；修复需要更新移动端SDK，不会自动修复已有坏文件。SDK176文件仅CLogan core一项预期修改，原Java/前端保持；无生产数据操作或切流。真实OSS/真机/Android/长稳等限制及readyz503门禁保留。环境保留，详见[修复及复验报告](ios-empty-frame-fix.md)。
