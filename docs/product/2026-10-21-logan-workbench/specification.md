# 产品与数据规格

版本：开发基线 v1，2026-10-21。所有“应、必须、禁止”均为待实现要求；“现有”只用于有代码或验收记录支撑的事实。API 细节见 [接口契约](api-contract.md)，逐项验收见 [验收表](acceptance.md)。

## 1. 用户任务与页面结构

调查人员首先需要知道发生了什么，再找能支持或推翻原因的证据。日志阅读是主要工作区域，调查上下文贯穿全过程。顶部持续显示应用、环境、设备、版本、时间段、当前工单及证据范围。

三个一级入口：日志阅读（任务、导入、解析、阅读）、采集任务（推送、设备响应、文件、重试）、问题调查（工单、对话、证据、验证、回写）。进入对象立即显示实际内容；仅创建、选择证据和确认操作使用对话框，不用通用“选择对象→查看详情”代替工作页面。

阅读区域保留筛选、时间分布、上下文及选中位置；侧区在源码、AI、证据、补采、验证、回写之间切换。跳转引用可以返回原筛选和位置。窄屏按阅读与侧区顺序显示，保留当前对象，不挤出不可访问的操作。

## 2. 不得遗漏的能力

| 需求 | 保留或新增后的具体行为 | 开发阶段 |
| --- | --- | --- |
| LOG-01 | Native/Web 日志任务列表、搜索、时间筛选、状态及详情；旧任务仍能读取，区分两套 ID | A |
| LOG-02 | 本地上传 UTF-8 JSONL、JSON 数组、明文；Native/Web 协议文件由相应适配器处理；格式预检、进度、失败原因和取消 | A |
| LOG-03 | Web 样例载入及下载、来源标记、可关联源码的演示；样例与真实数据隔离 | A |
| LOG-04 | 查看解析进度、错误位置与原因、有效/无效数量；严格/跳过坏记录；重新解析及历史版本对比 | A |
| LOG-05 | 阅读、关键词/级别/类型/时间过滤、时间分布、原始字段、相邻上下文、请求关联；空结果与未知时间明确 | A |
| LOG-06 | 收藏与移出调查证据、原始文件/筛选结果下载、调查导出；保留出处、范围、截断及权限信息 | A |
| COL-01 | 从工单发起回捞，继承现场上下文，选择设备、时间、种类、预算、有效期和结果提醒 | B |
| COL-02 | 独立追踪渠道受理、设备送达、SDK 确认、文件上传、解析；真实事件与未知状态分开 | B |
| COL-03 | 部分成功、失败、过期、取消、迟到回执及重复回执；仅重试未满足范围并保留父请求 | B |
| COL-04 | 补采完成后返回原调查，展示新增证据及仍缺少的内容；站内提醒可读、可跳转 | B |
| SRC-01 | 日志与构建、仓库、commit、路径/行或符号关联；匹配、候选、缺失、不可访问分开 | B |
| SRC-02 | 源码片段、相邻上下文、版本差异、源码证据收藏；手动片段标记未验证 | B |
| INV-01 | 新建/编辑/查找工单、负责人、优先级、现象、上下文、关联任务与更新历史 | A |
| INV-02 | 自然语言提问、多轮追问、范围选择、补日志/源码/手动片段、停止和重试 | C |
| INV-03 | 每轮固定证据快照；事实、推断、反证、缺口、下一步和逐项引用；补采后比较增量 | C |
| INV-04 | 假设待验证/支持/反驳/无法确定，记录步骤、观察、证据；修复验证单独记录 | A/C |
| INV-05 | 从完成轮次生成可编辑草稿，预览、人工确认回写内部工单；冲突、重复提交、失败恢复 | A/C |
| SYS-01 | 项目隔离、分权限、脱敏、敏感内容到期处理、审计；外部连接未配置不伪造成功 | A |
| SYS-02 | 保留原型数据与旧接口/ID，显式迁移、回滚和灰度；有限资源与可观测性 | A/B/C |

