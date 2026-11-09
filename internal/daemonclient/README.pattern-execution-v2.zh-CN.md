# Pattern v2：固定引用客户端与 CLI 执行（实验）

此片接上已经保存的高级 Pattern：preview、run、原票据 request 和 cancel。它保留作者记录的完整 Ref、冻结执行摘要和 typed Job/FrameJob，复用 nb1 的设备所有者与 worker。仅 daemon 模式，无 USB/direct 回退、用户库隐式选择、自动重播或系统权限变更。`internal/daemonclient` 属于 nb1 内部接线，不是外部模块可直接 import 的独立 SDK。

## 命令

已有 `control patterns-v2 get ID`、`preview-draft FILE`、`save FILE` 保持原语义；save 不播放。新增：

```text
nb1 control patterns-v2 preview ID --revision SHA [--serial SERIAL]
nb1 control patterns-v2 run SERIAL ID --revision SHA [--policy reject|queue|replace] [--request-id KEY --expected-daemon-instance-id INSTANCE] [--wait]
nb1 control patterns-v2 request SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE [--job-id ID] [--execution-digest SHA] [--wait]
nb1 control patterns-v2 cancel SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE [--job-id ID] [--execution-digest SHA] [--wait]
```

SHA 为完整 `sha256:` 加64位小写hex，取自已保存记录；不猜 latest。record_contract 固定 `nb1-pattern-record/experimental-v2`，并在请求、响应与输出中完整保留。ID 不能代替 revision；execution_digest 也不能代替作者记录 revision。

新 Pattern run 的有效默认策略是 reject；显式接受三种策略，不修改旧 control 的 replace 默认，也不改变旧 light/raw Frame 仅reject的接口。preview/作者命令不接受 policy/wait/票据；request/cancel要求原票据且不接受policy；--mode direct全部拒绝。list/import/export由后续切片完成。

## 一次提交与原票据恢复

run 缺票据时仅 Prepare 一次，已有完整票据时不 Prepare；执行只发一次 POST，不先 Get/preview 库。因此库消失、配置失败时，核心仍有机会重放已接受的精确旧票据。没有自动换实例/换票据、低层程序回退或失败补播。

输出包含 pattern_ref、request_ticket、accepted、outcome_unknown、daemon_instance_id、replayed 和 job。job 为核心 Envelope `{pattern_ref,execution_digest,job_kind,job}`；job_kind=program/frame，内层保留原 Job/FrameJob 形状。accepted 说明原任务已被确认接受，不表示灯光完成。后续查询/取消输出也用 accepted 表示原任务已有可信回执；cancel_attempted另外表示是否尝试过取消POST。

--wait 只查原 Pattern receipt，固定原 Ref/serial/实例/request_id，首次可信结果后固定Job ID和摘要；每次比较完整冻结执行、创建/启动时间、进度与生命周期。timeout不cancel、不重新提交，不用历史或裸Job猜结果；已验证terminal先结束等待，随后context结束不把已知终态改成unknown。任何查询/完整性错误立即保留原票据和最后可信快照，不由随后好结果掩盖。run/request等待到failed/cancelled可返回非零，但可信终态仍是已知结果，outcome_unknown=false。

取消先 GET 原回执并验证可选已知ID/摘要，terminal/cancelling零POST；其它状态最多一次带confirmed_job_id的POST。取消202可仍cancelling；native写入与清理要由worker完成，终态hold再取消不关灯。取消响应丢失或不一致时保留before快照和cancel_attempted；本方法不再自动GET来抹掉错误。用户可以之后明确request原票据；--wait只在此前操作已成功验证时继续原回执读取。

## 客户端验证与边界

内部方法为 `PreviewPatternRefV2`、`SubmitPatternV2WithReceipt`、`LookupPatternV2Request`、`CancelPatternV2Request`，以及 `ValidatePatternV2Progress`。执行响应解码为严格闭合的 Program/Frame union，保留零字段，拒绝null/unknown/duplicate/Unicode损失，调用 `control.ValidatePatternExecution` 和固定域 Digest/VerifyDigest。Frame target写入进度必须与帧进度相符；时间戳、状态、错误字段及终态不变性共同验证。failed必须有finished_at，但后端错误文案可能为空，不能因可选error缺失把真实失败终态误判为unknown。导出对象是可变Go数据，跨快照比较会重新验证两端。

客户端验证的是冻结执行摘要与请求/回执绑定。它不凭Ref在本地重建作者source，也不在run前读取库；完整source/recipe/revision验证由服务P5与core执行。引用preview的Program/Frame纯事实重新计算，可选缓存设备证据必须回显所选serial，未选则不得出现device或执行承诺。统计与缓存都不是物理效果或墙钟时长验收。

提交只有明确的status/code白名单可报告确定拒绝：400 invalid_pattern_request/invalid_request；404 pattern_not_found；409 busy/queue_full/idempotency_conflict/pattern_revision_conflict/pattern_library_busy；429 idempotency_capacity。未知code/错status、普通404、3xx、5xx/501、过期、实例变化、传输/解析/close失败保持unknown；查询或已开始取消的错误一律保留原结果不确定性。错误body必须严格JSON、非空message且无矛盾成功headers。即使确定拒绝，也不授权自动生成新票据重试。

## 软件验证范围

测试通过真实P6 handler、P5 verifier、临时Store和注入内存Engine验证Program与Frame链路，包含库消失/错误配置后的重放、原请求查询/取消及单次目标调用。其它用受控HTTP fixtures注入坏报文、错误metadata、lifecycle退回与错误code/status。

等待取消通过“原回执GET已到达”通道握手触发caller结束，避免把Prepare+POST预算误当成已接受后的等待阶段；另有已过期deadline的独立等待单测。测试只用私有loopback fixture listener与内存驱动，不连日常daemon、不读用户库、不操作USB或摄像头、不安装服务。Go1.18/current/race证据随独立artifact固定，不据此宣称新代际或其他系统实体设备通过。
