# 修复后镜像、真实SDK与历史对象隔离复验（2026-10-16）

本轮使用 Dockerfile 实际重建的修复镜像，运行真实 Java/Tomcat、Go 和 MySQL。原 Java 源码、配置、SDK、golden及原部署不改；没有启动或停止原业务服务，不执行生产切流。Go readiness实际保持503，容器不发布宿主业务端口。

## 实测结果

| 项目 | 结果与范围 |
| --- | --- |
| 镜像重建 | Dockerfile多阶段编译通过，Go1.27.0，linux/arm64。新镜像 `sha256:c20df6288ab67bb2208cce7f94b5a29a6c583c22e8427664fac0ebe204a898d4` |
| 基础容器运行 | 14项通过：UID10001、只读根目录与合成只读挂载、Linux0400标记、tmpfs权限和重启清空、memory/CPU/PID限制、静态二进制、监控端点、readiness503、两次SIGTERM退出0及归属清理 |
| 真实SDK服务端接入 | 本轮重新编译未改CLogan生成移动端密文、执行未改WebSDK生成加密BMP日志；独立OpenSSL验证Native2条/Web1条。新Go镜像接收产物，实际HTTP→官方OSS SDK本地PUT/GET→真实MySQL→后台解析→查询→签名地址获取及本地下载，Go20接口全部通过 |
| 原Java对照 | 实际原Java WAR/Tomcat9/JRE17运行。Web10和Native初始4项正向通过；Native brief只恢复内部头1条，目标日志缺失。后续6项正向覆盖未获得，不冒充Java20通过。仅允许精确已定位缺失，任何其他差异使验收失败 |
| 历史对象恢复 | 两个预先存在的解密后对象，分别含零/PKCS成员间填充，各有3条日志。相同对象供Java基线副本和Go独立待解析行读取：Java各1条，Go各3条；Go待解析行ID保持。原Java已完成任务与详情所有字段、ID、内容和时间在Go运行前后完全相同 |
| Java→Go→Java数据库往返 | 两个实际Java/JUnit阶段与Go测试二进制运行并核对真实MySQL，既有已解析/待解析数据和Go发布数据通过，拒绝skip |
| 回归 | Go test/race/vet/build通过、Java基线通过；59项脚本回归、Harness检查及17项自测通过。Java/SDK源码指纹保持有效 |

协议样本模式最终也通过：Java20/Go20、严格响应比较通过，双方下载字节均为96字节且SHA相同。此样本未出现字节差异，不推断所有历史输入都无差异。SDK模式的总体验收通过指“Go20通过且Java差异严格符合已声明缺陷”，不代表两端严格一致。`strictJavaGoParityPassed=false`、`javaAll20Passed=false`、`goAll20Passed=true` 分别保存；未比较到的Java后续接口不获得兼容成功记录。

## 历史兼容和下载差异

历史任务采用明确的基线副本策略：每个对象创建一个Java基线待解析行和一个Go待解析行。Java只处理基线行，Go待解析行保持status0；Java停止后Go处理原待解析行。没有把已完成部分任务重置、删详情或重新插入同ID数据。通过完整SQL行快照证明Java已完成行未自动补数。本轮不是生产历史数据修复或可直接执行的批量重解析工具。

CBC解密对象保留完整帧字节。下载验证逐字节匹配独立OpenSSL计算的运行时预期，再用独立gzip成员/填充校验恢复全文并比较。原Java预期准确复现其原IV末块padding探测，不能假设它总会去填充。原协议样本未更换以迁就对照。对合法padding保留/删除的差异有单独观察字段；不把压缩哈希简单忽略后宣称一致。

Go解压恢复此前被Java/旧Go截断的后续成员，这是有意的内容差异。普通gzip工具可能在padding处停止或报错；本轮未将对象重压成通用连续gzip。未知SDK类型7仍按原Java投影为UNKNOWN(0)，API路径、业务code、字段和查询规则不随此轮改变。细节见 [SDK恢复说明](native-sdk-recovery.md)。

## 环境和未验证项

