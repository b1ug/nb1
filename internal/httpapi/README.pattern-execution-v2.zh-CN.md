# Pattern v2 固定引用执行（实验）

本接口把已保存的完整作者记录交给 nb1 核心验证并执行。客户端传完整 Ref 和原票据；高级 source、编译快照、recipe、revision 由服务复验，不让客户端传任意 Program 冒充已验证图案。此为开发源码接口；是否安装到某个 daemon 要另行核实。未声明任何新硬件版本或实物效果通过验收。

## 固定身份与四个操作

`pattern_ref` 为 `{record_contract,id,revision}`。契约固定 `nb1-pattern-record/experimental-v2`，ID 1–64 ASCII 字符，首字符字母或数字，后续可用 `_`/`-`；revision 是 `sha256:` 加64位小写十六进制，不能用 execution_digest 代替，不隐式选择 latest。

| HTTP 操作 | 输入与成功 |
|---|---|
| POST `/v2/patterns/preview-ref` | `{pattern_ref,serial?}` → 200；复验来源并返回身份、execution_digest、冻结 execution、对应纯 preview、pattern_execution 接线状态。 |
| POST `/v2/devices/{serial}/pattern-jobs` | `{pattern_ref,policy,request_id,expected_daemon_instance_id}` → 202；三种 policy 必须显式传 replace/queue/reject。 |
| GET `/v2/devices/{serial}/pattern-requests/{request_id}` | 四个必填、唯一 query：expected_daemon_instance_id、record_contract、pattern_id、revision → 200。 |
| POST `/v2/devices/{serial}/pattern-requests/{request_id}/cancel` | `{pattern_ref,expected_daemon_instance_id,confirmed_job_id?}` → 202。confirmed_job_id 省略才不加约束，显式空/null 非法。 |

`GET /v2/patterns/preview-ref` 仍是普通合法 ID 的读取，与 POST 预览互不占用。其余新路径精确匹配，不接受尾斜杠或多余路径段。

预览响应 contract 为 `nb1-pattern-ref-preview/experimental-v1`，schema_version=1。execution 是原规范 `{kind,contract,program}`；preview 根据 kind 是现有 Program PreviewResult 或 Frame HTTP preview；两者在选择 serial 时均回显 device.serial/connection，未选择则不含 device。可选 serial 只读一次缓存 Device，无 USB、Prepare、回执查询、Job 或取消。Frame hold 的缓存检查额外要求 hold。名义时长不含 native I/O、实际尾等待差异、Read 与清理，缓存支持不是执行承诺。

## 票据与恢复

执行响应是原核心 Envelope：`{pattern_ref,execution_digest,job_kind,job}`，job_kind 为 program/frame，job 保持原 Job/FrameJob 字段。成功 POST 对新请求和重放均返回202；唯一 `X-NB1-Daemon-Instance-ID`、`X-NB1-Replayed`、Location 保留完整原身份。Location 已正确 URL 编码，直接 GET 该值即可；不要从 Job 历史或旧裸 Job 路径猜结果。

提交前从既有 `/v1/instance` 获取实例和 request_id prefix，再生成一次32位小写十六进制后缀，保存该票据和 Ref。网络失败后保留相同内容；不能换新 request_id 自动重发。receipt 在内存中，daemon 重启后实例变化；不存在的过期票据410不能证明它从未执行。

Handler 不在 submit 前探测图案库。核心先查精确原回执，仅新请求才调用惰性 verifier。库消失、配置失败、当前来源不支持或 Engine 关闭后，保留的旧回执仍可按核心规则重放。查询和取消完全不访问 Store/目录/Resolve；取消也不先隐式查询，只验证调用者提供的绑定。调用者若需要先确认 Job，可 GET 原 Location 后附 confirmed_job_id。

取消202仅表示响应了请求；active 可能仍 cancelling，native 写入不会被 host context 中断，清理可失败。终态 hold 不因再次取消而关灯。已失败或取消的票据不会重新播放。

## 输入、安全与错误

所有新 POST 最多4096原始字节，恰好一个 application/json Content-Type，不接受 query（含空 `?`）；有效 UTF-8、严格对象、无 duplicate/unknown/case alias/null/trailing。复用 jsoninput 并对全部字段验证闭合 ASCII 语法，孤立 surrogate 不能被替换后混入。GET 不接受 body，四个 query 拒绝未知/重复/空值/无效编码/空分隔段。继承 loopback Host、同源 Origin、无 CORS、JSON/no-store/nosniff；demo CSP 不变。

| 情形 | status / code |
|---|---|
| 原始输入或绑定非法 | 400 invalid_pattern_request |
| 核心现有缓存能力等 ErrInvalid | 400 invalid_request |
| service 不实现 PatternService | 501 pattern_execution_unsupported |
| 新请求未配置 v2 Store | 501 pattern_library_unsupported |
| ID 不存在 / revision 不符 / 库忙 | 404 pattern_not_found / 409 pattern_revision_conflict / 409 pattern_library_busy |
| 来源或 recipe 未知 | 501 pattern_source_unsupported |
| 配置、权限、unsafe/corrupt、未知 I/O | 503 pattern_library_unavailable；旧新库格式互误为 pattern_library_format_mismatch |
| 返回的 Ref / 冻结执行 / 摘要不一致 | 500 pattern_integrity_error |
| context 结束 | 503 unavailable；保留原票据查结果 |
| 实例/票据冲突、过期、容量、队列等 | 沿用既有409/410/429/404/503 code |

先按 typed resolver 分类再映射核心错误；不能把来源损坏包装的 ErrInvalid 当作用户报文400，也不能因为响应503就把未知 I/O 当作 core 可重放的 availability。错误只给 `{error:{code,message}}`，不返回部分成功数据、Location、路径或来源内容。

`pattern_execution.available` 只表示软件接口和 resolver 配置接线：true=`pattern_service_configured`；false=`pattern_service_unsupported` / `pattern_library_unconfigured` / `pattern_library_configuration_error`。读取此状态不调用 Store。它不阻断旧回执恢复，也不证明库健康、设备可执行或光学效果合格。

## 配置与验证范围

v2 使用独立 `--pattern-v2-dir` / `NB1_PATTERN_V2_DIR`，默认服务账号 `UserConfigDir()/nb1/patterns-v2`，不随自定义旧 --pattern-dir 改变。构造、纯作者预览与缺失库读取不创建；有效写入进入事务时才可能创建目录，发布失败也可能留下目录。既有库读取可能创建锁文件。目录格式互误拒绝，不自动迁移或覆盖。

本片测试只用 httptest、临时库与注入内存驱动；不开真实监听、不接日常 daemon、不读用户库、不访问 USB。CLI/MCP/Web 接入、OpenAPI 与总手册更新由后续独立切片完成。
