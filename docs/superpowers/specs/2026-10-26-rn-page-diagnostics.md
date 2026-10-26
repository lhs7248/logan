# RN 页面排查试点

用户已批准页面打不开、白屏或加载慢的结构化事件方案并要求直接落地、自验。随后指定 HermesDemo 模拟器验证，并于 2026-10-26 明确切换到 iOS 模拟器；本次以其真实 Module Federation MiniApp 为试点，不把它冒充 com.hxl.xlb.test 的生产业务验收。原业务工程及其未提交内容不修改。保留 Logan 已有资产、原始密文、解析记录和 UI 入口；不接阿里云或源码映射。不自动提交/推送。

## 数据契约

原生加密日志 content 中的对象只在 `schema=xlb.page-diagnostic` 且 `schemaVersion=1` 时识别。必需字段：eventId（稳定随机标识）、pageAttemptId（每次打开独立）、pageName=MiniApp、eventName、stage、outcome、occurredAtMs（epoch ms）、monotonicMs（本进程单调时钟）、source=native|rn、level=info|warn|error、module=page|bundle|render|network。可选字段：durationMs、errorCode、retryOf、scenario（normal|load_failure|render_failure|blank|slow）、testInjected（布尔）、dataState（content|empty|error|unknown）。ID 仅字母数字下划线连字符且长度<=128；不接任意消息、URL、参数、用户身份、请求正文或堆栈。

允许事件名：page_open_started、bundle_load_started、bundle_load_succeeded、bundle_load_failed、rn_mounted、content_ready、page_interactive、render_failed、page_suspected_blank、page_slow、page_abandoned。阶段 page|bundle|render|content|interactive；结果 started|succeeded|failed|suspected|abandoned。业务 ready 必须来自实际组件布局/就绪信号；超时仅疑似。慢日志可与最终成功共存，用户离开不算失败。重试产生新 attempt，retryOf 指向前次。

Go 保留 native time 原值，结构化后 time 采用明确校验的 occurredAtMs；保留 envelopeTime、originalLine 和原始 content。新格式损坏保留原文并标记诊断错误，不放宽 native 解码或让普通 JSON 意外变成诊断事件。旧日志解析不变；不重写旧资产/任务。动态信息不进入可分享报告。

## 交互与采集

HermesDemo 保留当前 Host/MiniApp/TurboModule 功能，增加 Debug 诊断入口和上传状态；Codegen 模块独立契约，通过仓库真实 Logan SDK 写文件并实际 HTTP 上传（iOS Objective-C/CLogan；已有 Android Java/JNI 实现保留），不用 JS/主机伪造原生文件。日志及上传配置在 App 私有目录持久化，App 只持有本应用上传令牌。真实成功链路与受控故障明确区分；测试开关只影响该 demo 新建的打开尝试，不影响原业务 App。采集失败不能崩溃或拖慢页面；有限容量、丢弃/暂停状态明确，不能沿用无限增长或不披露丢日志策略。

Web 在已有阅读页内增加按 pageAttemptId 的筛选和阶段摘要，展示开始/完成/失败/证据缺口、慢和受控故障标识，可跳到对应事件并加入原有调查证据。保留现有上传、解析历史、筛选、源码/AI/验证/回写入口；未配置 AI 不制造结论。按同一日志 run 分组，避免跨运行或跨进程同名页面误串；不以缺少 content_ready 直接宣判白屏。

## 验收

真实模拟器 App 正常打开并可交互；关闭 Remote 服务得到实际加载失败；受控渲染异常被边界捕获；延迟加载记录慢但最终成功；无业务内容就绪仅标记疑似白屏；离开标记 abandoned；重试新 attempt。每场景真实采集、App 上传、严格解析、Web 分组，故障注入单列不当作自然业务事故。重启 App/Go 后配置、原始文件、回执与诊断记录保留，旧业务资产仍在。自动测试覆盖隐私、字段协议、时间归一、混合旧日志、状态汇总及关联；UI/模拟器验收不能由合成 fixture 代替。