阶段是开发顺序，不是删减范围。全部需求为本版工作台的交付范围；渠道、模型、源码供应方未就绪的功能必须展示未配置原因与替代入口。

## 3. 对象与身份

所有新 ID 使用字符串；旧 bigint ID 在 JSON 中也用字符串。应用、项目、环境是独立字段，不能由工单标题或文件名推断。任何对象关联都检查项目权限，不接受仅凭前端 ID 的跨项目访问。

| 对象 | 关键字段与约束 |
| --- | --- |
| Issue | id、projectId、revision、标题/现象/负责人/优先级、context、status、createdAt；context 包含 app/platform/environment/device/build/timeWindow，允许有明确 unknown 的字段 |
| CollectionRequest | id、projectId、可空 issueId、parentId、targetSnapshot、expectedIncidentContext、requestedWindow、effectiveWindow（能力决定的执行计划）、requestedKinds、capabilitySnapshot、budget、expiresAt、aggregateStatus；采集现场不可改，关联工单通过追加关联事件完成，变更目标需新请求 |
| CollectionEvent / File | eventId、requestId、attemptId、occurredAt、receivedAt、来源/验签结果；File 有 kind、hash、size、coverage、assetId、状态；事件只追加 |
| LogAsset | id、projectId、来源 import/native/web/collection/demo、rawHash、size、存储键、sourceRevision、expiresAt、legacyRef；原始内容不可覆盖 |
| ParseRun / LogEvent | runId、assetId、parserVersion、配置摘要、状态、计数、诊断；eventId 绑定 runId 和记录位置，含原始时间、UTC 时间、时区依据、原始字段、字段来源 |
| LegacyRef | kind=native/web、taskId、可选 detailId、sourceRevision；Native 123 和 Web 123 不相等，旧详情 ID 不重新编号 |
| BuildMapping / SourceRef | mappingId、project/app/platform、build、包摘要、repoId、commit、path、range、片段摘要、状态和匹配理由；不得仅按文件名认定精确匹配 |
| InvestigationContext | issueId、revision、成员 evidenceVersionId 列表；增删只影响后续轮次，不能覆盖历史快照 |
| EvidenceVersion | id、类别 log/source/manual/verification、originRef、内容摘要、版本、取得时间、脱敏策略、可用性、expiry；内容相同不等于同一次事件 |
| EvidenceSnapshot | id、issueId、contextRevision、固定成员、每条可用性与版本；不可变，权限及到期策略仍适用 |
| Turn / Claim / Hypothesis | turnId、parentTurnId、snapshotId、question、状态、模型/提示版本、retrievalManifest；claim 有 type/text/citations；hypothesis 有稳定 id 及版本，不因数组顺序复用 |
| Verification | id、issueId、hypothesisId/version（可空）、purpose=hypothesis/fix、步骤、预期、实际、结论、evidenceVersionIds、操作者及时间；修正采用追加记录 |
| TicketUpdate | updateId、issueId、原 revision、草稿版本、源 turnId、snapshotId、引用、验证、人工正文、状态变更及操作者；只追加历史 |
| Notification / Audit | 通知存状态、目标引用和已读时间；审计存动作、对象 ID、结果、操作者、traceId，不复制敏感正文 |

缺失字段不补成工单当前值；允许人工修正，但保留原值、修正人和依据。日志展示编号如 L5 仅为位置标签，不能成为“第 5 条必然是状态错误”的业务规则。

同一 requestId 只能作为关联候选；需要核对应用、环境、会话/设备、构建与时间边界。缺少这些字段时显示不确定，不跨请求拼成一次故障。时间接近不构成因果；没有错误日志不证明没有错误。

## 4. 日志导入、解析、阅读

上传前检查格式、数量和大小，展示将关联的工单及项目。文件选中不算上传成功；上传成功只表示资产已存储，解析完成后才发布可阅读事件。原型演示数据不得上传到真实项目，提供显式的演示空间。

