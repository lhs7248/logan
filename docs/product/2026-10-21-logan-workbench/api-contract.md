# 接口与交互契约（待实现）

本文件定义新增工作台接口，不声明已有服务支持这些路径。服务默认 context path 为 `/logan-web`；新接口基址建议为 `/logan-web/api/workbench/v1`，下文路径均相对于它。旧 `/logan/...` 兼容路由和原响应格式不变。概念、状态与数据保留按 [产品规格](specification.md) 执行。

## 1. 通用约定

- 全部请求有身份认证；projectId 由授权上下文验证，不能只信客户端。未知项目与无权读取的对象统一返回 404，避免泄漏存在性；已授权对象的动作权限不足返回 403。
- ID 均为字符串。时间使用带偏移 ISO 8601，服务端存 UTC；请求窗口采用 `[start,end)`。显示时间保留原时区；未知时间用 null 加 reason，不能生成当前时间代替。
- 成功返回 `{data, meta:{traceId}}`。分页为 `{data:[],meta:{traceId,nextCursor,filterHash}}`；游标固定排序、解析版本及过滤条件，变更过滤须重置游标。
- 创建已持久化对象返回 201；接受异步任务返回 202 和可查询 operationId/resourceId，并不表示执行成功。查询返回 200；取消返回 202（已提出取消）或 200（已确认终态）。
- 错误为 `{error:{code,message,fieldErrors,retryable},meta:{traceId}}`。前端按 code 决定操作，不依赖中文错误文本。
- 所有写操作使用 `Idempotency-Key`，新 API 约定为带创建时间的 UUIDv7。键按项目、操作者、操作类型隔离，绑定规范化请求摘要；同键同内容返回原结果，不同内容 409 IDEMPOTENCY_CONFLICT。数据库唯一约束和结果写入同一事务，不用内存缓存去重。
- 幂等键从创建起有效 7 天，记录至少保留至该期限；未来超过 5 分钟的键拒绝为 INVALID_INPUT。服务端即使已清理记录也能依据键时间拒绝过期键为 IDEMPOTENCY_EXPIRED，不能当新操作重放。客户端超时不表示失败，先查操作；退避重试使用同键，过期后先查业务对象再决定新操作。明确的用户新尝试使用新键并关联原 attemptId。
- 修改可变资源使用 `If-Match: "<revision>"`；缺失 428 PRECONDITION_REQUIRED，版本不符 409 REVISION_CONFLICT 并返回最新可读版本。多个副作用必须在事务内写入，不出现工单变了但历史未写的结果。
- 下载授权与读取权限相同，令牌短期有效且绑定用户/项目/对象；不得公开永久对象存储链接。大导出异步执行，完成时再次校验权限和内容期限。

| HTTP | 常见错误码与交互 |
| --- | --- |
| 400 / 422 | INVALID_INPUT、INVALID_WINDOW、UNSUPPORTED_CAPABILITY：就地标出字段，保留输入 |
| 401 / 403 / 404 | AUTH_REQUIRED、FORBIDDEN、NOT_FOUND：不返回敏感正文，不靠切换旧 API 绕过 |
| 409 / 428 | REVISION_CONFLICT、DRAFT_STALE、INVALID_TRANSITION、IDEMPOTENCY_CONFLICT、PRECONDITION_REQUIRED：刷新差异或重新预览，禁止自动覆盖 |
| 410 | SOURCE_EXPIRED、EVIDENCE_EXPIRED、IDEMPOTENCY_EXPIRED：解释失效原因，提供新导入/补采或查询入口 |
| 413 / 415 | LIMIT_EXCEEDED、FORMAT_UNSUPPORTED：展示有效限制，不静默裁剪后声称全量成功 |
| 429 | QUOTA_EXCEEDED：显示队列/预算与 Retry-After；不自动产生新采集或模型任务 |
| 503 | CONNECTOR_UNCONFIGURED、DEPENDENCY_UNAVAILABLE：提示连接状态及可用替代操作，不生成假回执 |

旧接口存在 HTTP 状态与业务 code 不等价的行为，适配器应逐项解释；禁止把 HTTP 200 或下载 201 统一解释成新接口操作成功。

## 2. 接口清单

以下清单为必要接口边界，实际实现可以共享内部服务，但不可删掉用户结果或审计。

