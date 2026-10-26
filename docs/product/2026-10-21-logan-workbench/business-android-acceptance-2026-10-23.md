# 业务测试 App Logan 接入与验收

> 后续复审：[2026-10-26 提交前复审与修正](business-android-review-2026-10-26.md)；下文保留 10 月 23 日的实际验收基线。

日期：2026-10-23。目标：真实业务工程 `hxl-host-app`、包 `com.hxl.xlb.test`（6.0.1 / 6001），不是此前独立示例包。用户已确认 [接入范围](../../superpowers/specs/2026-10-23-business-app-logan-preflight.md)。

## 实现与数据边界

业务 Android Debug 版本通过主进程初始化接缝连接实际仓库 Java/JNI/CLogan。可选 AAR 从 Logan 源码构建，不包含访问凭据。未提供该构建输入时正常 Debug 可编译，入口明确显示不可用；Release 不链接这项 SDK。已有青鸟等监控保持原样。Debug 调试入口要求调用者拥有 DUMP 权限，应用内部也可从原有容器测试页进入。

首批记录应用启动和允许名单内的页面创建、进入、退出与销毁，内容仅含事件分类、页面分类、随机安装/会话标识及时间。不抓取任意 logcat，不上传账号、登录令牌、路由参数、请求响应正文、聊天、订单或既有第三方监控文件。不能把此次贯通说成所有业务日志、网络错误或 RN 内部页面埋点已全面覆盖。

原始日志保存在业务 App 私有目录；上传时由实际 SDK 选取当日加密文件，固定发送事件快照及密文摘要，并保留独立密文副本。上传回执绑定提交 ID。真实 HTTP 接收和最终解析结果分开核对；2xx 响应结构不合法时显示协议错误。无当日事件不发起上传；超时不宣称已取消，并明确保留数据重启后恢复的路径。

平台继续使用 `local-mobile`，新增独立业务上传客户端 `business-android-test`、环境 `test-local`。不替换示例客户端、管理员凭据或原解码密钥，不清理已有日志资产。业务 App 只持有本应用的上传专用凭据。

## 已执行的安装与原生核对

- 手机实际安装包为 `com.hxl.xlb.test`，启动入口 `com.hxl.app.NativeHomeActivity`。
- 原包与本次工程构建签名证书 SHA-256 一致：`b3616ef6116ba7d19411d7df5ba129baec726c8eb85ab34f34cb46f804edb083`。
- 使用同包名保留数据覆盖安装；安装后、首次重新启动前，原 `xlb_auth` 登录存储逐字节未变。未执行卸载、清数据、登录退出或账户迁移。
- 上传私有配置以 stdin 写入应用沙盒并核对一致，未放入 Intent、URL、APK、日志、截图或报告。
- SDK AAR 源码基线为本仓库 `7b2bf7c`，包含实际 arm64-v8a 原生库，AAR SHA-256：`b5b32f16228eef35eb27eae52b6d0d08634c7c61bd73d5d72b7b5cacd2b1cd97`，144552 字节。MIT 版权与许可随可选 Debug 资源保留。
- Go 完整 test/race/vet/build、正式 Web 63 项、原型 124 项通过。27 项运行脚本/验收工具测试及最终业务 Android 13 项针对性测试通过；业务原生 APK 构建通过；此前不带 AAR 的 Debug 编译通过。未执行 Release 实包构建。
- 独立代码复核未发现剩余高置信度 P1/P2。已修复回执内层字段解析、快照准备异常释放、跨天无事件上传造成的采集暂停，并补回归。

## 真机闭环状态

用户解锁后完成真实业务 App 的原生生命周期采集 → App 按钮调用实际 SDK 上传 → Go 严格解析 → 正式 Web 阅读。不是脚本制造事件或代替 App 上传。验收结果如下：

| 场景 | 实际结果与证据 |
| --- | --- |
| 首次成功业务上传 | [initial.json](evidence/business-android-2026-10-23/initial.json)：115 条业务事件 + 1 条原生日志头，28,392 字节，严格解析零错误；解析内容与 App 固定快照逐条匹配，服务原文件与 App 实际发送密文摘要一致 |
| 同文件再次上传 | [duplicate.json](evidence/business-android-2026-10-23/duplicate.json)：不同提交 ID，复用相同资产、解析任务、回执及密文，无重复资产 |
| 服务重启 | [service-restart.json](evidence/business-android-2026-10-23/service-restart.json)：同一日志、回执、解析内容和密文保留 |
| App 关闭重开 | [app-restart.json](evidence/business-android-2026-10-23/app-restart.json)、[persistence.json](evidence/business-android-2026-10-23/persistence.json)：强制停止进程后从真实业务首页重开；配置与安装身份保留，115 条旧事件全部保留；新原生代重放 115 条已提交事件，追加 4 条真实生命周期事件 |
| 重启后再次上传 | [after-restart-upload.json](evidence/business-android-2026-10-23/after-restart-upload.json)：119 条业务事件 + 1 条日志头，29,600 字节，仍逐条及逐字节摘要匹配，严格解析零错误 |
| 新日志再次跨服务重启保留 | [final-service-restart.json](evidence/business-android-2026-10-23/final-service-restart.json)：最新 120 条解析事件、回执与原始密文全部不变 |
| 旧数据保留 | [retention-summary.json](evidence/business-android-2026-10-23/retention-summary.json)：原有 4 个工作台对象内容摘要不变；[原示例日志](evidence/business-android-2026-10-23/existing-example-retained.json)的 482 字节原文件和 3 条标记事件 + 头仍通过核对；失败业务资产也保留 |
| 正式 Web | 已实际选择最新业务解析任务，显示 Android 6.0.1 / 6001、完成、120 条事件，打开 home / activity_resumed 原始事件详情；[阅读页](evidence/business-android-2026-10-23/web-reader.png)、[事件详情](evidence/business-android-2026-10-23/web-event.png) |

