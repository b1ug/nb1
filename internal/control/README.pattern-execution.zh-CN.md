# Pattern 身份与 typed 执行摘要（P4a 纯函数）

本片只定义有界身份、严格冻结执行体及固定域摘要。没有来源resolver、Engine准入、Store、HTTP、任务或硬件接线。

`internal/patternidentity` 是仅依赖标准库的叶子，提供 `Ref{RecordContract,ID,Revision}`、既有 RecordContract/ExecutionDomain 常量、`ValidateRef`、`ValidateDigest` 与 `HashExecutionCanonical`。ID维持1–64 ASCII、首位字母/数字；revision/digest是`sha256:`加64位小写hex。合法ID `latest`仍只是一个必须同时提供精确revision的普通ID，没有隐式选择最新版本。Hash只计算固定域+一个LF+调用方字节，不证明JSON规范、来源正确或可执行。

control的最小接口：

```go
execution := control.PatternExecution{
    Kind: "program", Program: &alreadyFrozenProgram,
    // 或 Kind:"frame_program", FrameProgram:&alreadyFrozenFrameProgram
}
copy, err := control.ValidatePatternExecution(execution)
canonical, err := execution.CanonicalJSON()
digest, err := execution.Digest()
err = execution.VerifyDigest(expectedDigest)
```

`Validate`而非`Normalize`命名强调输入必须已经冻结规范化。恰好匹配一个Program或FrameProgram；外层contract从kind派生，不收任意JSON、Action、recipe、float或外来contract。验证使用既有Normalizer和原预算，再逐值比较原输入；补repeat/on_finish、RGB转小写等任何变化都拒绝，不在原来源身份下悄悄修复。返回值深复制。没有给Program误加Frame的1024执行数预算：1024个存储step、repeat2合法。

PatternExecution本身是可变的Go输入值。CanonicalJSON、Digest每次都重新严格验证并深复制，不依赖先前Validate，不缓存结果；VerifyDigest重新计算后与精确预期比较，拒绝不匹配而不替换预期。返回canonical bytes各自独立。调用方仍须按普通Go规则同步对原输入的并发修改；并发只读调用和各自修改返回副本不共享可变数据。

Canonical只编码闭合ASCII/非负整数子集，逐层构造固定key的map/slice，再json.Marshal排序；保留全部零字段、FrameProgram内层contract以及所有数组顺序。不会把内层struct塞进map后假设字段顺序，也不复制通用Unicode/数字parser。

摘要固定为 `sha256(UTF8("nb1-pattern-execution/experimental-v1\n") + canonical_execution)`，前缀`sha256:`小写hex，无尾LF。它不含Ref；未来准入必须同时绑定Ref和重算摘要。control不能独立证明作者Action编译到此数据；后续P1 adapter仍必须验证完整Record、原外层execution contract、kind与typed union，不能在转换时丢掉错误contract。

P1只复用叶子两个常量和两处execution Hash调用。原Record revision hash、parser、canonicalizer、错误语义、快照及公开常量值不变；control不反向依赖patternsv2。

外部测试通过P1真实构造八动作、不同LED/结束方式、4/8 Orbit、LED顺序与周期余数、named/Hex/RGB8/HSV等来源；从P1 canonical Record用RawMessage抽取原执行子树逐字节比对，不经过float64。十个独立Python typed向量覆盖最小零值、最大repeat/结构、精确24h、51ms/60ms量化界限、反序LED及不同repeat布局；它们不是P1来源记录，不冒充已验证recipe。所有字段/数组变更须改变摘要或拒绝，错误域/LF/预期摘要、默认补全、非规范RGB、预算与深复制并发边界另测。
