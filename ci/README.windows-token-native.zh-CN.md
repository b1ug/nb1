# Windows 当前进程 token 原生 fixture

当前状态：**run42565611347两ABI原生token/profile/清理主步骤PASS，但最终证据门禁FAIL；本次报告保留修复原生重跑NOT_RUN**。本工具只为隔离 GitHub Windows VM 提供两个 token 身份用例，不管理Task Scheduler、服务、USB、设备或机器安全策略。不得在开发机器执行 `-Execute`。

需先整合 `taskuser.Current` 与 `TestNativeCurrentOptIn`（token守卫提交3f5d63d，显式opt-in fixture提交6ea176b，以及原生DWORD查询修复6331d4a），再由独立分支 `validation/windows-token-native` 触发 `.github/workflows/windows-token-native.yml`。该workflow不修改既有runtime-native矩阵，不代表当前Mac常驻版本已升级或Windows任务管理已完成。

```powershell
# 默认仅输出PREPARED_NOT_RUN，不加载原生helper、不启动进程或创建用户。
.\ci\windows-token-native.ps1
# 纯注入fake，不调用NativeOps。
.\ci\test-windows-token-native.ps1
# 以下仅用于隔离GitHub托管Windows VM，由workflow调用：
.\ci\windows-token-native.ps1 -Execute -Binary C:\explicit\taskuser.test.exe -OutputDirectory C:\new\evidence
```

执行要求 `GITHUB_ACTIONS=true`、Windows、64位PowerShell 5+宿主。workflow使用Windows2022/PS5.1，分别运行amd64和386/WOW64的test binary；ARM64只编译，不冒充原生执行。测试binary缺少精确opt-in测试名时直接失败，不能把空测试/Skip当作通过。

先运行host实际RID500或elevated管理员的拒绝用例。只有六项采集与Close成功、真实管理员预条件成立且Current拒绝与独立分类一致，才会得到原生PASS；query失败不算拒绝成功。随后创建随机 `nb1tok-` 用户，密码由加密RNG直接填入内存SecureString，既不变成PowerShell明文字符串，也不放入argv/env/文件/日志。SecureString分别交给New-LocalUser和ProcessStartInfo.Password，finally释放。不会使用PasswordInClearText、S4U、batch登录或其它密码模式来“救活”失败用例。

创建前检查精确用户名、预期新profile及私有fixture目录不存在，调用前记录creation intent。唯一Description marker与SID反向核对证明本次账户；部分New-LocalUser失败后仍保守re-query，只清理可归属的新账户，创建完成状态未知即便观察已清理也保留FAIL_CLEANUP。只允许最小BUILTIN Users成员；已出现其他组时失败，不移除其它权限来伪造测试前提。

随机账户名为19字符，Description marker为 `nb1-token-` 加32位GUID共42字符；在任何创建意图前纯检查格式和长度，满足New-LocalUser的Name最多20字符、Description最多48字符限制。[官方参数说明](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.localaccounts/new-localuser?view=powershell-5.1)。首次隔离原生run42562093831证实两ABI管理员拒绝通过，随后旧49字符marker在参数验证失败；普通用户用例尚未开始，整体保留FAIL_CLEANUP，不作为ordinary PASS。