| 路径 | 输入 / 输出及主要约束 |
| --- | --- |
| GET /capabilities | projectId、appId、deviceId → 可用连接、设备能力、有效限额、能力观测时间；unknown 与 false 分开 |
| GET /operations/{id} | 返回状态、resourceId、错误及幂等键有效期；只向原项目授权用户开放 |
| GET /operations?key={key}&operation={type} | 响应丢失、尚未取得 operationId 时按当前用户/项目查询幂等结果；只能查自身授权操作 |
| GET /log-tasks | Native/Web/import/collection 列表适配；返回 origin、legacyRef、assetId、可读 runIds 和 source_changed |
| POST /imports | multipart 文件与 metadata；预检、完整性校验后创建批次/资产，返回 201 或批次逐文件拒绝原因；不自动视为解析成功 |
| GET /imports/{id} | 每文件接收、校验、资产、错误；页面刷新恢复处理结果 |
| GET /imports?clientBatchId={id} | 查询断线前批次结果；clientBatchId 在项目/操作者内唯一 |
| POST /imports/{id}/cancel | 停止接收未完成文件；完整资产保留并标明结果，取消不删除已生成解析或证据 |
| GET /demo-fixtures | 合成样例清单和下载；POST /demo-fixtures/{id}/load 只允许演示项目，写入带 demo 标签的新资产 |
| POST /assets/{id}/parse-runs | parser、mode、期望 rawHash、可选 retryOf → 202 runId；拒绝过期/版本冲突/不支持的解析器 |
| GET /assets/{id}/parse-runs | 历史配置、计数和错误，不能只返回最新一轮 |
| GET /parse-runs/{id} | 状态、进度、诊断位置、发布计数、parserVersion；计数只在完成发布后标记 final |
| POST /parse-runs/{id}/cancel | 取消队列或发送运行取消信号；原子发布已完成则返回终态，不撤销已发布记录 |
| GET /parse-runs/{id}/events | query、level、type、start/end、cursor → 固定版本事件；有 unknownTime 开关 |
| GET /parse-runs/{id}/histogram | 与 events 一致的 filterHash 和范围；结果含时间未知数量 |
| GET /events/{id}/context | before/after 有界数量，固定 runId；原始内容按权限脱敏 |
| GET /events/{id}/related | 返回请求/会话关联候选、匹配字段与缺失字段，不直接断言因果 |
| POST /exports | kind=raw/events/investigation、固定对象与 filterHash → 异步导出任务；GET /exports/{id} 查结果，GET /exports/{id}/content 授权下载 |
| GET /issues | query/status/owner/cursor，按授权项目过滤 |
| POST /issues | 标题、现象、优先级、负责人、context → 201 issue/revision；允许未知字段但明确缺口 |
| GET /issues/{id} | 当前内容、revision、关联采集/任务/调查摘要 |
| PATCH /issues/{id} | If-Match + 编辑字段/状态/理由；关闭状态需修复验证引用及复核权限 |
| GET /issues/{id}/updates | 只追加的更新记录与证据快照引用；到期内容保留 tombstone |
| POST /issues/{id}/log-links | 关联已有授权任务/资产；检查上下文差异，需显式确认差异，不修改原资产身份 |
| POST /collections | 见补采示例；创建前固定能力和现场快照，返回 202 |
| GET /collections | issueId/status/deviceId/cursor；支持失败和部分成功视图 |
| GET /collections/{id} | 各状态维度、覆盖范围、文件、父子请求、终态理由 |
| GET /collections/{id}/events | 使用游标持续查询真实事件，断线可恢复，不依赖前端计时器推进状态 |
| POST /collections/{id}/cancel | 服务端请求取消，显示已无法撤回的阶段 |
| POST /collections/{id}/retries | failedScope、原因、预算/有效期 → 新子请求；已满足范围默认排除，扩大范围需确认 |
| GET /source-mappings | app/platform/build/packageHash → 可信映射及版本状态；无权限不回传路径 |
| POST /source-resolutions | eventIds、mappingId 或搜索条件 → matched/candidate/missing/unavailable，逐结果解释；只读操作但需防滥用限额 |
| GET /source-fragments/{id} | 不可变 commit、path、range、hash、版本状态及受限上下文 |
| GET /source-diffs | leftRef/rightRef → 两个不可变 commit 的差异；拒绝任意 URL 和未授权仓库 |
| GET /issues/{id}/context | 当前 contextRevision、成员、可用性、缺口及选入来源 |
| POST /issues/{id}/evidence | originRef 或 manual 内容、类型、说明；服务端创建 EvidenceVersion；手动来源默认 unverified |
| PATCH /issues/{id}/context | If-Match + add/remove evidenceVersionIds；仅改变未来轮次范围，不删除证据历史 |
| GET /evidence-snapshots/{id} | 固定成员和状态；内容仍受当前权限和期限检查 |
| POST /issues/{id}/turns | question、parentTurnId、contextRevision、期望成员摘要 → 202 turn/snapshot/operation；服务端原子固定快照 |
| GET /issues/{id}/turns | 历史轮次、版本、问题与状态；分页保持父子关系 |
| GET /turns/{id} | 完成的结构化回答/检索清单或运行进度；部分流式文字标记 provisional |
| POST /turns/{id}/cancel | 取消本轮；不得将截断文字变成 completed |
| POST /turns/{id}/retries | 只允许失败/取消/中断轮次，原问题及原快照，new turnId + retryOf；证据过期则拒绝 |
| POST /issues/{id}/verifications | purpose、hypothesis/version、步骤、预期、实际、结论、证据；required 字段按用途校验 |
| GET /issues/{id}/verifications | 追加记录及 supersedes 关系；不覆盖历史观察 |
| POST /issues/{id}/handoff-previews | mode=ai 选 completed turn；mode=manual 不携带 turn；两者均需 issueRevision、contextRevision、verificationRevision、编辑内容 → draftId/version/hash、差异及阻塞项 |
| POST /issues/{id}/handoffs | If-Match + draftId/version/hash + confirmed；见回写示例，原子形成更新 |
| GET /notifications | unread/issueId/cursor → 站内事件及跳转目标，含真实/演示来源 |
| POST /notifications/{id}/read | 幂等已读；不触发额外补采或修改工单 |

