# Pattern v2 纯记录 codec（实验）

本包只验证和冻结数据，没有 Store、HTTP、CLI、MCP、worker、设备操作或自动播放。用户入口与库目录仍是独立规划，不能据此声明“已支持保存/播放 v2 图案”。

`nb1-pattern-record/experimental-v2` 同时保存：

- `source`：作者高级 Action 的 JSON 意义。颜色名、RGB/HSV 表示和省略默认值保留；不保留缩进、键顺序或数字多余尾零。
- `compiled.normalized_action`：完整默认值和 canonical RGB。
- `compiled.recipe_id`：实验 Orbit 的固定 4/8 段 recipe。v1 高级 Action 不引入此字段。
- `compiled.execution`：判别为 Program 或 FrameProgram 的冻结执行体。不会把帧转换成旧步骤。
- `revision`：除 revision 本身以外整份记录的内容身份。

已知来源是现有 `nb1-light-action/v1` 的八个动作，以及 `nb1-light-action/experimental-v2` 的 Orbit。验证继续调用其版本绑定的纯编译器，比较完整规范 Action、recipe 和冻结程序；不修改原 v1 编译器/记录/摘要含义。

## 调用边界

```go
record, err := patternsv2.NewRecord(id, title, patternsv2.Source{
    Kind: "light_action",
    Contract: "nb1-light-action/experimental-v2",
    Action: json.RawMessage(`{"kind":"orbit","colors":["red","blue"]}`),
})
wire, err := patternsv2.EncodeRecord(record)
```

`NewRecord` 构造一份新值，不访问现有 ID、不负责覆盖判断。将来的 Store 必须另行实施 create-only 原子事务。

`DecodeRecord`、`NormalizeRecord`、`EncodeRecord`、`CanonicalRecordJSON` 与 `ExecutionDigest` 只接受已知验证器确认的记录。`Execution` 是 Go 判别联合：`program` 对应唯一的 `*control.Program`，`frame_program` 对应唯一的 `*control.FrameProgram`；线上 JSON 均使用 `kind/contract/program`。反序列化必须使用包内严格 decoder，不使用普通 `json.Unmarshal` 充当验证。

备份/查看使用显式只读路径：

```go
inspection, err := patternsv2.InspectRecord(wire)
status := inspection.Support() // verified 或 unsupported_recipe/source
copy := inspection.Record()   // 数据副本，不是运行许可
backup := inspection.CanonicalJSON()
verified, err := inspection.VerifiedRecord() // 未知 recipe 返回 ErrUnsupported
```

Inspection 的内部副本和验证状态不可从包外改写；所有返回切片/指针都独立复制。`Support.Verified` 只表示来源/冻结记录校验通过，不表示硬件、执行器或存储可用。未知 recipe/source 仅在外壳、SHA、有限 JSON 与已知低层执行契约仍合法时返回只读结果。未知 Record/执行契约无法借此混入；已知 recipe 与 segments 不配、低层类型交叉、遗漏冻结字段或错误程序都属于 invalid。

未知 recipe 保留当前作者契约规定的规范 Action 与执行类型，但不能按当前 recipe 验证目标。只读路径不擅自替换配方。普通 codec 写出入口拒绝未知版本；只读备份使用 Inspection 返回的原 canonical 数据。

`InspectSnapshot` 与 `DecodeSnapshot/EncodeSnapshot` 使用独立的 schema 2 快照；前者可只读携带 unsupported 项，后者要求所有记录已验证。任何错误返回整包失败，没有部分结果。快照按 ASCII ID 排序，重复 ID 拒绝，空库是 `records:[]`。这不是磁盘发布逻辑。

## 严格语法与内容身份

全树先检查有效 UTF-8、Unicode scalar、重复/转义重复键、null、尾部值、深度和数字词法。未知字段逐层拒绝；未知来源的 Action 作为有界不透明对象保留，不声称理解其字段。当前作者 Action 对自己的所有字段继续严格检查。

时间、次数和 LED 必须经过原作者语法检查，不能先把 `1.0` 规范成 `1`。HSV 接受原颜色契约允许的非负十进制、最多六位小数；没有浮点运算。canonical 数字移除小数尾零；禁止指数、符号、负零、超出 int64 的整数部分。冻结规范 Action 同样先检查原数值词法。Program/FrameProgram 必须包含所有规范字段，RGB 小写；不补默认值后假装原冻结数据相同。

canonical 规则是本包独立实验规则，不宣称 JCS：ASCII 对象键按字节排序，数组次序保留；字符串 C0 使用小写 `\u00xx`，引号/反斜线转义，其余 scalar 保留；不做 Unicode normalization、HTML 转义或尾换行。

```text
revision = sha256("nb1-pattern-record/experimental-v2\n" + canonical(record without revision))
execution_digest = sha256("nb1-pattern-execution/experimental-v1\n" + canonical(compiled.execution))
```

两者输出均为 `sha256:` 加 64 位小写 hex。标题、ID、原始颜色写法改变 revision；相同规范执行体仍可拥有相同 execution_digest。SHA 不等于来源签名；即使篡改后重算正确 SHA，已知来源的冻结不一致仍被拒绝。

限额：记录/快照输入与 canonical 输出最多 1 MiB；128 条记录；JSON 深度 32；Action 与规范 Action 各最多 65536 字节；ID 1–64 ASCII、title 1–256 scalar/1024 字节。已知编译器和低层规范器继续实施各自时间/数量/64 位预算。直接 Go 输入在序列化前检查原始切片、字符串与判别联合的界限。

## 测试依据

`testdata/design` 来自 2026-11-10 本地设计夹具，真实冻结程序由基线 `c4147cef3aff1a1f9622b4c29ec84d4130aedd9e` 的纯编译器提取；canonical/SHA 由独立 Python 草案实现产生。没有通过设备生成测试输出，也没有从最终 Go codec 倒算 golden。

测试覆盖这些独立 golden、作者写法与 HSV、原八动作、Orbit 4/8 段、带正确 SHA 的冻结篡改、只读未知 recipe/source、重复/null/Unicode/词法/限额、可变数据隔离与并发读取。旧 v1 快照依然通过旧 codec 逐字节往返；新的 v2 不读取或迁移旧格式。

测试通过只证明纯数据边界。Store 发布、原子 Pattern 来源回执、worker 执行、用户 API、实机观感和 MK3 支持均不在本片。