自动识别失败时允许选择解析器；不能通过改扩展名绕过协议校验。首版通用上传不接受 ZIP、任意压缩包或可执行内容。Native/Web 加密协议交给已配置的协议适配器，缺密钥、格式不兼容等返回可操作错误，不回退为乱码文本。

ParseRun 状态：queued → running → succeeded / partial / failed / canceled；queued 也可 canceled。终态不可被继续修改。服务重启通过持久化租约恢复未结束工作；失去租约的旧 worker 无权发布结果。重试建立新 runId，保留旧解析器、参数、错误和事件 ID。

严格模式任一记录错误则不发布该轮事件；跳过模式仅对已验证的明文记录级格式错误生效，有效记录大于零且存在坏记录时为 partial。零条有效且有错误为 failed；空文件在导入时拒绝，合法但无事件的 JSON 数组等返回 EMPTY_RECORDS，不发布可读任务，沿用原型行为。解密、签名、CRC、资源超限等整体错误必须失败，不允许跳过继续。

每轮成功/部分结果以事务原子发布；运行中不能读到半批正式事件。解析版本切换只影响当前阅读，已有证据及旧轮次仍引用原 runId。旧源内容更新时标记 source_changed，保留旧证据版本；不能把旧详情伪装成新文件解析结果。

重新解析要求原资产仍可用；过期返回 SOURCE_EXPIRED，并提供重新上传/补采入口。新上传生成新 assetId，不冒充原文件。旧受控 Native 补数 CLI 不是页面重解析接口：不得重置旧任务、重排 detailId 或绕过其隔离要求。

阅读默认按可解释的时间顺序、同时间按 eventId 稳定排列；无时间的事件进入“时间未知”，不拿上传时间混成现场顺序。搜索、时间分布和导出使用相同 filterHash 与 runId；结果说明筛选范围、总数和截断。上下文查看围绕固定事件，不更改原筛选。

## 5. 补采与推送状态

采集任务可以独立存在，issueId 可空，不创建占位工单。从工单补采时带入现象、设备和故障时间作为线索；从采集中心新建时默认不关联。目标通过 project/app/environment + install/session 身份解析，平台和能力由目标记录提供，不能默认 Native。工单预期构建、设备最近已知构建、文件/事件实际构建分开保存；实际构建缺失或混合时保持未知，不能继承工单构建用于精确源码映射。关联工单不改采集快照；已解决工单须先重新调查，或独立采集后再决定关联。

希望获取时段、预计采集范围和返回材料的事件观察范围分别展示。创建时的 effectiveWindow 是执行计划；返回文件声明和解析事件时间须注明来源。首末事件不能证明连续覆盖，没有覆盖清单时保持未知。

创建前先读取设备能力：支持的日志类型、实际时间粒度、可回捞时长、最大体积、远程接收方式、在线状态及其更新时间。现有 SDK 上报接口不证明已经具备远程指令接收和 ACK。没有渠道/SDK 支持时保留创建表单，但提交前明确说明阻塞原因。

当前 SDK 按日获取/上报文件，不能默认承诺按分钟或按业务种类在端侧裁剪。请求时段与预计采集范围分别展示；如需扩大到整日，提交前明确确认预计范围。应用/网络分类仅在能力声明支持时显示为可采种类，否则显示混合文件、解析后筛选。Web 页面未运行时不能承诺后台送达；已增量上报并清理的内容可能不可回捞。

追踪五个独立维度，不能用一条线假装每个阶段都有回执：

| 维度 | 合法状态及含义 |
| --- | --- |
| 渠道 | not_sent / accepted / rejected / unknown；accepted 只代表通道接单 |
| 设备送达 | unknown / delivered / failed；渠道不支持回执时一直 unknown，可展示“后续收到 SDK 响应”但不伪造 delivered 事件 |
| SDK | pending / acknowledged / unsupported / no_data / failed / unknown；保留设备实际返回能力 |
| 文件上传 | 每文件 pending / uploading / stored / failed；stored 必须有服务端完整性校验及资产 |
| 解析 | 每文件对应 ParseRun，不复用上传状态表示解析成功 |

