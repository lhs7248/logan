# 移动端 SDK 日志恢复修复（2026-10-16）

Go 现在仅在已确认完成的 gzip 成员边界识别填充，使真实 CLogan 连续帧中的目标日志可恢复。原 Java、SDK、API 路由、JSON 字段、任务状态和数据库迁移没有修改；没有生产切流，readiness仍503。

## 根因和修复

CLogan 每个完成帧使用连续 CBC 状态加密，并在结束时写入1至16字节的 PKCS#7。原 Java 仅用初始IV单独解密最后一个密文块，以此判断整帧是否有填充。多块帧的探测值往往不等于正确明文，导致填充残留在 gzip 成员间；原 Java 与旧 Go 在第一个成员之后停止解压，目标日志丢失。

本轮只在 Go 修改两处：

- 帧解密保留所有正确CBC明文字节，不以末尾看起来像PKCS#7就删除。跨帧gzip片段可能以合法padding字节结尾，只有解压阶段知道成员边界。保持流式64KiB缓冲、每帧重置IV、输入/帧预算、取消、非法长度和原有正探测但完整padding无效的拒绝规则。
- 解压兼容已落存储的旧对象：仅在完整 gzip 成员之间，允许一组1至16字节的完整 PKCS#7，或1至15字节的零对齐填充，且后面紧接 `1f 8b 08` 才跳过。最多检查19字节，不搜索任意垃圾，不跳过损坏 gzip 的 CRC。所有成员共享明文总预算。

跨帧 gzip 的原始字节连接后解压；未完成流的对齐片段不随意去零或去PKCS#7。确定性反例使用gzip额外头字段，使第16字节为真实0x01；分帧后仍精确恢复正文，避免直接去填充造成的新丢失。官方已完成 CLogan 帧带PKCS#7。历史真实用户日志尚未提供，本轮历史回归是原Java合成向量和明确来源的SDK产物/旧对象形态。

## 明确的兼容差异

| 输入/状态 | 原 Java、旧 Go | 修复后 Go |
| --- | --- | --- |
| 真实 CLogan 两帧（内部头+目标日志） | 只恢复内部头 | 精确恢复两条，目标内容、原始类型和时间戳正确 |
| 正确PKCS#7 | 探测可能去除或保留padding | 帧层保留padding，解压层在已完成成员边界识别；部分对象字节/长度有差异 |
| 已存 gzip 成员间有上述合法padding | 通常在第一成员后停止 | 恢复后续成员；不能保证旧 Java读新数据得到同样记录数 |
| 零对齐的多成员历史向量 | 原样例只恢复首条 | 恢复第二条，原golden不覆盖重写 |
| 非法间隔、超长填充、CRC损坏、截断 | 既有完整行保留规则 | 不做任意扫描，维持既有损坏处理和预算 |
| 原始类型7 | 查询投影为UNKNOWN(0) | 保持UNKNOWN(0)，不随此次修复更改类型枚举 |
| 已标记完成的旧任务 | 已发布的部分详情 | 不自动补数；需后续单独设计受控重解析，不能仅改状态就当作修复完成 |

原Java12个协议向量不变。Go测试显式列出2个解密字节差异（pkcs5-single-block、damaged-next-member-header保留原来被删除的13字节padding）和1个额外记录恢复差异。其余预期保持；原Java正探测但完整末块padding无效的输入仍拒绝。回滚到Java可能重新截断含旧padding的对象；上线/回滚前应对具体对象和任务详情做对账，此轮不执行该操作。

## 测试与证据

`internal/protocol/native_recovery_test.go` 覆盖短读（单字节/7/16/64KiB）、连续帧、跨帧gzip、超过64KiB的CBC帧、全部1至16种PKCS#7、旧零填充、已存padding对象、拒绝扫描垃圾和恢复第二成员后的总预算。原向量继续覆盖CRC、截断、跨缓冲换行和无最终换行。

固定回归样本在 `testdata/protocol/clogan-regression/`，由未修改的共享C SDK产生，使用公开合成AES参数和测试文本。`native-plain.bin` 为独立OpenSSL逐帧去填充后gzip输出；`legacy-padded.gz` 是独立OpenSSL `-nopad` 解密形成的历史对象形态，并非真实用户数据。来源及SHA256在provenance.json。未加入任何私钥。

`native_sdk_recovery_test.go` 使用实际样本执行 Hertz上传、官方OSS SDK访问本地HTTP夹具、内存任务仓库、真实解析/发布、brief与详情查询，断言两条、目标只出现一次、时间戳和类型映射正确、重复查询不重复发布、临时文件回收、readiness503。它不是实际云端或真实MySQL部署。压缩对象下载仍保持分帧解密字节，未重压为标准连续gzip；普通gzip工具可能遇padding只读取首成员或报错，外部消费者应使用此兼容解压器。没有声称下载内容已变为通用gzip工具可完整读取。

本轮另外重新编译未修改CLogan、运行未修改WebSDK生成全新样本，再独立OpenSSL验证。Go显式SDK测试通过，Web使用参考1023位RSA时仍需要测试专用 `GODEBUG=rsa1024min=0`；此次没有修复或放宽该RSA限制。

本地证据（系统可能清理）：

- `/private/tmp/logan-native-fix-red.log`：新增回归先失败。
- `/private/tmp/logan-native-sdk-before.log`：修复前真实SDK目标计数0。
- `/private/tmp/logan-native-sdk-chain.log`：修复后本地上传/解析/查询链路通过。
- `/private/tmp/logan-native-fix-sdk-fresh/`、`logan-native-fix-sdk-producer.log`：全新实际SDK产物和独立验证。
- `/private/tmp/logan-native-fix-sdk-after.log`：显式SDK Native/Web测试通过，Web旧RSA选项范围如上。

独立复核发现中间片段0x01误删反例，已先观察失败，再改为成员边界处理；最终复核无新增问题。

真实OSS仍无凭据；本轮未重建镜像、未重跑隔离容器全20接口，也未执行浏览器/Android/iOS真机、生产数据批量重解析或生产切流。前一阶段SDK失败记录保留，不能将此次本地修复通过追记为当时已通过。

最终全量 Go 验证：`/var/folders/6w/c5rms8nd1z10_pdghq0j31bm0000gn/T/logan-agent-mmjm1aa2/summary.json`，test/race/vet/build均退出0；包含真实SDK本地链路回归。协议详细日志：`/private/tmp/logan-native-fix-protocol-final.log`，10个新增测试和原12个Java协议向量均通过。Harness检查通过，Java源码指纹保持有效。

后续实际重建镜像、Go20接口及历史对象容器复验结果见 [容器SDK验收](container-sdk-validation.md)。
