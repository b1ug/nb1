# Pattern v2 HTTP：预览与独立图案库（实验）

本片开放高级 Action 的作者预览、条件保存、读取和冻结快照导入导出。**没有 Pattern v2 运行、任务查询或取消入口**，不生成 Job/FrameJob、执行票据或 daemon ID。

## 启用方式与目录隔离

库只通过显式 `WithPatternStoreV2(store)` 注入：

```go
store, err := patternstore.NewV2(explicitAbsoluteDirectory)
handler := httpapi.New(service, httpapi.WithPatternStoreV2(store))
```

选路径、验证配置及部署由调用方负责。构造 handler 不调用 Store（连 Path 也不调用），不自动计算默认目录、不使用旧 `--pattern-dir`/`NB1_PATTERN_DIR`、不扫描/迁移/合并 v1 库。本片没有新 CLI flag/env、DirectoryV2 option 或日常服务配置。P2 的 `DefaultV2Dir` 仍只是纯路径计算，不被此入口自动调用。

未注入存储时，纯 preview 仍可用；有效的 save/import/list/get/export 返回 **501 `pattern_library_unsupported`**，消息明确说明 v2 storage 未配置且 preview 可用。这不代表该平台的文件系统失败。实际注入 Store 的平台错误按其现有错误分类返回。

v1 与 v2 Store 选独立目录。误把 v1 目录给 v2 时返回 503 `pattern_library_format_mismatch`，不覆盖原快照；旧 v1 接口对 v2 文件仍沿原错误行为拒绝。文件权限、短锁、重开读取、原子发布和关闭错误复用原 StoreV2 事务，不另写文件系统代码。

## 路由与报文

所有 JSON 成功外壳固定 `schema_version:2`、`contract:"nb1-pattern-library/experimental-v2"`。P1 Record/Snapshot 的契约、canonical 与 SHA 域不变。

| 请求 | 输入 / 输出 |
| --- | --- |
| `POST /v2/patterns/preview` | 原始 `{id,title,source}` → Record、verification、execution_digest、纯 preview facts |
| `POST /v2/patterns` | 相同作者内容加必填 expected_revision → 201 新建 / 200 相同内容已存在；单一 Location 指向原 ID |
| `GET /v2/patterns` | 一次 List → 库信息和摘要；一次 Path 仅用于该响应的库位置 |
| `GET /v2/patterns/{id}` | 一次 Get → Record、该记录自己的 verification、execution_digest |
| `GET /v2/patterns[/{id}]?format=snapshot` | 整库 Export / 单条 Get → P1 canonical 冻结快照原字节，无额外换行或 HTTP 元数据 |
| `POST /v2/patterns/import` | 完整冻结 Snapshot 原始字节 → 一次 Store.Import；成功固定 200 |

GET 的 `preview`、`import`、`export`、`capabilities` 等仍是合法 ID。只有指定两个子路径的 POST 是操作。其余 ID 必须满足既有 1–64 ASCII 规则，附加 run/query/cancel 路径不会转成执行请求。不接受 PUT/PATCH/DELETE 或运行参数。

预览把原始 body 直接交给 `patternsv2.PreviewDraft`；保存直接交给 `PrepareSave`。HTTP 不先 Unmarshal 或重新编码作者请求，不计算或替换高级动画。Source 保留颜色写法与省略默认值，冻结 Compiled/recipe/revision 由原构造器提供。

expected_revision 确认的是完整候选，不是库版本、覆盖权限或 daemon 生命周期。与候选不匹配时 409 `pattern_revision_conflict`，Store 零调用；匹配后只 Import 一次单记录 snapshot，不先查 ID/List/Resolve，不重试。已有同 ID 异内容仍由 create-only 事务拒绝。保存结果是 `{schema_version,contract,records,created,existing,total}`，不返回执行 `replayed`、202 或 Job。

## 纯事实与未知只读数据

preview/list 显式返回 `pattern_execution:{available:false,reason:"pattern_identity_admission_not_integrated"}`。verification 只表示来源与冻结内容通过验证，不表示设备、worker、Pattern admission 或实机观感合格。

