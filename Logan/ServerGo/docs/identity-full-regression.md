# 身份迁移后的全接口与 SDK 回归

本轮验收将受控身份授权、旧计划补数与回滚，和完整 HTTP/SDK 链路放在同一个隔离数据库中执行。原 Java、SDK、golden 及原部署保留，不操作生产数据、不执行生产切流。

## 验收标准

三个边界分别是旧操作身份授权完成、原 pending 计划补数完成、三个原操作回滚完成。每个边界运行 protocol 与 SDK 两种模式，Java 与 Go 串行启动，使用同一个提升后的 MySQL。all20 自己创建独立合成任务；回归前后必须证明旧任务、详情、原审计、身份授权及历史对象不变。

protocol 模式要求原 Java 和 Go 各 20 接口通过且规范化响应一致。SDK 模式要求 Go 20 接口完整恢复实际上传内容；原 Java 的 Native 多成员截断保持严格失败记录，仅允许已定位的精确差异，不能据此声称 Java20 或两端全部一致。SDK 输入来自重新编译未改 CLogan、执行未改 WebSDK，并使用独立 OpenSSL 解密核对。

## 环境边界

真实阿里云 OSS 没有凭据。内部 HTTP 对象夹具验证官方 SDK PUT/GET、签名 URL 生成、重定向和下载字节，不验证云端签名密码学、权限及过期拒绝。

WebSDK 在 Node 中运行，IndexedDB 模拟、XHR 截获；不等价于浏览器、Android/iOS 真机网络链路。原参考 RSA 为 1023 位，SDK 模式必须显式开启已有 legacy RSA 诊断兼容；默认配置不放宽。Go readiness 保持 503。

身份迁移依赖预先可信见证与外部旧主 fencing，不提供自动 HA、多跳迁移或跨复制分叉的全局一次消费。完整规则见 [身份迁移说明](reparse-identity-migration.md)。

## 复验入口

先在专属引擎用 `script/verify-containers.py --keep-image` 构建当前 Dockerfile 并检查基础容器，再编译当前正式 `cmd/logan-reparse`，重新生成并独立复核实际 SDK 产物。下面入口要求全新证据和工作目录，拒绝默认 Docker context。RSA 参数是本地 SDK 诊断的显式授权，不修改默认服务配置。

```sh
python3 script/verify-identity-all-interfaces.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary <current-linux-reparse-cli> --go-image <fresh-built-image> --java-bundle <original-java-bundle> --sdk-fixtures <fresh-verified-sdk-directory> --legacy-rsa-compat --out <fresh-evidence> --work-dir <fresh-private-shared-directory>
```

控制器复用真实 TLS/GTID 提升、授权事务崩溃和幂等恢复验证；每个边界的两个模式各有独立设备命名、对象夹具和证据目录。原任务、详情、审计和授权完整字段使用 SQL JSON/HEX 传输核对，避免混淆 NULL、文本 NULL、转义字符与 Unicode。任何未知接口差异、原记录改动、旧计划变化或缺少矩阵边界均失败，不自动生成新基线。

准备或执行失败时也删除各轮私钥和带测试凭据的 WAR，按标签清理资源；新增故障注入测试覆盖准备阶段失败。TLS 观察记录实际 reader 账户会话的协议和 cipher；Java/Go 串行启停，但当前不按容器 IP 对会话逐一归属。

## 本轮发现并修复的验收问题

首轮授权后的 protocol 模式 Java20/Go20 严格通过，SDK 模式得到 Go20/Java14 的精确既有差异，但最终固定 `GET >= 4` 的对象计数断言失败。该断言把原 Java 在 Native brief 即失败的已声明场景，也按完成 Native 下载计算；早先历史对象场景的额外 GET 掩盖了它。

修正后，仅在完整 runtime assessment 已通过时计算最少请求：两端各一次 Native 上传与懒解析，加上每个已通过精确字节/解压校验的 Native 下载。protocol 仍至少 2 PUT/4 GET；精确已声明 Java SDK 缺失场景至少 2 PUT/3 GET。不是把 Go 缺下载当作可接受。新增测试拒绝 protocol 少一次 GET、SDK 少一次 GET和任何失败 assessment，HTTP、正文和未知差异判定保持不变。首轮保留为失败证据，不作为完整验收成功。

## 已实测结果（2026-10-17）

最终第二轮从全新合成库执行，退出0，三个边界全部完成。每个单元都是本轮重新启动实际 Java WAR 和新 Go 镜像；原 Java 源码、SDK、golden 和原部署无修改。

| 状态 | protocol | 实际 SDK | 原记录保护 |
| --- | --- | --- | --- |
| 三个旧操作身份授权后 | Java20/Go20，严格一致 | Go20；Java14，严格失败仅为已声明 Native 缺失 | 完整旧任务/详情/审计/授权/历史对象不变 |
| 原 pending30 补数后 | Java20/Go20，严格一致 | Go20；Java14，同一精确差异 | 同上；补数后的旧记录也不被回归请求改动 |
| 原30/40/50回滚后 | Java20/Go20，严格一致 | Go20；Java14，同一精确差异 | 同上；回滚后记录保持 |

合计 protocol 两端各60个正向路由，SDK Go60/Java42个正向路由；不能将总体验收通过解释为 SDK Java20 或所有运行时严格一致。每轮Go均要求上传、任务解析、查询、下载地址和精确下载内容全部通过；Web10两端完整通过。实际 SDK 产物重新生成后，独立 OpenSSL 恢复 Native2条/Web1条；Native 包含真实 CLogan 内部头和目标日志。原 Java 在 Native brief 断言处失败，后续6项不计正向覆盖。