请求 aggregateStatus：queued / active / succeeded / partial / failed / expired / canceled。仅服务端派生：所有约定范围满足且可读则 succeeded；可读但有缺失则 partial；未结束但有文件先到仍 active；无可读结果且明确失败为 failed。到期/取消保留 expired/canceled 和已收到文件、coverage，不能隐藏部分产物。空范围无数据作为明确 no_data 结果展示，不等于成功取得证据。

合法转换为 queued → active/failed/expired/canceled，active → succeeded/partial/failed/expired/canceled。所有终态不可变；succeeded/partial 只能在范围已收齐或剩余范围已有明确终结结果后产生。无数据但请求已处理时，聚合为 failed，reason=no_data，界面显示“设备确认无可回捞日志”，不展示技术故障文案。进度百分比只在有确定文件数或字节数时提供，否则显示阶段及已等待时间。

重复提交用幂等键返回同一请求；检测相似进行中请求时返回候选，由用户选择复用或新建，不能仅凭设备/时间合并不同工单或预算。重试建立 parentId 子请求，只针对未满足范围，原请求终态及成功文件不改。每个 attempt 的令牌、有效期及回执去重独立。

终态后迟到回调记录为 late，不回退状态、不触发自动加入证据。已落地且校验通过的迟到文件可在授权人员复核后以独立资产显式关联；过期上传凭证直接拒绝。取消是停止后续工作请求，不能承诺撤销已送达的设备指令；界面保留“设备可能继续上传”的事实状态。

补采不会自动重跑 AI 或把整批日志塞入当前对话。完成通知返回原 issueId/contextRevision，展示新增文件、匹配范围、缺失及重复证据；用户选入下一轮后创建新快照。旧轮次与工单正文保持原样。

## 6. 源码身份

matched 要同时满足可信构建映射、应用/平台/构建身份和不可变 commit；准确行号/符号还需相应定位信息。只有构建映射但找不到具体位置时，显示“版本已匹配，位置待定位”，不得伪造行号。

candidate 表示关键词、文件名或附近版本候选；missing 表示无映射；unavailable 表示权限、仓库或内容失效。源码面板展示匹配依据与未满足条件。日志自报 commit、当前分支 HEAD、同名路径均不能单独证明现场版本。

源码比较明确两侧 commit、差异及是否属于故障构建；后来修复版本不能反向作为现场证据。日志跳源码须带 task/run/event 及 build 身份，返回能恢复原日志位置。人工片段和任意粘贴默认 unverified，可加入调查但不能升级成已匹配代码。

## 7. AI 调查与验证

一次提问包含问题、明确的当前范围和可选历史轮次。服务端在创建时原子固定 contextRevision 与 snapshotId；输出过程中补证不改变这轮输入。增量视图列出新增、移出、版本变化、反证及过期，旧内容不被覆盖。

Turn 状态 queued → running → completed / failed / canceled / interrupted；queued 可 canceled/failed。断线不自动视为服务端终止，重连查询原 turnId；服务端进程中断且不可恢复则 interrupted。重试生成新 turnId/attempt，保留相同问题和原快照，选择新证据需要显式发起新轮次。

每轮记录实际送入模型的证据清单（包含/排除/截断原因、查询、时间、摘要、权限、模型与提示版本）。引用只能指向本轮实际可用证据及确定片段；不能引用未发送的整份文件。历史回答是推断历史，不是新增事实。

回答结构必须包含：已知事实及出处、可能原因及推断依据、冲突/反证、证据缺口、可执行的下一步。这里展示的是可核对的解释摘要，不要求模型隐式思考过程。证据不足时保留 unknown；不显示无校准依据的“根因置信度 95%”。引用合法不等于推理正确，仍需人工核对。

反驳记录进入下一轮检索范围；若继续提出相同假设，必须说明新证据及旧反证为何不足。网络慢只能支持网络观察，不能凭一条请求推定 UI 状态未复位；候选源码不能确认根因；补采得到网络日志不代表补到了应用状态日志。

