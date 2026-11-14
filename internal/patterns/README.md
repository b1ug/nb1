# Pattern 包纯 codec（B 的第一步）

只处理内存中的版本化创作包、冻结记录和快照字节。不读写文件、不管理库、不提供 HTTP/CLI/MCP、不创建 Job、不访问设备或锁。这个包的存在不表示命名图案库、持久化、引用执行或硬件 Flash 已可用。

本实现冻结 `nb1-pattern-record/v1` 哈希域与 `nb1-program-effect/v1` 编译契约，复用 `control.Normalize` / `control.CompileEffect`。只支持这一个编译契约；如果 control 编译契约以后改变，本包没有保留该旧验证器时会拒绝，而不把旧快照用新版本偷偷重编译。先将颜色通过独立颜色入口变成 Hex6；本 codec 不引入颜色名字、Hex3 或 HSV。

## 补充边界与 API

- `id` 精确满足 ASCII `[A-Za-z0-9][A-Za-z0-9_-]{0,63}`。区分大小写，不解释为路径。
- `title` 必填，1–256 个 Unicode scalar；不 trim、不 NFC/NFD 转换，允许 C0 字符但只在规范 JSON 中以 `\u00xx` 编码。拒绝非法 UTF-8 和 JSON 孤立代理项。
- 包版本为整数 1；输入至多 1MiB，每包最多 128 项；完整规范快照导出至多 1MiB。创作和快照均允许空数组，导出空库使用 `records:[]`，不使用 null。同包重复 ID（即使内容相同）整包拒绝。库层未来的跨包 idempotency 不由 codec 实现。
- 所有数字均为普通非负 JSON 整数词法，拒绝小数、指数和负零；随后仍受 control 字段范围、总时长和形状约束。JSON 字段大小写精确，拒绝 null、未知、重复（包括转义重影）、尾随 JSON 和混合包形状；不放宽 `jsoninput` 的其他入口。
- `Source` 只包含 `Program *control.Program` / `Effect *control.Effect` 中一个。`Record` 包含 ID、Title、Source、CompiledProgram、CompileContract、Revision。
- `NewRecord(id, title, source)` 生成规范来源、独立的执行快照和真实摘要。
- `NormalizeRecord(record)` 验证已声明的契约、规范来源展开、规范 compiled_program 与 revision；返回规范深拷贝，不替换契约、不生成一个不同的 revision。等价的省略默认、RGB 大小写和 JSON 转义可被规范为同一记录。
- `CanonicalRecordJSON(record)` 验证记录后返回参与哈希的规范 JSON；不含 revision、不含域前缀、无尾随 LF。哈希前缀为 `nb1-pattern-record/v1` + 一个 LF。
- `DecodePackage(data)` 严格识别 `schema_version/patterns` 创作包，或 `schema_version/kind/records` 冻结快照包；完整校验和规范导出限额通过后才返回全部记录，失败返回 nil。
- `EncodeSnapshot(records)` 验证全部记录，返回封闭规范快照包字节（无尾随 LF）。包内顺序保留，但单条 revision 不包含包内顺序。

返回的 Record/Source/Program steps/Effect 指针均与调用者输入独立。每条 Record 的 source Program 和 compiled Program 也不共享 steps backing array；不同记录不相互共享可变数据。公开 DTO 本身可由调用者修改，因此它不是不可变对象；再次传给导出/验证时必须重新核验。无隐藏全局缓存。

## 规范编码

哈希内容按顺序编码 `id,title,source,compiled_program,compile_contract`；Program 为 `steps,repeat,on_finish`，Step 为 `led,color,fade_ms,hold_ms`。Effect 按适用种类补齐默认，steady 为 `kind,color,led,fade_ms`；pulse 为 `kind,color,led,cycles,period_ms,on_finish`。

数字为最短十进制整数。JSON string 只转义引号、反斜杠和 C0；C0 固定小写十六进制 `\u00xx`，非 ASCII、U+2028/U+2029、HTML 字符和 `/` 直接写 UTF-8。不同转义形式解码为同一字符时 revision 相同；ID、标题、Unicode 序列或来源种类变化会改变 revision。Program 与 Effect 来源即使有同一执行快照也不是同一内容版本。

测试中的真实 canonical bytes / SHA-256 由独立 Python encoder 生成并与 Go 比对。它们不证明存储原子性、掉电耐久、Job 执行或实灯效果。
