# verified Store → Pattern core resolver

本包只把**显式选定**的 Pattern v2 Store 接到 `control.PatternService`。不注册 HTTP/CLI/MCP/Web 执行入口、不选择目录、不读取默认配置、不拥有 Engine/USB，也不把作者 Record 或路径保存进 Engine。

## 最小接口与调用

```go
type Store interface {
    Resolve(id, revision string) (patternsv2.Record, error)
}
func New(store Store) control.PatternResolver
```

构造 callback 时零 Store 调用。nil、typed-nil pointer/function 等配置在 callback 真正执行时返回 `PatternResolveConfiguration` + `ErrConfiguration`；不会调用 nil 的 Resolve。调用方可以始终把这个 lazy callback 交给 core：既有原回执在 core 初查命中时根本不调用 resolver，库丢失或未配置不会阻断旧任务查停。

callback 只调用一次精确 `Resolve(ref.ID,ref.Revision)`，不调用 Path/List/Get/Export、NewV2 或默认目录计算。何时调用、两阶段锁与幂等由 core 控制，不在本包另做缓存/in-flight表或提交重试。具体 StoreV2 的读取事务在 Resolve 返回前已经关闭；Engine 锁不覆盖这段文件系统操作。

## 验证不是相信一个 Store 名字

即使接口由 fake/第三方实现，也不相信其声称 verified：

1. 检查 ctx 和有界精确 Ref；只接受已有 RecordContract/ASCII ID/完整小写 SHA。
2. 核对返回的 record_contract、ID、revision 与原 Ref 逐字段相等；不改查 latest。
3. 保留并检查 P1 的显式 execution contract 与闭合 typed union，不能只从 kind 推导新 contract 来掩盖错误数据。
4. 调用 `patternsv2.NormalizeRecord` 重新验证完整 Record 的有界形状、Source/Action语法、canonical revision、已知 recipe、normalized_action 和冻结执行体。它使用既有 P1 规则，不改变字段、canonical、revision 或 recipe 语义。
5. 从已验证冻结记录计算 P1 ExecutionDigest，转换为 control.PatternExecution，严格验证规范体并深拷贝，再用 core VerifyDigest 重算比较。core 准入仍会独立重算，双向绑定不会信任调用方自报摘要。
6. 返回前再次检查 ctx。错误都返回零 ResolvedPattern，不留半个可执行结果。

P1 规范化保存语义而非原文件空白/键顺序；真实 StoreV2 另外核对磁盘快照就是 canonical 字节。本包只有 Record 接口，不声称能从 fake struct 证明其原磁盘字节。改 Source 或冻结程序后重算合法 SHA 仍必须通过已知来源/recipe比对，不能凭新 hash 放行。

不接受 Inspection 作为可执行输入。合法未知 source/recipe 仍可以通过原 Store.Get/Export只读检查；本 verified 路径返回 Unsupported。库含别的未知记录不会阻断精确解析一个已知记录，也不触发写入或迁移。

## 错误分类与有限白名单

所有错误保留原 cause，正常单链 `errors.Is/As` 可查到它；外层使用实际 `control.PatternResolveError`。新增的 Unknown 只是该现有类型的 additive 常量，core 行为不变：除 availability 外都不能被并发好回执掩盖。

| Kind | 情况 |
| --- | --- |
| integrity | ErrReference；P1/control/identity invalid；Store corrupt/unsafe/invalid/limit/revision-conflict；错误显式执行 contract/union、来源与冻结不符 |
| unsupported | P1未知 source/recipe、Store平台不支持/只读错误 |
| configuration | nil/typed-nil Store、格式错配、权限拒绝或显式上游 configuration |
| unknown | 没有可靠来源依据的错误、上游 unknown/非法Kind、context cause及未分类错误；外层 Unknown 会压住恶意内层 availability |
| availability | 仅下述明确白名单，且任何分支都没有前述阻断诊断 |

availability 白名单：

- 真实语义 sentinel：`patternstore.ErrNotFound`、`patternstore.ErrBusy`。
- OS 缺失：必须位于具体 `*os.PathError` 的 `Op == "open"` 或 `"read"` 之下，再匹配 `os.ErrNotExist` / 对应 OS errno。裸 ENOENT/ErrNotExist 无法识别操作阶段，按 Unknown。
- 临时读取：必须位于具体 `*os.PathError.Op == "read"` 之下，errno恰为 `EINTR`、`EAGAIN`、`EWOULDBLOCK`、`ETIMEDOUT`。内层 PathError 替换外层阶段；close/rename/remove/sync不能借缺失或暂时性 errno 提权。

不信任 Temporary()/Timeout()、错误消息子串、任意 PathError、裸 errno 或上游自报 availability。EIO 不在白名单。错误树的完整性/config/unsupported/context/unknown标记先于白名单，支持 multi-Unwrap 分类，避免 Go1.18 与 current 对复合错误判断不同；不是把任一可用性分支当成整棵树可用。

Store 已返回明确完整性/配置/不支持诊断时，保留该诊断，即使 ctx 同时取消。可用性失败遇到本调用 ctx 取消则返回 ctx错误；成功 Store 返回后与最终转换后也检查 ctx。同步 Store 操作本身不能被这层 ctx 强制中断，本包不启动超时 goroutine、不持有 Engine/native lease来等它。

## 验证范围

单元测试覆盖 nil/typed-nil、精确Ref、恶意接口返回、source/编译体篡改重hash、未知 recipe、显式contract、深拷贝、ctx与错误Is/As/分类。集成测试只用真实私有临时 StoreV2和内存fake Backend：正常Program/Frame，Store锁在worker前释放，库删除/配置丢失后原回执查停，并发新key每调用一次Resolve但只发布一次，availability可恢复而bad ref/source/unsafe/unknown/context不能被好winner覆盖。

不操作真实用户库、设备、daemon进程、配置、摄像头或远端。不把这一软件片当成新HTTP执行表面、Windows原生、实体USB、MK3或原生I/O硬deadline验收。最终commit/命令/原始日志/修前修后记录保存在工作区 `artifacts/pattern-v2-resolver/`。