run42562610395中账户创建/marker/SID归属及全部清理通过，groups阶段因将BUILTIN alias错误拼为机器域而IdentityNotMapped，普通用户与profile仍未观察。现在NetUserGetLocalGroups只返回本地alias名称，再用Get-LocalGroup按原名称解析精确本地SAM SID；不做NTAccount域猜测，不继承域权限，不接受查询失败/多结果/名称不符/未知SID。仍只允许唯一Users SID545，其他组失败，不移除它们。[本地组名称结构](https://learn.microsoft.com/en-us/windows/win32/api/lmaccess/ns-lmaccess-localgroup_users_info_0)、[Get-LocalGroup](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.localaccounts/get-localgroup?view=powershell-5.1)。

普通进程用 `ProcessStartInfo.UserName/Domain/Password(SecureString)`、`UseShellExecute=false`、`LoadUserProfile=true` 启动同一SHA256的test binary。只设置固定Go test参数，无shell。EnvironmentVariables先Clear，仅放SystemRoot/WINDIR、System32 PATH、私有TEMP/TMP及三项native协议env；不继承任何runner token、GitHub凭据、SUDO、用户profile或其它环境。三个变量是 `NB1_TASKUSER_NATIVE=1`、`NB1_TASKUSER_EXPECT=ordinary|administrator-rejected`、`NB1_TASKUSER_EXPECT_SID`。每次必须恰有一行固定 `NB1_TASKUSER_NATIVE_RESULT ` 加JSON，原生Go fixture仍是判断Current/句柄语义的权威。

所有启动尝试保留私有Process对象；stdout/stderr同时异步排空，退出后仅接受每路不超过131072字符的证据（不是流内存硬上限）。Start返回后的40秒进程等待超时不会算PASS，只尝试终止该已知私有Process并等待真实退出；启动结果未知或仍存活时绝不删除profile/account。不会按文本找PID、killall、重新制造会话、开启服务、改UAC/登录权限/ACL策略或换另一种登录方式作为fallback。New-LocalUser和Process.Start的同步OS调用不宣称可取消；整个job另有15分钟超时，超时不是清理成功或验收成功。不能满足普通交互token的环境保持FAIL/UNAVAILABLE。

仅新建的fixture目录使用私有ACL：host精确SID与SYSTEM有FullControl，fresh SID只对binary目录ReadAndExecute、对私有tmp有Modify。文件/目录记录NTFS FileID、内容hash、owner和SDDL；遇reparse、替换、ACL变化、未知子文件或部分发布就保留并报清理失败。没有宽泛 `Remove-Item -Recurse`。

清理顺序固定：所有私有children确实退出→重核fresh账户SID/marker→核全部fixture快照→按SID读取ProfileList及Win32_UserProfile、核OS返回profile根路径、限定owner归属依据、FileID/无reparse并等待Loaded=false→`DeleteProfileW(SID,NULL,NULL)`→删除精确已核的fixture文件和空目录→再重核SID/marker并删除该账户→验证account/profile/fixture缺失。profile内部交由OS的精确SID profile API处理，不自行递归遍历/删除或强卸载hive；任何已观察到的路径替换/reparse/未知映射均FAIL_CLEANUP保留，绝不猜路径扩权删除。run42564396412两ABI观察到profile根owner实际为BUILTIN Administrators SID544；不能把owner==freshSID假设当OS保证。全部owner都须证明下列本次创建归属链，再仅接受freshSID或这个已实测的544 owner：账户创建成功+marker/SID归属、登录前profile注册表/CIM/路径都不存在、普通native完整PASS、HKLM/CIM精确freshSID与随机预期路径一致。根DACL另须freshSID有有效FullControl、无根deny及其他SID写权限（SYSTEM/Administrators/host管理员除外；仅继承到children的CREATOR OWNER可接受）。预存profile、partial创建、普通native未通过、未知start均不授权profile删除；首次看到合法映射的根就绑定FileID/owner/SDDL和原始安全描述符hash，此后改变即拒绝；未观察的SYSTEM或未知owner仍拒绝。不修改系统owner、ACL或profile内容来满足验收；新DACL兼容条件需独立原生重跑。账户与profile的OS检查不是针对同一UID或管理员故意并发替换的原子事务。

workflow token仅contents:read、checkout不持久化凭据，不声明secrets；admin权限只用于本次fresh账户、最小Users组、私有fixture ACL与准确清理。主用例失败、原生collection失败、native报告缺失、句柄持续增长、环境不满足、任何清理失败都红。精选artifact只含构建source/二进制hash、纯fake报告、两个native精选JSON、固定Go test错误行和cleanup结果，不上传密码、环境值、完整groups、profile内容或二进制。

依据：[ProcessStartInfo.Password](https://learn.microsoft.com/en-us/dotnet/api/system.diagnostics.processstartinfo.password?view=netframework-4.8.1)、[EnvironmentVariables](https://learn.microsoft.com/en-us/dotnet/api/system.diagnostics.processstartinfo.environmentvariables?view=netframework-4.8.1)、[CreateProcessWithLogonW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createprocesswithlogonw)、[DeleteProfileW](https://learn.microsoft.com/en-us/windows/win32/api/userenv/nf-userenv-deleteprofilew)。这些API资料和软件fake/交叉编译都不替代Windows原生运行证据。普通用户、filtered/Entra token、任务权限与真实Task Scheduler生命周期仍应分别记录实际验收。

profile DACL原始证据由OPEN_REPARSE_POINT句柄上的GetSecurityInfo读取，仅申请READ_CONTROL/READ_ATTRIBUTES及owner/group/DACL，不申请SACL或写ACL/owner权限。返回buffer限制64KiB，LocalFree和SafeHandle统一释放。RawSecurityDescriptor/RawAcl逐ACE检查，拒绝missing/NULL/空DACL、object/callback/opaque/audit/未知ACE和未建模flags/rights，不通过GetAccessRules投影来忽略未知语义。精选结果仅保存原始描述符SHA256和访问判定摘要，不上传完整ACL。

run42565611347两ABI实际满足原始DACL/owner归属条件且profile/account/fixture清理均PASS；整体仍红，因为清理时重复Claim重建fixture_user对象，擦掉先前已核的local_group_sids，最后证据门禁拒绝。现在重复Claim只接受相同name/SID/marker并保留原组证据；缺失group元数据仍明确报错。ARM64步骤当时被红门禁跳过，不声称已完成全job验收。