假设验证保存步骤、预期、实际观察、支持/反驳/无法确定及证据。假设支持与修复验证分开；修复验证需记录验证构建、场景、实际结果、执行人及证据。AI 可以建议验证计划，不能把建议标记已执行。

工单状态：investigating（调查中）、awaiting_fix（待修复）、awaiting_verification（待验证）、resolved（已解决）、reopened（重新调查）。investigating、awaiting_fix、awaiting_verification 三种状态间可人工调整并给理由；resolved 只允许从 awaiting_verification 且有有效修复验证进入；resolved 后只能 reopened；reopened 可转到前三种工作状态，不能直接再次 resolved。所有转换保留历史。等待补采作为调查子状态，不制造自动关闭路径。

从 AI 回写仅使用 completed 轮次；草稿包含源轮次、事实/推断/未知、引用、验证、建议状态与下一步，允许编辑且标记人工编辑。没有启用 AI 时允许人工调查更新，mode=manual、turnId=null，固定证据快照并明确作者，不能把未完成 AI 回答伪装为已完成结论。新增证据/验证或工单版本变化后提示草稿过期，必须重新预览确认。保存一次形成一个不可变更新；重复点击不重复写入。AI 完成、补采完成和假设支持均不自动关闭工单。

## 8. 权限、数据期限与外部边界

| 角色 | 默认权限 |
| --- | --- |
| 阅读者 | 访问授权项目的脱敏日志和工单；不能发补采、导出原始内容、调用模型或修改工单 |
| 调查者 | 阅读者权限，加创建调查、补证、提问、发起允许范围的补采、记录验证、保存调查更新 |
| 复核者 | 调查者权限，加核对修复验证和关闭工单；不默认获得所有仓库/原始日志权限 |
| 项目管理员 | 管理成员、连接和预算；不默认豁免内容访问限制 |

原始日志、导出、源码仓库、模型发送、保留延长是独立授权项。服务端在读取、检索、生成、下载和回写时重新检查。没有权限不能通过旧接口、对象 ID、缓存或导出绕过；旧接口需网关/网络隔离及项目映射，不能把前端隐藏当作权限控制。

日志与源码中的文本均为不可信数据，不执行其指令，不向模型暴露密钥，不允许模型任意访问 URL 或触发推送/写工单。外部模型必须项目显式配置，记录脱敏规则；不同用户的缓存不可泄漏数据。供应商访问和数据存放要求未确认时禁用外发。

建议试用内容保留 7 天、无正文审计 180 天，生产值待确认。内容包括原文件、解析行、证据摘录、模型输入/输出中的摘录、导出缓存，不能以“证据快照”名义无限延长。到期留下 ID、摘要、时间及 expired 标识，清理正文；旧引用显示过期而不是悄悄换成新日志。保留延长需明确权限、原因、截止日期及容量上限。

新资产与旧清理任务分开管理；被引用的旧内容必须按授权快照到工作台资产存储，再由工作台期限管理。不得靠删除保护标志令历史内容无限保留。源码权限撤销后也应阻断快照再次查看及模型发送；敏感内容撤回需覆盖派生存储。

## 9. 开发容量与兼容性

现有代码默认 HTTP body 32 MiB；Native 解码/查询还有行、条数、明文及暂存上限；这些不是生产容量承诺。原型的单文件 256 KiB、本地总量 2 MiB、最多 8 次解析属于浏览器演示限制，不搬成服务端规格。

建议通用上传试用配置：单文件 8 MiB、单批文件正文合计 16 MiB、最多 10 个文件，整个 multipart 请求仍受 32 MiB 或部署更小值限制。进度页显示有效上限。Native/Web 旧上报入口继续执行其自身限制，不因新 UI 限制改写旧协议。每次解析最多 100000 条；并发导入 2，超量排队并支持取消；所有限值均可配置且遇超限明确失败，不静默截断。