管理连接、项目成员、构建清单接入及授权需使用独立管理接口；以上公共调查接口不能直接写入密钥、任意仓库地址或自行声明可信构建。第一版外部工单适配器可为空，内部工单必须可工作。

批次上传中断用浏览器取消网络请求停止传输；若批次已落地，使用取消接口。所有新写接口同时返回 operationId，即使同步返回 201，响应丢失后也能按幂等键查询；异步操作的成功状态必须指向已持久化业务结果。

## 3. 上传与解析样例

`POST /imports` 的 metadata 至少包含：projectId、clientBatchId、可选 issueId、每文件 clientFileId/name/size/sha256/declaredFormat、显式 app/platform/environment/device/build 信息及 unknown 字段。服务端重算摘要，不信任客户端 hash；文件名仅展示，不能当存储路径。

批次预检失败不开始存储；接收过程部分文件失败时返回每文件结果，已接收资产保留并可继续解析。相同幂等键重复上传返回既有结果；补交失败文件使用新操作并关联原批次，不能重复解析成功文件。断网后用 clientBatchId 查询批次，未收到完整请求的文件不产生正式资产。

```json
{
  "parser": "utf8-jsonl",
  "mode": "skip_invalid_records",
  "expectedRawHash": "sha256:<原始文件摘要>",
  "retryOf": "parse_previous"
}
```

parser 逻辑名由服务端可用解析器列表决定；解析器实际版本写入 ParseRun。前端不能任意上传解析代码。示例中的摘要为说明性占位值，服务端须校验完整 SHA-256 格式。

结果示例：`state=partial, validCount=4, invalidCount=1, publishedCount=4`，诊断包含行号、错误码、脱敏片段。切换严格策略生成新 run，不能覆写当前 run。下载原始文件、导出解析结果和导出调查是三个不同操作，不能都返回同一段演示文本。

## 4. 补采样例与回调协议

```json
{
  "issueId": "iss_1047",
  "issueRevision": 8,
  "deviceId": "demo-A17",
  "appId": "sample-app",
  "platform": "android",
  "environment": "test",
  "buildId": "build_42",
  "requestedWindow": {
    "start": "2026-10-19T10:40:00+08:00",
    "end": "2026-10-19T10:50:00+08:00"
  },
  "requestedKinds": ["mixed"],
  "capabilityRevision": "cap_12",
  "acceptedEffectiveWindow": {
    "start": "2026-10-19T00:00:00+08:00",
    "end": "2026-10-20T00:00:00+08:00"
  },
  "maxBytes": 8388608,
  "expiresAt": "2026-10-21T18:00:00+08:00",
  "notification": "in_app"
}
```

此例表示设备只支持按日采混合文件，用户已看到并接受范围扩大。服务端复核能力版本、有效期、数据仍可获取、字节预算和工单版本；过期或能力变化返回错误，不静默换用其他设备或更大范围。

回调使用隔离的集成入口，不走普通用户身份。每个回调至少含 requestId、attemptId、eventId、设备身份、eventType、occurredAt、nonce、协议版本及签名；服务端检查签名/有效期/重放/关联身份，再追加事件。供应商未提供的字段标 unknown，不能臆造。