最新资产 `aff5bfe01b71099e3e7933552a017ec1`，解析任务 `c5ed533f1bb689755765f90cd6d7c19f`，回执 `341aa22e1cac941bc9a13041c5863c6d`。原始文件 SHA-256：`064cc77d59a2f66f53652c9e4bc091aac875e42cc1f689b4db986eb92e1221bf`。

网页：本地服务 `/workbench/#project=local-mobile&page=reader&tool=ai&issue=&run=c5ed533f1bb689755765f90cd6d7c19f&detail=0&batch=`。浏览器重新加载后如出现登录页，使用原管理员令牌连接 `local-mobile`；这不影响服务上的资料保留。未把管理员令牌放到业务 App。

## 旧数据与操作说明

追加业务配置后，原 settings 文件字节未变，原 4 个工作台对象内容摘要未变；原示例日志仍通过其事件与原始加密文件核对。

在 Logan 仓库根目录执行以下命令新增或复用业务凭据；输出目标必须是不存在的私有文件，重复调用不轮换已建立身份：

```sh
python3 Logan/ServerGo/script/workbench-local.py business-app-profile --output /absolute/private/new-business-profile.json
python3 Logan/ServerGo/script/workbench-local.py restart
```

将私有配置通过受控 stdin 写入目标 App 的 `files/workbench-profile.json`，重启 App 后生效。不得把文件内容粘贴到共享终端记录、Intent 或截图。设备重新连接后需恢复 `adb reverse tcp:8091 tcp:8091`；USB 转发不是 App 持久化配置，不保证拔插或整机重启后保留。

实际上传之后，可只读核对业务 App 事件快照、原始密文副本、Go 资产与严格解析事件：

```sh
python3 Logan/ServerGo/script/workbench-business-android.py --serial YOUR_DEVICE_SERIAL --report /tmp/business-check.json
```

服务/App 重启后、再次发起上传前，可追加 `--baseline /tmp/business-check.json` 比较同一回执、原文件、事件及资产。重启新产生的真实生命周期事件可以追加到当日日志，不能要求活动日志文件从此不变；既有发送快照和资产应保持不变。

不包含阿里云推送、源码映射、iOS 实体机、完整设备重启和生产发布验收。日志内容遵循当前 7 天保留期；本地目录持久化不能替代备份。

历史阶段证据：[重启保留的初步核对](evidence/business-android-2026-10-23/restart-preliminary.json)，仅证明当时的初步状态。最终结果以上表为准。[最终业务 APK 与证书](evidence/business-android-2026-10-23/apk-verification.json)：最终 APK SHA-256 为 `dbf14d25218ef1ce19d73949b605bc233fb3c08c00c6cac4e4479125c5198b78`；原生构建和 13 项测试通过。

## 真机复核发现：进程中断后的原生缓存恢复

解锁后的首次实际上传，先观察到 USB 转发丢失造成的真实网络失败；恢复转发后，业务 App 收到 HTTP 200、平台登记了资产，但严格解析拒绝该文件。失败资产和原文件均保留，见 [帧与解码证据](evidence/business-android-2026-10-23/native-recovery-failure.json)。

定位到旧 CLogan 的 mmap 恢复路径：进程在日志段尚未结束时退出，恢复时将缺少完整 gzip/AES 结束数据的缓存追加到当日文件。只补一个帧尾字节也不能修复压缩内容；未修改 Go 严格解析、未跳过坏段或删除旧文件。

采集侧修正为每个新进程使用独立原生目录，将私有写前日志中已持久化的真实当日事件通过 SDK 重新编码，再继续记录新事件。恢复保留内容中的原始 timestamp、sessionId 与 installationId；SDK 外层写入时间和线程属于恢复进程，可能与原始事件时间不同。恢复来源、代目录和重放数量写入回执元数据。这是业务 Debug 适配器的恢复方案，不宣称已普遍修复上游 SDK 的缓存恢复。

旧原生目录、失败上传快照与平台失败记录保持原样。对代目录和容量作拒绝式限制，达到上限提示受控归档，不自动清理已有数据。最终新文件已重新核对全部真实事件和实际发送的密文；旧失败文件仍是失败状态。每日上限 240 条，完整重放及逐条 flush 最多 480 次队列操作，低于所用 SDK 的默认 500 条队列限制。新事件先原子提交私有写前日志，再发布并入原生队列；失败提交不进入活动记录。跨本地日期需非破坏性重启 App。

另一次实际发送观察到读取超时；恢复 USB 转发并通过 App 重试后成功。精确原因未证实，不声称修改或修复了 SDK HTTP 传输层。

**阅读时间限制**：恢复日志在 Web 时间线显示的是 SDK 外层重新入队时间；JSON 内容中的 `timestamp` 才是原始事件时间。因此不能用恢复后的外层时间线推断原始业务事件间隔。当前日志等级和模块未独立埋点，Web 显示“未知”，不代表解码失败。本次只验收真实生命周期链路；原始时间的结构化展示、完整业务语义埋点和生产容量仍需后续设计与验收。