建议隔离试用的超时配置：采集有效期默认 30 分钟、可选 5 分钟至 24 小时；上传授权不晚于请求到期；解析运行上限 30 秒、排队上限 5 分钟；AI 运行上限 120 秒、排队上限 5 分钟。渠道重试仅对明确可重试失败使用同 attempt、最多 3 次并受总有效期限制；发送结果 unknown 时先查渠道状态，不盲目重发。真实 SDK/渠道联调后调整这些建议值，超时必须产出可查询结果而不是无限加载。

性能目标：固定隔离环境、记录硬件/数据集/索引条件，10 并发阅读、2 导入任务、每 run 最多 100000 条下，第一页及常用筛选 p95 ≤ 2 秒；测量至少 100 次请求，冷缓存与热缓存分别记录。此为待测目标，未测量前不宣称达标。

迁移仅新增工作台表和资产命名空间，显式审查与执行；不启动时自动建表、不重编号旧数据、不双写 Java/Go。旧记录通过只读适配器读取；没有项目归属的旧数据先进入受限映射流程。原型 localStorage 保留，未明确导入前不自动发送到服务器；演示数据永不混入真实问题。

灰度按项目启用。回滚关闭新入口/写入开关并保留新增记录，旧日志接口继续可用；禁止用删表回滚。新请求、解析、AI 和回写都有 traceId、审计、超时及幂等记录；观测不记录敏感正文。

## 10. 代码事实来源

| 来源 | 本包使用的事实 |
| --- | --- |
| [旧路由](../../../Logan/ServerGo/biz/router/legacy.go) | 20 条兼容路由，不能直接视为新工作台接口 |
| [启动与就绪检查](../../../Logan/ServerGo/bootstrap.go) | readyz 仍保留集成/生产验证未完成的限制 |
| [数据模型](../../../Logan/ServerGo/internal/repository/models.go) | Native/Web 任务/详情分开；缺少新调查对象与统一 build 来源 |
| [服务配置](../../../Logan/ServerGo/internal/config/config.go)、[Native 查询配置](../../../Logan/ServerGo/internal/config/native_query.go)、[清理配置](../../../Logan/ServerGo/internal/config/maintenance.go) | 资源和保留期代码默认值，不是生产服务目标 |
| [Native 处理任务](../../../Logan/ServerGo/internal/repository/native_jobs.go) | 现有租约、fence、发布事务可作为新任务机制参考 |
| [受控重新解析](../../../Logan/ServerGo/docs/native-reparse.md)、[Web 行为](../../../Logan/ServerGo/docs/web.md) | Native 补尾工具边界、Web 源文件与旧详情版本差异 |
| [iOS SDK](../../../Logan/iOS/Logan.h)、[Android SDK](../../../Example/Logan-Android/logan/src/main/java/com/dianping/logan/Logan.java)、[Web 配置](../../../Logan/WebSDK/src/interface.ts) | 有上报能力不代表已实现远程控制、分钟裁剪和完整送达回执 |
| [现有前端](../../../Logan/LoganSite/src/app.js)、[API 适配](../../../Logan/LoganSite/src/common/api.js) | 旧页面和业务响应约定需要保留 |
| [最近 SDK 修复验证](../../../Logan/ServerGo/docs/ios-empty-frame-fix.md) | 已有隔离环境和模拟器证据，不能替代真实渠道、云存储及生产验收 |
| [原型能力](../../../prototypes/logan-investigation/capability-map.md)、[连续工作台验收](../../../prototypes/logan-investigation/evidence/workspace-redesign-2026-10-20/report.md)、[推理验收](../../../prototypes/logan-investigation/evidence/reasoning-audit-2026-10-20/report.md) | 已有能力保留与状态逻辑基线；不是服务端实现证明 |

## 阿里云回捞入口对齐

回捞与日志解析分开建模。接收日志并关联请求后回捞成功，解析失败不会撤销该结果。目标输入、工单带入和未确认协议的边界以 [阿里云回捞流程](aliyun-retrieval-flow.md) 为准；此前合成绑定和命令载荷不作为开发协议。
