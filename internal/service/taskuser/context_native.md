# 原生 Windows token opt-in fixture

仅新增一个 Windows test-binary 测试 `TestNativeCurrentOptIn`，不修改产品守卫，不接 CLI、任务计划或 PowerShell。默认没有 `NB1_TASKUSER_NATIVE=1` 时直接 Skip，尚未查询 token；普通 fake 测试和 Windows 交叉编译不计原生 PASS。

运行协议（这些命令只用于后续原生 Windows runner，本轮没有执行）：

```powershell
go test -c -o taskuser-native.test.exe ./internal/service/taskuser
$env:NB1_TASKUSER_NATIVE = '1'
$env:NB1_TASKUSER_EXPECT = 'ordinary' # 或 administrator-rejected
$env:NB1_TASKUSER_EXPECT_SID = 'S-1-5-21-...' # harness 独立核实的完整 SID
.\taskuser-native.test.exe -test.run='^TestNativeCurrentOptIn$' -test.v -test.count=1
```

三项环境变量必须明确提供；不增加文件输出路径变量。fixture 每次 opt-in 执行输出恰好一行 `NB1_TASKUSER_NATIVE_RESULT ` 加 JSON，由 harness 提取并保存。字段有 schema_version/status/expected/expected_sid、collection_fields/collection_complete/collector_closed、facts、current、handle_observation，失败时增加 failure_phase。非 opt-in Skip 不输出此原生报告。

首先独立打开本进程 token（只 TOKEN_QUERY），依次读取 User、Elevation、ElevationType、IntegrityLevel、Groups、SessionId **全部六项**，并成功 Close 后才允许作判断。输出只包括当前 SID、session、elevation/type、完整性、selected interactive/service/admin 组事实以及 Context 摘要，不输出完整 groups、环境、其他用户或 token 原始 buffer。

- `ordinary`：SID 精确匹配外部预期；medium、非提升、普通 Default 或 Limited+deny-only Admin 一致；enabled INTERACTIVE、非 service/batch、非 Session0；Current 必须成功返回精确 SID/任务路径和同一组标量。
- `administrator-rejected`：六项采集仍必须全部成功；真实 SID 是本地/域 RID500，或真实 elevation DWORD 非零。Current 必须返回零 Context 和该事实对应的分类拒绝。只检查 ErrIdentity 不足以通过：查询/Close 失败同样包装这个错误，因此还要求与已成功采集事实所得的分类错误一致，不能把 I/O 失败当作安全拒绝。

对 Current 另作有界本进程句柄计数观测：先预热8次，再读取 baseline，连续32次调用；每次核对成功/拒绝语义并采样句柄数。循环预算5秒，末次计数最多额外等待250ms；最终允许比 baseline 多2个句柄以容忍 Go/runtime 波动。JSON保存baseline/final/peak、完成次数、预算和容差。该窗口可检测每次泄漏一个 token 等持续增长，不能证明机器永久零泄漏，也不会因等待预算结束就声称 native 调用被取消。

使用 [GetProcessHandleCount](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getprocesshandlecount) 只读当前进程；[GetCurrentProcess](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getcurrentprocess) 返回 pseudo handle，不打开/枚举其他进程，也不 Close/复制 pseudo handle。collector 的真实 token 与每次 Current 的真实 token 都必须完成 Close。

本轮交付是可编译的原生验收 fixture，当前状态 **Windows native NOT_RUN**。后续 fresh 普通用户 runner 必须独立确认 SID、仅普通用户组和实际 token 权限；host 管理员拒绝测试与普通用户通过测试分开保存，不以管理员 runner 的结果替代普通权限证明。任务注册/运行、交互会话仍存活、USB和硬件清理均不在此 fixture 的证明范围。