本轮实际 Dockerfile Go1.27.0/linux-arm64 构建镜像 `sha256:0e65f268ee4d77dba63498475db724c86bad97fb65cf5d549ea67422a30a5b9d`，14项基础检查通过：UID10001、只读根/合成挂载、tmpfs重启清空、memory/CPU/PID限制、静态程序、监控端点、readiness503、两次SIGTERM及归属清理。正式离线CLI SHA256为 `8e56c619ec7830ed3ebbd1e8c91412b38de6c95f9aae2b4480ab7cdffb6be1f0`，当前源码linux/arm64、CGO0、stdjson、trimpath构建，无诊断源码overlay。

真实MySQL8.4.11 TLS/GTID复制提升、旧主SIGKILL并保持停止、未授权跨UUID拒绝、13类迁移负例、只读预检、授权事务中崩溃恢复、重复/并发授权及旧操作补数回滚全部重新执行。十个旧数据状态的原Java/Go读取共216响应、20对象下载通过；六组all20额外核对12个reader运行时TLS观察、222个正向路由。六次all20的原记录完整快照前后完全一致。

独立重放实际证据：源见证canonical摘要、授权整行摘要、原审计before/after档案SHA、原计划文件SHA核对通过。旧任务全部完整字段保留，原审计identity/原操作ID/原档案保持；原40只有state按回滚语义推进。最终旧30/40/50均rolled_back，旧详情仅删除原40本操作的4001/4002，其他旧ID/内容/时间保持，原30本轮追加数据按本操作ID回滚，历史对象字节不变。原Java不认识新增内部审计表，兼容结论来自实际业务读取和其前后审计/授权不变，不声称Java提供了新审计API。

Go full的test/race/vet/build、原Java基线、111项工具测试、Harness检查及17项自测通过。工具测试最初受沙箱回环端口限制导致已有监控子进程未启动；允许本地合成进程所需权限后完整通过，没有跳过该项。控制器、清理修复与请求计数修正经独立评审；最终证据无阻断。

## 证据与范围

系统临时目录可能被清理；这些文件是本地合成验收证据，不是生产发布认证：

- `/private/tmp/logan-phase18-build/summary.json`：当前Dockerfile实际构建、14项基础检查和镜像ID。
- `/private/tmp/logan-phase18-e2e-2/summary.json`：最终退出0、三个边界和归属清理。
- 同目录 `all20-gp/gs/ap/as/rp/rs/`：每组完整Java/Go响应、flow差异、strict判定和对象传输计数。p=protocol，s=SDK；g=授权，a=补数，r=回滚。
- 同目录 `identity-all20-*-protected-before/after.json`、`identity-all20-authorized/applied30/rolledback.json`：完整旧数据、审计、授权和对象保护。
- 同目录 `identity-all20-events.json`：串行启动/退出与12次实际TLS会话观察；旧数据十状态读取在 `real-reader-summary.json` 及各phase原始响应。
- 同目录 `source-witnesses.json`、`identity-grant-crash.json`、`identity-recovery.json`、负例前后快照：真实迁移与恢复；私有计划及见证保留在 `/private/tmp/logan-phase18-work-2/`。
- 同目录 `independent-matrix-audit.json`：独立快照、路由覆盖、TLS、见证/授权/审计摘要和旧ID边界重核。
- `/private/tmp/logan-phase18-e2e-1/summary.json`：请求计数断言修正前失败轮，退出1，归属清理通过；不代替第二轮成功记录。
- `/private/tmp/logan-phase18-runtime-ready.json`、`logan-phase18-source-provenance.json`、`logan-phase18-controller-source-final.json`：引擎、镜像、构建源码及最终控制器摘要；当前文件比对通过。SDK产物/原源码摘要与独立解密结果在 `logan-phase18-sdk/` 及 `logan-phase18-sdk-producer.log`。
- `/private/tmp/logan-phase18-go-image.tar`：本地镜像归档，SHA256 `28dd6767c3cfe7e4199ca84aa5160989931cdc2075040e0deec712d168bf8c1d`；5个OCI blob摘要复核通过，见同名前缀JSON。没有镜像签名/漏洞扫描/正式仓库发布。
- Go full证据为系统临时目录 `logan-agent-ge5tslu0/summary.json`；Java为 `logan-agent-uqz42lhw/summary.json`。`/private/tmp/logan-phase18-tools-final.log` 为111项，`logan-phase18-harness.log` 为17项。
- `/private/tmp/logan-phase18-cleanup.json`：专属引擎容器/卷/用户网络均0，敏感临时副本残留0，专属VM与socket已删除；保留合成产物、私有计划/见证、报告和镜像归档，未使用全局prune或修改默认context。

本轮没有生产数据、生产入口、网关或云对象操作。默认readiness503仍阻止生产放量。未覆盖真实云OSS、浏览器/真机、原JRE8/amd64、持续容量/生产网络故障、自动HA/脑裂/物理断电；也没有把基础SIGTERM检查当作新一轮活跃负载排空验证。真实SDK指未改SDK代码生成的产物进入实际服务端HTTP/官方OSS SDK本地传输/MySQL/后台解析链路，保留前文环境限制。
