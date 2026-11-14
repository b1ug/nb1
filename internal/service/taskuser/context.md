# 当前进程 Windows 用户身份守卫

这一步只增加 `taskuser.Current() (Context, error)`，不接 CLI、不查询/安装/运行任务、不运行 PowerShell、不触碰 USB。Context 字段私有，零值无效，成功值只能由 Current 公开获取；SID/TaskPath/SessionID/IntegrityRID/ElevationType/AdminMembership 是只读 getter。其他包须先检查错误与 `Valid()`，不能用用户提供的 SID 代替此身份。

Current 使用 Go 1.18 标准库 `syscall.OpenCurrentProcessToken`，它只请求 **TOKEN_QUERY**。只读取这个 token 的 User、Elevation、ElevationType、IntegrityLevel、Groups、SessionId，不查询其他用户、不读取权限全集/linked token、不修改或复制 token。所有拿到的真实 token 均由共同路径 Close；读取、分类或 Close 失败都返回零 Context。GetTokenInformation 最多四次、64 KiB；读取原生指针值后只计算其在同一返回 buffer 的偏移，不解引用外来地址。32/64 位布局来自原生 Sizeof/Offsetof。

成功需满足：

- canonical 本地/域用户 SID `S-1-5-21-a-b-c-rid`，或 Microsoft 文档中的 Entra `S-1-12-1-a-b-c-d` / `S-1-12-a-b-c-d` 形式；任务固定为 `\B1UG-nb1-<SID>`。其他 SID 族明确拒绝为尚未验证。
- Elevation=0、完整性恰为 Medium `S-1-16-8192`、非零普通 session、enabled INTERACTIVE token group。Low/MediumPlus/High/System 或未知 label 不在本阶段支持合同内。
- 普通 Default token 不含 Administrators；UAC filtered token 则必须 Limited，且 Administrators **deny-only、不 enabled**。不会把正常 filtered 管理员账户一律误判为 elevated。
- SYSTEM、Local/NetworkService、服务 SID、service/batch logon、Session 0、enabled Administrators、Full、缺失/矛盾字段均拒绝。RID500 仅适用于本地/域账号，不误伤末段为500的 Entra SID。

`Valid()` 只证明这些 token 条件，并不证明 Windows 任务计划所需的交互会话仍存在、COM/任务权限可用、机器有设备或任务/daemon 已运行。Context 是一次读取的快照，不是永久授权；未来管理动作须在当前进程边界重新核实。Unix Current 明确返回 ErrUnsupported。

测试只验证分类、fake token 查询/Close、有限长度原生结构解码和尺寸增长。Mac 上 software PASS、Windows 交叉编译都不算原生 Windows token PASS；仍待 Windows 普通用户、filtered token、Entra 与服务账号的原生环境补证。

依据均为官方资料：

- [Go 1.18 syscall 实现](https://github.com/golang/go/blob/go1.18.10/src/syscall/security_windows.go)：TOKEN_QUERY 与真实 token Close。
- [GetTokenInformation](https://learn.microsoft.com/en-us/windows/win32/api/securitybaseapi/nf-securitybaseapi-gettokeninformation)、[TOKEN_MANDATORY_LABEL](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-token_mandatory_label)：查询权限、可变响应与标签布局。
- [TOKEN_ELEVATION](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-token_elevation)、[ElevationType](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ne-winnt-token_elevation_type)、[TOKEN_GROUPS](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-token_groups)：非零 elevation、Default/Full/Limited 和 deny-only 组。
- [Well-known SIDs](https://learn.microsoft.com/en-us/windows/win32/secauthz/well-known-sids)：INTERACTIVE/service/batch/Admin 等 SID。
- [Entra whoami 示例](https://learn.microsoft.com/en-us/troubleshoot/entra/entra-id/dir-dmns-obj/no-local-admin-privileges-azure-ad-joined-device)、[LocalUsersAndGroups CSP](https://learn.microsoft.com/en-us/windows/client-management/mdm/policy-csp-localusersandgroups)：Entra SID 形式；没有进行网络账号枚举。
