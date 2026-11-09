# 本地图案库 HTTP 合同（候选）

库由 daemon 的 `--pattern-dir` 管理；缺省读取 `NB1_PATTERN_DIR`，未配置则使用当前服务账号的 `UserConfigDir()/nb1/patterns`。构造、启动和读取不存在的库不创建目录。配置错误只使图案库接口不可用，不阻断原有灯光控制。没有自动恢复、迁移、提权或临时目录回退。

这些路由沿用 loopback Host 与同源 Origin 防护。库操作不接触设备，不创建 Job，不接收执行票据。初版只允许 create-only 导入，不提供修改和删除。

| 请求 | 成功结果 |
| --- | --- |
| `GET /v1/patterns` | `PatternList`，固定 `schema_version:1`、`contract:"nb1-pattern-library/v1"`、library 与按 ASCII ID 排序的 patterns summaries。空库是 `patterns:[]`。 |
| `GET /v1/patterns/{id}` | 完整已验证 Record，无额外 wrapper。`import/export/library` 都可以是 ID。 |
| `POST /v1/patterns/import` | 原始创作包或快照包，限1MiB，原始字节直接交给 Store；HTTP200 返回原 ImportResult 的 records/created/existing/total。 |
| `GET /v1/patterns?format=snapshot` | Store.Export 的精确规范冻结包字节。 |
| `GET /v1/patterns/{id}?format=snapshot` | 单条规范冻结包，可重新导入。 |

library 字段为 path、state（成功时 ready）、create_only、max_records、max_package_bytes、record_contract、compile_contract、snapshot_kind。summary 字段为 id、title、revision、compile_contract、total_steps、nominal_duration_ms、on_finish、required_capabilities。不可用库返回明确错误，不冒充空库。GET 不允许请求体；查询仅允许一个 `format=snapshot`，不接受重复、未知或空参数。导入不接受查询参数，且要求唯一的 `application/json` Content-Type。

## 固定引用

`POST /v1/programs/preview` 支持三选一的 program、effect、pattern_ref；后者形状为 `{id,revision}`，revision 必须是实际保存的 `sha256:` 加64位小写摘要。可带 serial，只检查该设备的缓存快照。

旧 program/effect 预览结果不变。pattern_ref 分支返回 `{pattern_ref:{id,revision},preview:<原 PreviewResult>}`，preview.input_kind 为 program。使用保存的 CompiledProgram，没有“latest”、重新展开旧 Effect 或升级编译合同。

`POST /v1/devices/{serial}/jobs` 同样三选一。pattern_ref 分支要求显式 policy=reject、request_id 与 expected_daemon_instance_id 原票据；没有无键回退。Resolve 的文件事务已结束后才调用一次原 SubmitWithReceipt。成功仍是原 Job JSON 和原回执响应头，没有新增 Job 字段。

执行去重继续以 Program/serial/policy 为依据。不同标题或引用产生相同 Program 时，同一票据仍复用原 Job；不同 Program/策略触发原 idempotency_conflict。原 Job、票据查询和取消不读图案库。引用失效可能令重复 POST 失败，但不能据此判断先前没有执行；应查询原票据。

## 错误

错误仍是 `{error:{code,message}}`。输入无效为400 invalid_pattern_request；请求体过大为413 pattern_package_too_large；库限额、ID冲突、版本不符和锁争用分别为409 pattern_library_limit / pattern_conflict / pattern_revision_conflict / pattern_library_busy；缺失为404 pattern_not_found；未配置/平台不支持为501 pattern_library_unsupported；不安全、损坏、配置和I/O错误为503 pattern_library_unavailable。

发布后无法确认的导入返回503 pattern_commit_uncertain，可能已保存；不返回伪造的空 created。此时按导入ID/revision重新读取核对。它是文件发布不确定，与执行票据 outcome_unknown 不同。

能力目录仅在配置的 Store 本次可读取时标记 pattern_library=true 并增加 supported_inputs 中的 pattern_ref；这不是写入权限、后续准入或真实硬件兼容性保证。没有改动旧 Light/Effect 语义或 MCP 工具。
