# 实验 Pattern 来源准入核心

这是仅供内部接线的 `PatternService` 软件切片。没有新增 HTTP/CLI/MCP/Web 执行入口，没有默认图案目录或 Store adapter，也未安装或验证实体设备。完整来源验证仍由未来外层 P1 adapter 负责；pure typed digest 见 [P4a 说明](README.pattern-execution.zh-CN.md)。

## 输入与身份

`SubmitPatternWithReceipt(ctx, serial, ref, policy, ticket, resolver)` 接受完整原票据、精确 `patternidentity.Ref{record_contract,id,revision}`，及显式 `replace` / `queue` / `reject`。不接受空 policy 或隐式 latest。客户端可明确选择 reject；core 保留现有三政策。

`resolver` 返回 `ResolvedPattern{Ref,Execution,ExecutionDigest}`。它只在首次回执缺失且基本准入可行时运行，每次调用最多一次、运行时不持 Engine 锁。它必须完整验证已知来源/recipe 与冻结 Record、核对原执行 contract，并关闭 Store 事务。core 不能凭短 Ref 独立证明作者 Action，因此该 callback 是内部信任边界，不能直接暴露为用户提交裸 Program 的接口。

core 再次要求返回 Ref 精确相等，使用 P4a ValidatePatternExecution 验证规范执行体并深拷贝，重算固定 P1 域的执行摘要并比较预期值；不会接受“修复后的新摘要”替换来源。既有 Action/Program/Frame 语义、P1 canonical/revision/digest 都不变。

私有 record 的 origin 与执行 kind 独立：Pattern 来源拥有不可变 Ref、execution_digest、原 admission policy；kind 仍为顺序或帧。来源记录与 Job、receipt、同一 worker queue 在最终 Engine 锁内一次发布，不调用旧 Submit 后补 side-table，不引入 in-flight reservation。

## 回执、错误与政策

1. 先按原 serial/request_id 查共享 receipt。已接受精确 Ref+policy 直接返回原 envelope；此时不依赖 resolver、当前目录、设备在线或 runtime 仍开放。
2. 仅新 key 锁外 resolve 和纯校验。多个并发调用可各自 Resolve，但同 key 至多准入一次。
3. 最后同锁再查实例、原 key、容量、worker/cached 能力、队列、ctx 与 deadline；所有校验/随机 ID 成功后才发布和执行 replace 的取消动作。ID 生成返回后再次检查 ctx 和单调票据 deadline，失败不抢占旧任务。

`PatternResolveError` 保留原 error：只有显式 `availability` 可在最终二查时让位于并发接受的精确 Ref+policy。integrity、unsupported、configuration、context 与未知 error 均不被好回执掩盖。unsafe/corrupt 必须在 adapter 中优先归完整性；不能把任意 I/O error 或普通 error 当作暂时性。多分支错误中的完整性/context、未分类并列分支同样不能藏在 availability 后。

callback 忽略 context 并返回成功/availability 时，最终同锁仍检查 ctx，保留本调用的取消诊断。原票据可另外显式查询；本次取消等待不表示别的并发调用未接受任务。

same key 不同来源、policy、kind/执行摘要冲突；legacy Program/Frame 的 key 也不能与新 Pattern 复用。旧 receipt fingerprint 的原字节和域不变。共享4096条、65536 stored steps/targets、单调窗口/retention、active pin、history 淘汰规则不变；metadata 不保留任意作者全文。

queue 跨来源 FIFO 且受同一容量限制；replace 只在所有可能失败的准入工作完成后取消先前 active/queued，旧 active 完成 native 返回及清理后新任务才能接触 backend；reject 不抢占。

## 返回与查停

独立 `PatternJobEnvelope` 序列化为 `{pattern_ref,execution_digest,job_kind,job}`。job_kind 为 program/frame，恰好含原 Job/FrameJob 中一种，原字段形状不加 provenance。全部程序切片、targets 和时间指针均为副本。admission 另外返回 Replayed/DaemonInstanceID；它们不是裸 Job 字段。

`PatternRequest(serial,ticket,ref)` 与 `CancelPatternRequest(ctx,serial,ticket,ref,knownID)` 仅按原 receipt 查记录，不依赖 Store。knownID 是可选附加核对，不能替代票据；取消校验和 mutation 在同一锁内。库丢失/损坏、history 已淘汰、runtime 关闭不剥掉已接受来源；过期或实例更换不重建任务。

旧 Job/FrameJob/Request/FrameRequest/Cancel/CancelFrame 在 Pattern origin 上返回 not found，不能剥来源后返回裸任务。旧 replace 提交仍可通过共用 worker 取消 Pattern，这是设备所有权语义，和旧 ID-only getter/cancel 的隔离不同。

active 取消只进入 cancelling；仍持 native handle/lease，原生调用不可被 host ctx 强制中断。只有 worker Stop(off)+Read 成功后才 cancelled，清理失败为 failed。queued 取消没有 Set；取消已经 succeeded 的 hold 任务不操作设备。Frame 仍用原 scheduler，运行期间不插入周期 Read。

## 软件验证边界

测试只用内存 fake Backend、channel barrier、注入 clock/random；无 HID 枚举、USB、摄像头、runtime 子进程或真实用户库。覆盖两执行类型×三政策并发、来源/政策/执行冲突、完整性诊断、旧裸接口隔离、跨来源 FIFO/replace、失败替换零抢占、native/hold清理取消、history/retention/restart/close、深拷贝和共享预算。

current、Go1.18、race、vet 的最终来源与原始日志见工作区 `artifacts/pattern-v2-admission/`。首轮通过记录与独立审查发现的 context 回归失败/修复记录保留，不能把首轮旧覆盖当成最终验收。软件通过不扩大 Linux/Windows USB、旧 OS、MK3 或原生硬超时支持范围。
