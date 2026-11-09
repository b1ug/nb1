# 高级 Pattern 创作客户端

三个 HTTP 客户端方法为 `GetPatternV2`、`PreviewPatternDraftV2`、`SavePatternV2`。CLI 以 `nb1 control patterns-v2 get ID`、`preview-draft FILE`、`save FILE` 接入独立实验 v2 库。它们不播放、不准备执行票据、不新增 MCP 工具，不接受服务器返回的本地路径，也不访问 USB。旧 `control patterns` 仍使用 v1 格式。

`preview-draft` 输入为 `{id,title,source}`；`save` 还要求精确 `expected_revision`，应取自同一份作者预览结果，不能凭 ID 猜测。保存错误 JSON 保留 `pattern_ref` 与 `publication_unknown`；只有完整确认才标记 `publication_confirmed:true`。这三个命令仅使用 daemon 模式，不接受 policy、wait 或执行票据参数。

作者预览与保存复用 `patternsv2.PreviewDraft/PrepareSave` 做本地纯验证，HTTP 仍发送调用者的原始 JSON 字节一次。预览响应必须对应原始完整 Record 与 PreviewFacts；保存必须确认同一记录、唯一 Location，以及 HTTP 201/created 或 200/existing 的一致性。响应中的软件可用性只表示接口接线，不证明设备、库健康或灯光效果。

读取保留完整 Record JSON，并再次通过 P1 `InspectRecord` 核对 shape、revision、typed execution、source/recipe 和执行摘要；服务器的 verification 必须与本地检查相同。有效未知 source/recipe 可只读返回，保留其作者内容；不会转换为可执行记录或猜测新语义。

公共 JSON 检查器仅对显式 `json.RawMessage` 字段允许域内未知键，仍递归拒绝重复键、null 和过深结构。随后 P1 验证该字段完整的版本化结构；普通 typed 请求的反射字段检查不变。

保存不做 Prepare、提交任务或自动重试；禁用 POST GetBody 与重定向。失败沿用 publication-only `PatternImportError`：仅已知错误码与约定状态的明确拒绝可报告 `PublicationUnknown=false`；网络失败、不可信确认、意外状态/错误码或服务端失败保留不确定性。调用方应保留原 ID/revision，读取并比较完整记录后再决定是否重存。保存确认不意味着已经播放。

固定Ref的preview/run/request/cancel与CLI执行、等待及取消不确定性规则见[执行篇](README.pattern-execution-v2.zh-CN.md)。三个作者命令保持原语义，不隐式播放。
