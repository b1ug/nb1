# Windows 当前用户任务：只读查询适配器

这是独立的只读底层部件，尚未接入 CLI 或服务 manager。`New() (*Reader,error)` 从 `taskuser.Current()` 取得不可公开构造的已验证身份；`Query(context.Context) (Status,error)` 仅查询该身份固定的 `\B1UG-nb1-<SID>`。调用者不能传入用户、SID、任务名、服务器或程序路径。每次查询前再次核对当前 SID、任务路径与 session；查询层不重新限制 SID 家族。

普通用户及 token 的资格由 taskuser 判定。其他平台构造返回 `ErrUnsupported`；身份未知或改变时不启动桥。构造不联系 Task Scheduler，查询没有自动重试。

## 数据与权限边界

- 只连接本机 Schedule.Service，Connect() 不传服务器、账号或凭据，然后只读取准确任务的 Path、Enabled、State、LastTaskResult。不开枚举接口，不读完整 XML、action、命令、环境或凭据，也不读取 EnginePID。
- presence=present 只证明同名注册存在。ownership=not_checked 始终保留，不能据此称 managed、daemon 已运行、HTTP ready 或 USB 在线。Scheduler 的 ready、running 和上次结果也不是硬件清理结果。
- 任何连接、权限、服务、COM、输出/协议或取消错误都保留 unknown。微软 GetTask 只规定失败返回 HRESULT，尚没有本项目普通用户原生缺失合同；get_task/0x80070002 特别报告 not_found_unverified，**仍为 unknown**。Absent 常量仅保留未来合同位置，生产代码当前没有返回 absent 的路径。
- 没有注册、启用、Run、Stop、删除、ACL 修改、提权、UAC、密码或远程管理。默认软件测试不启动 PowerShell、COM 或任务；测试私有子进程只回显协议和验证输出/退出预算。

## 固定桥和可信路径

PowerShell 路径来自 GetSystemDirectoryW，再追加固定 WindowsPowerShell\v1.0\powershell.exe；不相信调用环境的 PATH、SystemRoot 或可执行路径。逐项检查已有路径的非 reparse 属性、受保护 owner 与 DACL：只允许 SYSTEM、Administrators、TrustedInstaller 拥有对象；拒绝非受信 principal 获得文件写入或祖先替换权限。卷根可允许仅创建新子项，但不能删除已有子项/改 owner/DACL。未知 ACE 类型或无法查询安全描述符均拒绝，不尝试修复权限。

这是保守白名单，不是通用 Windows effective-access 计算器；deny ACE 不会放宽可疑 allow。保护检查不抵御管理员/系统在检查后更改文件，不是完整沙箱，也未证明每种 Windows/WOW64 布局都可用。

桥使用固定 argv：`-NoLogo -NoProfile -NonInteractive -InputFormat Text -OutputFormat Text -Command <固定代码>`。输入是 stdin JSON，不插值进代码。进程只带从系统 API 派生的 SystemRoot/WINDIR/PATH 和空 PSModulePath；工作目录固定系统目录。脚本禁用模块自动加载，使用强名称 .NET JSON 程序集和本机 COM，finally 释放 task/folder/service RCW。遇到企业约束语言/程序集限制时报告未知，不使用 ExecutionPolicy Bypass 或备用提权路径。

子进程读取自己的 SID 也只申请 TokenAccessLevels.Query，显式使用 WindowsIdentity.GetCurrent(Query)；不使用默认请求 MaximumAllowed 的无参重载。实际子进程 argv 回归保留此权限边界，Windows 原生取证另列。

Query内使用调用者期限与15秒中的较短者；此前的New构造阶段不计入这段预算。同步 token/系统文件/ACL 检查在调用前后核对 context，但这些 OS 检查没有原生强制取消接口，不承诺它们有硬退出期限。stdout最多16KiB，stderr只计至4KiB后取消且不保留内容；输出超限立即请求终止私有桥。Go1.18的 CommandContext 只管这个固定子进程，不把取消/进程退出说成 COM 服务或任意后台操作被原子取消。这里没有控制任务的副作用。

## 首次桥接预算候选：待正常 Query 原生复验

原三秒预算的[普通用户正常 Query 验收](https://github.com/b1ug/nb1/actions/runs/42568948786)中，amd64通过，386/WOW64真实返回deadline，整体失败保留。不能将该失败解释为任务缺失，也不能因构造通过就推断PowerShell或COM已完成。

随后独立[计时诊断 run42572206853](https://github.com/b1ug/nb1/actions/runs/42572206853)（源码`2916dbc`）在全新普通用户profile中先测原固定桥：386/WOW64第一样本10593ms、第二257ms；amd64为355ms、204ms，样本身份/重复trust检查为0–1ms。诊断不调用生产Query，结果只是DIAGNOSTIC_ONLY。这是有限样本，bridge耗时包含信任检查、PowerShell启动、固定脚本、COM及Wait，不能单独归因于启动或COM，也不是受控OS缓存冷/热实验。

据此将默认有限预算调整为15秒；调用者更早的deadline仍优先。没有重试、Sysnative切换、权限、身份、固定目标或缺失语义变化。**本15秒候选尚待全新普通用户profile下的正常生产Query原生复验**；软件测试、交叉编译和预热后的成功不能代替首次调用验收，既有三秒失败与独立诊断收据继续保留。

## 验证范围

软件测试覆盖本地/Entra 已验证身份、身份变化不执行、严格 JSON、原生错误阶段、missing 保持 unknown、未知字段/重复键/敏感字段拒绝、固定代码与 stdin 分离、干净环境、输出限长、私有子进程取消、系统路径和保守 ACL 决策。跨编译只是编译，不是 Windows 原生 COM PASS。

```sh
GOWORK=off CGO_ENABLED=0 go test ./internal/service/taskquery
GOWORK=off CGO_ENABLED=1 go test -race ./internal/service/taskquery
GOWORK=off CGO_ENABLED=0 go vet ./internal/service/taskquery
```

未来只有在已证明为普通当前用户的 Windows 环境中显式设置 `NB1_TASKQUERY_NATIVE=1`，才运行 TestNativeCurrentTaskQueryOptIn。它只记录选定状态；即使观察到0x80070002也不宣称缺失合同已经开放。Hosted Windows默认管理员/UAC关闭不能替代该环境。本提交没有执行这项原生测试、注册任务或操作USB。

## 官方依据

- [GetTask：精确路径与HRESULT](https://learn.microsoft.com/en-us/windows/win32/api/taskschd/nf-taskschd-itaskfolder-gettask)
- [Connect：空服务器与当前token](https://learn.microsoft.com/en-us/windows/win32/api/taskschd/nf-taskschd-itaskservice-connect)
- [TASK_STATE](https://learn.microsoft.com/en-us/windows/win32/api/taskschd/ne-taskschd-task_state)
- [LastTaskResult](https://learn.microsoft.com/en-us/windows/win32/api/taskschd/nf-taskschd-iregisteredtask-get_lasttaskresult)
- [GetSystemDirectoryW](https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/nf-sysinfoapi-getsystemdirectoryw)
- [GetNamedSecurityInfoW与LocalFree](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-getnamedsecurityinfow)
- [ACCESS_ALLOWED_ACE布局](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-access_allowed_ace)
- [PowerShell5.1命令参数](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_powershell_exe?view=powershell-5.1)
- [Microsoft .NET Framework WindowsIdentity源码与默认MaximumAllowed](https://github.com/microsoft/referencesource/blob/main/mscorlib/system/security/principal/windowsidentity.cs)
