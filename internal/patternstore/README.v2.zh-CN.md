# StoreV2：实验图案库的内部持久化

`StoreV2` 只管理 `patternsv2` 冻结快照，不控制设备，也未接入任何 HTTP、CLI、MCP 或 worker。用户命令仍未在本片注册。

## 独立格式，共用文件安全底座

`NewV2(absoluteDir)` 只记录显式绝对目录，不创建或读取文件。`DefaultV2Dir()` 只计算当前用户配置目录下的 `nb1/patterns-v2`；它不会自动选择该目录，也不扫描、合并或迁移旧 `nb1/patterns`。

v2 与 v1 使用同一个私有 `transaction` / `openFS` 实现：相同文件权限/ACL 检查、父目录解析、文件身份核对、`writer.lock`、`patterns.json`、短锁、暂存与原子发布、同步/关闭错误语义。没有复制或弱化 Unix/Windows 文件安全代码，也没有改变旧 `Store` 的方法。

两种格式必须选不同库目录。v2 发现合法 v1 包返回 `ErrFormatMismatch`，不覆盖；旧 v1 发现 v2 快照沿用既有 `ErrCorrupt`，同样不覆盖。任一格式都不靠另一个 codec 迁移数据。构造器没有 I/O，只有锁而没有快照的空目录也没有格式身份；第一个成功发布的冻结快照才确定其格式。读取已有目录可能按原底座规则创建锁文件；“读取缺失目录不创建”并不意味着所有读操作都没有文件系统写入。

## 读取与验证分开

| 方法 | 返回及边界 |
| --- | --- |
| `List()` | 按 ASCII ID 排序的 `[]patternsv2.Inspection`；包含合法但未知 source/recipe 的只读项 |
| `Get(id)` | 一个 Inspection；查询未知项不是数据损坏 |
| `Resolve(id, revision)` | 精确 revision 对应的已验证 Record；未知项返回 `patternsv2.ErrUnsupported` |
| `Export()` | 完整、确定的 canonical 快照，保留未知项；不写外部文件 |

每次都重新持锁读库，不缓存旧快照或验证状态。库文件必须逐字节等于 codec 的排序 canonical 输出；损坏 SHA、非法记录、尾换行、非规范排序或非快照内容拒绝读取。缺失库返回空列表/空 canonical 快照，不创建缺失目录。

一个未知项会禁止这个库写入，但不阻止另一个已知项精确 Resolve。Inspection、Record 与导出 bytes 都不与库内部数据共享可变切片。Resolve 返回保存的冻结程序，不选择当前配方替代，也不表示设备支持它。

## 导入事务

`Import(data)` 只接受完整、全部已验证的 v2 冻结快照。Go 调用者可以 `patternsv2.NewRecord` → `patternsv2.EncodeSnapshot` → Import；本片不另设用户 save 语法。

顺序固定：

1. 全包严格解析、SHA 与来源展开验证完成后，才可能打开/创建库。
2. 取得相同底座的锁，重新读取并验证当前库。
3. 当前任一合法未知项返回 `ErrReadOnly`；空导入和完全幂等导入也如此，不把未知数据悄悄重写。
4. 相同 ID 相同完整 canonical 记录为 Existing；相同 ID 异内容返回 `ErrConflict`。任一冲突让整包失败。
5. 合并后检查最多 128 条及 1 MiB canonical 预算；单次发布完整新快照。结果的 Records/Created/Existing 按输入快照规范排序。

没有覆盖、删除、自动升级、版本历史或播放。全部错误都清空导入结果，避免暴露半成功的记录列表。发布前失败保持旧快照；原底座报告已发布后发生错误，返回 `ErrCommitUncertain`。这表示新文件可能已可见，调用者应读取核对，不能推断未保存。

幂等导入不重新发布文件；如果最终关闭失败，返回该关闭错误而不冒称 publication_unknown。List/Get/Export 的关闭失败同样清空原本取到的数据。

## 本片验证范围

测试用私有临时库验证持久化、重开、原始高级来源保留、并发导入、create-only、深拷贝、v1/v2 双向误配不覆盖、外部替换后重新读取、未知项只读/导出与已知 Resolve。事务注入测试检查 open/read/publish/close 故障与 unknown publication。现有底座安全测试继续覆盖共同实现，没有重写一套 FS 安全逻辑。

预算测试覆盖 128 条合并上限及 1 MiB 输入上限；合并 canonical 字节限制继续由 P1 codec 执行，没有修改格式或为构造大夹具开放手写帧来源。

Windows 交叉编译只能证明类型/编译兼容，不能证明新 StoreV2 的原生 ACL、锁、文件同步行为。原生 Windows 与新用户入口属于后续验收。本片不会读写正在运行的用户库、安装程序或控制硬件。