文件上传授权绑定 requestId/attemptId/fileId/预算/有效期；最终 stored 必须由服务端验证字节数、摘要和资产落地，不能只信客户端“上传成功”。回调乱序按事实保存，用服务端 receivedAt 和阶段规则归并；不把缺失阶段补成伪事件。整条请求只能由服务端根据文件和解析结果确定终态。

同一 eventId 同内容不重复应用；同 ID 不同内容拒绝并审计。已取消/过期请求的 late 事件不再改变状态。重试创建新 attempt 和子请求，不能让旧 ACK 激活新请求。

## 5. AI 输入、输出与证据检查

```json
{
  "question": "请求已经返回，页面为什么仍在加载？还缺什么证据？",
  "parentTurnId": "turn_5",
  "contextRevision": 12,
  "includeSource": true,
  "expectedEvidenceVersionIds": ["ev_network_1", "ev_source_candidate_1"]
}
```

服务端比较成员及 revision，拒绝陈旧输入；固定 snapshot 后按权限脱敏和预算生成 retrievalManifest。字段 includeSource=false 时必须排除源码正文，并在清单记录排除项。父轮次只能来自同一工单的 completed 历史，不把其生成文字当现场事实。

每个调查同一时刻仅允许一个 queued/running 轮次，第二次发送返回 409 TURN_IN_PROGRESS 并提供可见的 turnId；前端保留未发送问题。人工补证和验证可以继续，下一轮使用更新后的范围。开始真实模型请求前再次检查内容期限及发送权限，排队期间权限撤销不能继续发送。

```json
{
  "state": "completed",
  "snapshotId": "snapshot_13",
  "claims": [
    {
      "id": "claim_1",
      "type": "fact",
      "text": "选中的网络日志记录了请求返回。",
      "citations": [{"evidenceVersionId": "ev_network_1", "locator": "event:event_21"}]
    },
    {
      "id": "claim_2",
      "type": "hypothesis",
      "text": "需要检查返回后的状态更新路径；现有候选源码不足以确认现场行为。",
      "citations": [{"evidenceVersionId": "ev_source_candidate_1", "locator": "lines:20-28"}]
    }
  ],
  "conflicts": [],
  "gaps": ["缺少同一请求的应用状态日志", "缺少现场构建到 commit 的可信映射"],
  "nextActions": ["补采该请求的应用日志", "补充构建映射后再次定位源码"]
}
```

此示例仅展示结构，不固定生产回答。服务端校验：引用属于本轮 manifest 且片段存在；candidate/unverified 不得在结构字段中升级 matched；缺失/过期/无权证据不可发送或引用。结构校验不通过时记录失败或明确标记不完整并要求重新生成，禁止直接完成并回写。

检索清单保存实际选中与截断项、选择原因、模型输入摘要和策略版本；正文随内容期限清理。观察指标包括引用失效、无证据归因、反证遗漏、范围错误，不能只统计请求成功率。

## 6. 验证、回写和冲突

验证记录至少含 purpose、步骤、预期、实际、结论及证据。purpose=fix 时还需验证构建、场景、执行人；结论 pass/fail/inconclusive 与 hypothesis 的 support/refute/inconclusive 分开。记录修正使用 supersedesVerificationId，不擦除旧结果。

mode=ai 的预览从指定 completed turn 生成草稿；mode=manual 从人工填写内容生成，turnId 必须为 null，保存作者及人工来源。两种模式都固定 issue/context/verification revisions 及 evidence snapshot，标明遗漏、过期、候选来源和人工改动。内容变化重新生成 draftVersion，不能对旧 hash 签确认。不能通过自动切换 manual 模式规避失败或未完成 AI 轮次校验。

```json
{
  "draftId": "draft_7",
  "draftVersion": 2,
  "expectedContentHash": "sha256:<预览内容摘要>",
  "confirmed": true,
  "requestedStatus": "awaiting_verification",
  "fixVerificationId": null
}
```

提交携带 If-Match 和 Idempotency-Key。事务内复核工单、上下文、验证版本及当前证据可用性，写入更新记录、关联引用、状态与审计后才返回 201。任一步失败全部回滚；超时用 operationId 或幂等键查询结果，不能盲目再次写回。

请求 resolved 时，服务端要求前态 awaiting_verification、有最新有效 pass 修复验证、无未处理的相反验证及复核权限，并关联所验证构建。仅有 hypothesis support、模型建议或“我已核对”勾选框不够。

冲突时显示原草稿与最新工单差异，保留用户编辑，重新预览确认；不得最后写入者自动覆盖。后续若接外部工单，增加 outbox 和外部更新 ID、pending/succeeded/failed/unknown 状态，在外部成功确认前不展示“已写入外部”；本版内部保存成功不能冒充外部同步成功。
