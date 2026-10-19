# 接口与 SDK 验收（2026-10-16）

本轮保留原 Java 服务和源码，不执行生产切流。隔离环境使用实际 Java/Tomcat9/JRE17、生产 Go 镜像、MySQL8.4.11、linux/arm64；对象存储是本地 HTTP 字节夹具，不能验证阿里云签名。Go readiness 继续503。

| 项目 | 结果与边界 |
| --- | --- |
| 全20接口协议对照 | PASS：Native10 + Web10，实际上传、解析、查询、下载；比较完整规范化 JSON、状态、MIME/字符集、固定 CORS 和下载内容。仅归一化运行时ID、写入时间、随机文件名。使用合成协议样本，不能代替真实 SDK |
| 真实 WebSDK 产物 | PASS：未修改 SDK 的加密日志→持久化→report产物，经实际 Java/Go 服务上传、解析、查询、下载，Web10接口对照通过。使用 BMP 中文输入；Go 显式启用旧 RSA 兼容配置。Node 模拟 IndexedDB、截获XHR，不是浏览器网络或真机验收 |
| 真实 CLogan 产物 | FAIL：独立 OpenSSL 能恢复内部头和目标日志，但原 Java 与 Go 仅恢复内部头，均未发布目标记录。严格断言保留；整体 SDK 验收退出1 |
| 默认 Go 旧 RSA | FAIL：参考密钥实际1023位，当前 Go1.27 默认拒绝；测试专用 `GODEBUG=rsa1024min=0` 后 Web通过。不自动放宽生产安全配置 |
| 真实 OSS | BLOCKED：用户确认没有隔离测试凭据；没有云端上传、下载、签名有效性或过期拒绝的实测 |
| 浏览器、Android/iOS设备 | 未执行；原JRE8镜像、amd64运行、生产网关和长期容量亦未验收 |

Native定位证据：原 Java 和 Go 对末尾密文块的 padding 探测沿用初始 IV，解压在后续 gzip 成员前停止；正确逐帧解密可恢复日志。本轮不修改原 Java，也不默默改变 Go 兼容行为。修复需增加跨 gzip 成员、分帧和历史数据回归，再明确是否同步修复 Java。旧 RSA 需另外制定密钥升级/历史数据解密策略。原 Web 表为三字节 utf8，BMP成功不能推断 emoji 可写入。

原 Java Web详情为异步批量发布，索引可能先返回空数组。验收在15秒预算内等到完整预期记录才计成功，记录首次空响应和轮询次数；通过证明最终内容一致，不证明两端响应时序完全相同。

## 复验入口

从 ServerGo 执行。详细前置参数见 `tests/e2e20/README.md`、`tests/sdk/README.md` 和 `script/rehearse-all-interfaces.py --help`。

- `--fixture-mode protocol`：合成协议全20接口；缺依赖或任何内容失败都不算通过。
- `--fixture-mode sdk --sdk-fixtures <独立生成目录> --legacy-rsa-compat`：真实 SDK 产物模式，参考旧密钥需要显式兼容选项。当前预期暴露 Native失败，不得改断言使之通过。
- 两者都要求显式专用 Docker socket、`--allow-isolated-runtime`、Go测试二进制、原Java测试bundle和已核验Go镜像；不发布宿主业务端口，按归属标签清理资源。
- `all20-flow-comparison.json` 在整体失败前保存分链路结果，Web通过不能替代 Native或整体通过。

## 真实 OSS 准备

`python3 script/verify-cloud-e2e.py --out <全新证据目录>` 在没有配置时退出2并记录 blocked。获得凭据后使用 `--config <绝对路径> --run-oss`；配置必须为当前用户所有的普通文件，权限0600，最大16KiB，只允许字面 `KEY=value`，不执行脚本。环境中的其他云凭据不会隐式用于该入口。

必填键：`LOGAN_IT_ALLOW_WRITES=1`、`LOGAN_IT_OSS_PRIVATE_BUCKET=1`、`LOGAN_IT_OSS_MODE=aliyun`、`LOGAN_IT_OSS_ENDPOINT`（阿里云HTTPS）、`LOGAN_IT_OSS_REGION`、`LOGAN_IT_OSS_BUCKET`、`LOGAN_IT_OSS_PREFIX`（`logan-it/`开头、`/`结尾）、`LOGAN_IT_OSS_ACCESS_KEY_ID`、`LOGAN_IT_OSS_ACCESS_KEY_SECRET`；可选 `LOGAN_IT_OSS_SESSION_TOKEN`。仅使用隔离私有桶和受限测试前缀。

套件覆盖精确字节 PUT/GET、签名下载、匿名/篡改/过期签名403、取消与清理。过期用单独5秒测试签名，未更改生产有效期。只删除本次登记的两个随机对象；不遍历桶。证据脱敏签名URL及密钥。

此入口只认证存储套件。即使 OSS通过，真实云端 SDK→HTTP→OSS→MySQL 全20接口仍需单独部署验收，当前本地控制器未提供真实云模式，不会自动将其标记为通过。

## 本轮证据

系统临时目录可能被清理，发布前应归档脱敏报告：

- `/private/tmp/logan-e2e20-protocol-3/summary.json`：最终全20 PASS；同目录分链路比较、原始响应、退出顺序及清理记录。
- `/private/tmp/logan-e2e20-sdk-2/summary.json`：整体 FAIL；`all20-flow-comparison.json` 证明 Web10 PASS、Native两端均缺6个正向覆盖，失败接口不计覆盖。
- `/private/tmp/logan-sdk-java-compatible2`：真实SDK、独立解密、原Java Native失败/Web通过证据；`/private/tmp/logan-sdk-java-bmp` 为实际HTTP使用的BMP样本。
- `/private/tmp/logan-phase10-sdk-go-default.log`、`logan-phase10-sdk-go-legacy.log`：默认和显式旧RSA配置的独立 Go SDK 测试，均因Native失败而退出1。
- `/private/tmp/logan-phase10-cloud-blocked/summary.json`、`logan-phase10-oss-blocked/summary.json`：缺凭据退出2。OSS套件仅编译、vet通过。
- `/private/tmp/logan-phase10-tools-final-escalated.log`：53项脚本测试通过；受限环境首轮进程清理测试未启动子进程，获得本机测试执行权限后全量通过，首轮日志保留。
- Go full：`/var/folders/6w/c5rms8nd1z10_pdghq0j31bm0000gn/T/logan-agent-svs02bhh/summary.json`，test/race/vet/build通过；默认SDK测试跳过，不能据此宣称SDK通过。
- Java baseline：`/var/folders/6w/c5rms8nd1z10_pdghq0j31bm0000gn/T/logan-agent-zbkl2ilg/summary.json`。新集成入口默认跳过，实际 SDK失败由单独显式运行记录。

本轮只补齐验收工具与证据，真实 SDK及真实云端整体验收尚未完成，不具备生产切流资格。

Harness检查及17项自测通过。专属容器/网络余量均0，临时VM已停止并删除，见 `/private/tmp/logan-phase10-cleanup.json`。

后续状态：第十一阶段已修复Go移动端解析丢失，真实SDK及本地上传解析查询回归通过，见 [SDK恢复修复](native-sdk-recovery.md)。本页第十阶段的失败证据保持原样，未追记云端或容器20接口已通过。

后续实际重建镜像、Go20接口及历史对象容器复验结果见 [容器SDK验收](container-sdk-validation.md)。