preview 统计来自既有纯预览：数量、名义时长、要求的能力，不重复程序或观测设备。`host-nominal-excludes-native-io` 不包含 native I/O、Read 和额外 off 清理。hold 也等待末帧 fade；默认 3000ms Orbit 的4段名义3040ms、8段3045ms，差异来自375ms interval 的 fade 量化到380ms。

合法未知 recipe/source 通过 Inspection 只读返回，不在 HTTP 层重新生成/替换冻结执行体，也不伪造预览统计。一次 List 中有未知项即 `read_only`；单条 GET 仍只反映该条验证，不额外 List 判断整库状态。单条导出由原 Inspection canonical JSON 构成快照，再 InspectSnapshot；不走拒绝未知项的严格 EncodeSnapshot。整库导出直接使用 Store.Export 字节。库损坏则整个请求失败，没有悄悄漏项或部分数据。

导入只接受完整、全部已验证的冻结包。未知项不能借 import 新写入；返回400。已有未知项使空导入、幂等导入与新保存都返回409 `pattern_library_read_only`，不改写原数据。

## 严格输入与失败

新路由在既有 loopback Host/同源 Origin 保护之后，沿用 JSON MIME、no-store、nosniff，不增加 CORS；现有 demo 的 CSP 仍由原资源 handler 管理。

所有 POST 要求恰好一个 `application/json` Content-Type，拒绝任意 query（包括空 `?`），最多读取1MiB+1字节以判超限。超限413 `pattern_package_too_large`、Connection close；读取错误400。原始 JSON 再按严格 codec 拒绝孤立 surrogate、无效 UTF-8、重复/未知字段、null、尾部值和词法/深度/Action 限额。65536字节 Action 限额在整包有界解析后检查，未宣称它在任何分配前执行。

GET 不接受正长度、未知长度或 transfer body；只接受真正无 query 或唯一 `format=snapshot`，拒绝空 `?`、`?&`、重复/未知 query 和非法值。方法/输入错误在 Store 之前返回。

| 情形 | status / code |
| --- | --- |
| 作者输入或导入包非法 | 400 `invalid_pattern_request` |
| 作者契约不支持 | 501 `pattern_source_unsupported` |
| 候选确认不匹配 | 409 `pattern_revision_conflict` |
| ID 异内容 / 只读 / 合并限额 / 锁忙 | 409 `pattern_conflict` / `pattern_library_read_only` / `pattern_library_limit` / `pattern_library_busy` |
| 不存在 | 404 `pattern_not_found` |
| 目录格式误配 | 503 `pattern_library_format_mismatch` |
| 文件不安全、损坏、I/O失败 | 503 `pattern_library_unavailable` |
| 已发布但最终确认失败 | 503 `pattern_commit_uncertain` |

错误只返回现有 `{error:{code,message}}`，不暴露 Store 的部分 result 或 Location。`ErrCommitUncertain` 不自动重发；调用方保留原完整候选，GET 原 ID 并核对完整冻结身份。404或读取失败仍是未确认，不证明此前没有发布；一致只能证明该记录现在存在，不能判断哪次请求创建。HTTP本片没有恢复重试或播放验证逻辑。

## 测试证据与范围

`testdata/pattern-v2` 逐字节复制实现前P3计划的24项 case、请求及响应golden。fixture内部的proposal状态是原历史资料，不是运行时能力声明。测试把它们发给真实 handler，比较JSON结构/值、status/headers与canonical snapshot精确字节；错误 message 按原计划不固定平台字节。

测试使用私有临时库、实际 StoreV2 与只暴露 panic 方法的 Service/ReceiptService/FrameService，覆盖零执行/零设备方法、精确Store调用次数、无配置、严格原字节、未知只读、坏SHA/非法导入不创建库、整包冲突不部分发布、实际httptest服务/重开及v1/v2误配不覆盖。原HTTP v1及编译器测试继续运行。没有访问真实用户库、USB、摄像头或日常daemon，也不把本结果当作新Windows/硬件验收。