- 临时Lima VZ VM，3CPU/4GiB/18GiB，Docker29.9.1/cgroupv2；镜像、网络、容器和随机数据库全部专属。原Java测试工具链为现有legacy-java编译原源码，运行JRE17；没有验证原JRE8镜像或amd64。
- 对象存储是内部HTTP字节夹具。实际使用官方OSS SDK生成签名查询并下载，夹具不验证密码学签名。用户确认没有隔离OSS凭据，真实阿里云上传下载、匿名/篡改/过期URL拒绝以及云端SDK全链路仍BLOCKED，没有云写入。
- WebSDK在Node中运行真实持久化/report代码，IndexedDB模拟、XHR截获；BMP样本适应原MySQL三字节utf8。没有浏览器原生存储/网络、Android/iOS包装层或真机验证，也不宣称Web emoji可写入原表。
- Web参考RSA实际1023位。本轮Web测试显式使用 `--legacy-rsa-compat` 注入 `GODEBUG=rsa1024min=0`；默认Go拒绝旧密钥的限制不变，没有自动修改生产配置。
- 本轮基础SIGTERM测试不替代活跃请求、任务排空或SIGKILL场景；上一阶段的故障证据独立保留。没有新一轮活跃负载/强杀/长期容量、生产网关reload、备份还原或切流演练。

## 复验和证据

从ServerGo执行，先准备实际SDK产物、原Java bundle和当前源码编译的Linux crossruntime二进制。所有输出要求全新目录；引擎必须为显式专用socket，按标签清理，禁止接入生产。

```sh
python3 script/verify-containers.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --keep-image --work-dir <shared-fresh-dir> --out <fresh-evidence>
python3 script/rehearse-all-interfaces.py --docker <absolute-docker> --host unix://<dedicated-socket> --allow-isolated-runtime --binary <linux-crossruntime-test> --java-bundle <original-java-bundle> --http-go-image <new-built-tag> --namespace-prefix validation --fixture-mode sdk --sdk-fixtures <actual-sdk-dir> --legacy-rsa-compat --historical-objects --work-dir <shared-fresh-dir> --out <fresh-evidence>
```

协议模式用 `--fixture-mode protocol`，去掉SDK目录和RSA兼容参数。缺依赖/执行失败不算通过。云端存储单独入口见 [端到端验收](e2e-acceptance.md)。

本地证据位于系统临时目录，系统可能清理；需要长期留存时另行归档脱敏报告：

- `/private/tmp/logan-phase12-build-3/summary.json`：真实Dockerfile构建和14项基础检查退出0；每个命令保留原始输出。
- `/private/tmp/logan-phase12-protocol-4/summary.json`：协议20接口最终退出0，Java和Go各20通过、严格比较通过，下载字节独立核验一致。
- `/private/tmp/logan-phase12-sdk-e2e/summary.json`：SDK+历史验收退出0；`all20-runtime-acceptance.json`明确Java14/Go20、严格一致失败、无未知响应差异。
- 同目录 `all20-java.json`、`all20-go.json`、`all20-flow-comparison.json`：完整规范化响应、状态、MIME/charset/CORS、下载及发布时序证据；Java失败捕获原样保留。
- 同目录 `historical-object-acceptance.json`：两种历史对象SHA、任务ID、恢复内容及Java完整行前后快照；`event-ledger.json`证明串行启停，旧端退出后新端启动。
- `/private/tmp/logan-phase12-sdk/`及 `logan-phase12-sdk-producer.log`：本轮实际SDK产物、原SDK源码SHA与独立验证。
- `/private/tmp/logan-phase12-go-image.tar`：本地测试镜像归档，SHA256 `52c3246a97a1b599f3ec0e78c17d105d95c28e7fa13badceb91be8d5bd0563bb`；5个OCI blob散列核验。`logan-phase12-source-provenance.json`记录生产Go源码散列。不是经签名/扫描的正式发布产物。
- `/private/tmp/logan-phase12-tools-final-2.log`：59项脚本测试通过。
- Go full：`/var/folders/6w/c5rms8nd1z10_pdghq0j31bm0000gn/T/logan-agent-dd0nrnfs/summary.json`；Java基线：同级 `logan-agent-0t89zdtw/summary.json`。集成入口默认skip与真实单独执行的证据分开。
- `/private/tmp/logan-phase12-cloud-blocked/summary.json`：无私密配置退出2，存储门槛仍blocked，不覆盖独立容器MySQL的通过结论。

失败记录保留：VM安装时包锁/HTTP源502，改HTTPS且保留签名校验；初次socket组权限和基础镜像EOF后重试成功；错误格式的旧MySQL本机安装包未当作Docker镜像使用，改拉官方固定版本。协议首轮因镜像未就绪未执行；随后暴露MySQL临时bootstrap socket竞态，增加TCP就绪回归修复。再次协议对照暴露Java下载预期错误，按原Java探测行为用独立OpenSSL修正，未换样本或放宽精确断言。

最终清理：测试容器/专用网络/专用卷余量0，临时WAR/env/RSA副本无残留，专用VM已停止并删除；保留本地镜像归档及证据。见 `/private/tmp/logan-phase12-cleanup.json`。Git跟踪的原Java/SDK/部署文件无修改，独立最终复核未发现重要问题。
