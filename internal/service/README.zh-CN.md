# 用户级常驻启动配置

`nb1 service render` 只生成可审阅的启动配置，不安装、启用、注册、启动或停止服务。macOS/Linux候选另有 `install/start/stop/status/uninstall`，按运行平台管理当前普通用户的固定LaunchAgent或systemd用户unit；这些是显式操作，不会随普通CLI启动自动执行。daemon本身仍以前台 `nb1 server` 运行，由操作系统用户级管理器保持运行。

| 平台 | 输出文件 | 运行方式 |
| --- | --- | --- |
| macOS | `org.b1ug.nb1.plist` | 当前登录用户的LaunchAgent |
| Linux | `nb1.service` | systemd用户单元 |
| Windows | `nb1-task.ps1` | 当前用户登录时启动的任务计划，不是SCM Windows Service |

这三种配置都面向同一个用户账号，不承诺开机未登录或退出登录之后仍保持运行。Linux若需要linger或系统级服务，Windows若需要SCM集成，属于额外部署选择，本命令不会自动配置。

## 先生成并检查

```bash
nb1 service render --platform darwin \
  --binary /absolute/path/to/nb1 --devices 20005601 \
  --listen 127.0.0.1:8931 --output org.b1ug.nb1.plist

nb1 service render --platform linux \
  --binary /opt/nb1/nb1 --devices 20005601,30000001 \
  --output nb1.service
```

Windows PowerShell示例：

```powershell
$nb1 = 'C:\Program Files\nb1\nb1.exe'
& $nb1 service render --platform windows --binary $nb1 `
  --devices 20005601 --output .\nb1-task.ps1
Get-Content .\nb1-task.ps1
```

不传 `--output` 时把配置写到标准输出。输出路径须指向新文件，父目录须已存在；不会替换已有文件，也不会创建系统服务目录。可以在任意系统上生成其他平台的配置，路径按目标平台校验，但生成器不验证目标机器上的文件存在、执行权限或硬件支持。

`--binary` 必须是明确的绝对路径，序列号必须是8位十六进制且不能重复；不会选择任意首个HID。`--listen` 仅接受回环地址或localhost，端口1–65535；服务配置拒绝临时随机端口0。带控制字符的路径会被拒绝。`--lock-dir` 可固定一个目标平台绝对目录，否则由实际运行账号决定默认值。

## 所有权目录必须一致

daemon与CLI直控必须使用同一账号、同一所有权目录，才能识别彼此已持有的设备。默认目录为实际运行用户的 `os.UserConfigDir()/nb1/device-locks`：

- macOS：`~/Library/Application Support/nb1/device-locks`。
- Linux：`$XDG_CONFIG_HOME/nb1/device-locks`，未设置时为 `~/.config/nb1/device-locks`。
- Windows：`%AppData%\nb1\device-locks`。

需要自定义时，为daemon与直控CLI都传同一 `--lock-dir`，并核对现有 `NB1_LOCK_DIR` 环境覆盖。更换账号、改变目录或删除仍在使用的锁文件，可能绕过协作锁；不要用这些方式处理“设备忙”。此锁约束本套工具，其他未采用相同协议的软件仍可能直接访问USB。

## macOS：候选用户级安装、启动、停止、状态与卸载

先用上面的 `service render` 生成并检查配置。render仍可在任意平台运行，只输出审阅文件：

```bash
plutil -lint org.b1ug.nb1.plist
```

确认二进制位置和目标设备后，可以在当前普通用户的图形登录会话中显式安装。以下命令会修改真实用户配置。macOS候选已在当前Mac与选定mk2完成临时安装、独立start/stop、幂等不重启、退出读黑及恢复原daemon的19项原生检查；临时配置已移除。该结论不覆盖注销/登录/重启或其他平台。

```bash
nb1 service install --binary /absolute/path/to/nb1 --devices 20005601
nb1 service status --config-only --json
nb1 service status --json
```

`install` 默认先只读查询launchd，再写入 `~/Library/LaunchAgents/org.b1ug.nb1.plist`，不会立即bootstrap。**plist进入LaunchAgents后，下次图形登录可能自动加载**，因此这已经是持久安装，不能把它称为仅生成文件。没有可查询的GUI域、查询失败或已有归属不明的同名注册项时，安装失败，不会把它们当作“未加载”。工具不使用sudo，不写系统LaunchDaemons，不接收任意UID或label。

`status --config-only` 完全不调用launchctl，也不创建目录或锁，只报告磁盘文件；其 `registration: not_checked` 不能表示服务已运行或已经注销。普通 `status` 只读查询固定的 `gui/<当前UID>/org.b1ug.nb1`，分别返回 `config_state` 与 `registration`。`registration: registered` 仅表示launchd注册，不承诺进程正在运行、HTTP可用或USB在线。`nb1 doctor` 可另查API版本和已有设备快照；doctor显示的是当前CLI构建，不核对daemon二进制或实例身份。

需要安装后本次立即加载时显式加 `--start`；已有托管配置可单独 `start`：

```bash
nb1 service install --binary /absolute/path/to/nb1 --devices 20005601 --start
# 已安装配置的单独启动，不需要重填配置参数
nb1 service start --json
nb1 service status --json
nb1 doctor --serial 20005601 --json
```

立即加载前应正常停止占用同一监听端口或设备的手工daemon；安装器不会代停、替换或强杀它。服务先绑定端口，再尝试硬件所有权，注册成功也可能因端口占用、配置或设备问题而尚未提供API。`KeepAlive` 会保持已加载服务，不应把单纯 `kill` 当作永久停用。相同托管配置已注册时，重复install或start不会重启或kickstart进程，即使注册项当前没有运行进程。`start`只加载已有完整托管plist，不创建或接管配置；加载前复核完整内容、inode、当前用户身份和可查询的GUI域，并检查原绝对二进制仍为可执行普通文件、路径未改成符号链接。检查失败就拒绝bootstrap。

单独停止本次注册、保留配置：

```bash
nb1 service stop --json
nb1 service status --config-only --json
```

`stop`和 `uninstall --stop` 共用相同归属守卫、超时与bootout实现，仅对核对过的固定service-target请求注销。`stop`保留原plist内容和inode；**stop不等于禁用下次图形登录加载**，不会写持久disabled状态。注册已经消失且GUI域可查询时幂等返回，既不请求bootout，也不宣称进程退出或USB清理完成。无托管plist的同名注册项、查询错误和检查期间配置变化均拒绝动作。

卸载默认先查注册状态。已注销的完整托管plist可直接删除；仍注册的服务须显式 `--stop`：

```bash
nb1 service uninstall --json
nb1 service uninstall --stop --json
```

`--stop` 只有在磁盘托管标记完整、UID/label一致、当前注册项的顶层path/program精确匹配这份配置时，才会对单个固定service-target调用 `bootout`，随后确认注册项消失再删除plist。不会对整个域操作，不使用 `kickstart -k`、`kill`、`enable/disable` 或可能无限等待的 `bootout --wait`。查询异常、格式无法识别、停止结果不明、文件被修改时均保留配置并返回错误；没有受控plist的孤立注册项也不会被接管。

托管标记直接嵌在plist中，只含版本、固定label、当前UID与明确指定的配置，不采集任意环境变量。读取时必须与完整重渲染内容一致；旧版render文件、人工编辑后的配置和未知来源plist一律拒绝覆盖/删除。临时文件写全并同步后才以不覆盖目标的方式发布，最终文件为0600；目录与文件必须属于当前用户，拒绝符号链接或其他用户可写路径。独立管理锁在 `~/Library/Application Support/nb1/service-management`，不使用设备锁目录，也不删除持久锁文件。启动前、停止前与删除前均重新比较查询之前文件的完整内容和inode，已观察到替换或同inode改写就拒绝操作。管理锁只协调本工具的进程；这些检查与最后的Remove并非原子事务，不承诺防住同UID不合作进程故意卡在检查与操作之间的竞态。

当前不做原地升级替换：相同配置幂等，二进制路径、设备或监听等配置变化时先审阅并正常卸载再安装。旧的人工安装项需要先人工核对来源并正常卸载，候选不会擅自接管。这里没有系统级LaunchDaemon、SMAppService、登录前常驻保证或自动日志轮转；默认plist仍不指定stdout/stderr文件。Linux候选管理见下节；Windows自动管理明确返回unsupported，但候选另有只读status；其他平台仍仅render，不启动管理器或自动提权。

## Linux：候选用户管理与人工部署

Linux上的同名CLI命令使用独立systemd用户管理器，固定管理当前普通用户的 `nb1.service`，不调用launchctl、不提供切换UID或管理平台的flag。只有 `render --platform` 可跨平台生成配置；管理操作始终选择实际运行平台。需要可用的用户systemd manager和受信任的系统systemctl；root、sudo、身份切换或不可查询的用户manager均返回实际错误，不会自动提权、配置Polkit或开启linger。

```bash
nb1 service install --binary /absolute/path/to/nb1 --devices 20005601
nb1 service status --config-only --json
nb1 service status --json
nb1 service start --json
nb1 service stop --json
nb1 service uninstall --json
# 仍活动的unit须明确允许先停止
nb1 service uninstall --stop --json
```

`install` 写入 `os.UserConfigDir()/systemd/user/nb1.service`（Linux通常为 `$XDG_CONFIG_HOME` 或 `~/.config` 下），并创建本工具的 `default.target.wants/nb1.service` 持久启用链接，核对与刷新用户manager；**默认不立即start，但下次用户manager启动或登录可能自动加载**。加 `--start` 才请求本次立即启动。`start` 只立即启动已有配置，不顺便启用登录加载；已active时幂等返回，不restart。`stop` 保留unit和持久启用链接，不等于禁用自启，也不证明设备已经熄灭。卸载已加载但inactive的托管unit不要求 `--stop`；仍活动时必须显式允许停止。

Linux JSON保留独立的 `schema_version: 1` 状态结构，不把systemd字段改成macOS注册字段。磁盘 `config_state`、本工具的 `enable_link_state` / `managed_enabled_for_login`、manager的 `registration` / `properties.load_state`、`active_state` / `sub_state` 与更广的 `unit_file_state` 分别报告。`managed_enabled_for_login: false` 只表示本工具固定链接不存在，不排除其他来源的启用；null表示未检查或无法确认。`registration_owned: false` 也可能表示未查询或无法确认安全边界，不能一律当作外部服务。**loaded、active和归属核对都不等于HTTP可用或USB在线**，请另用doctor检查已有快照。

`status --config-only` 只读磁盘，不调用systemctl、不创建目录或锁；此时 `registration: not_checked`、`properties: null` 不能推断unit已停止或不存在。其他操作保守核对完整文件、inode、当前用户、FragmentPath、持久启用链接、无未知drop-in/transient或reload pending，并要求已加载配置的 `NoNewPrivileges=yes`。未知同名unit、覆盖、文件改写或查询失败均拒绝接管；结果不明时不会自动重试、强杀或回滚，部分文件动作按输出的Actions与状态检查。NoNewPrivileges只限制获得新权限，不代表完整沙箱；不会自动安装udev规则或放宽USB权限，详见[最小权限边界](../../docs/security-boundaries.zh-CN.md)。

此CLI接线目前只经过fake工厂、独立manager模拟及跨平台编译验证，真实Linux用户manager的CI生命周期与Linux原生USB尚待分别验收；不要把软件检查当作实测结论。

### 人工部署render文件

以下是人工管理的替代方式。render文件没有候选管理器要求的完整托管标记；人工安装项不会被自动接管，不能直接改用 `service install` 覆盖。

```bash
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
mkdir -p "$unit_dir"
cp nb1.service "$unit_dir/nb1.service"
systemctl --user daemon-reload
systemctl --user enable --now nb1.service
systemctl --user status nb1.service
journalctl --user -u nb1.service -n 100 --no-pager
nb1 control --endpoint http://127.0.0.1:8931 devices
```

卸载：

```bash
systemctl --user disable --now nb1.service
rm "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/nb1.service"
systemctl --user daemon-reload
```

单元在用户manager中运行，不需要把它放到 `/etc/systemd/system`。`ExecStart`不经过shell；每个参数独立转义，`%%`保留字面百分号，`:`前缀关闭环境变量替换以保留字面 `$`。不会自动配置USB访问规则或补齐目标机器的CGO运行环境。

## Windows候选：仅只读status与render

当前CLI仅提供候选只读查询，尚未实现Windows任务管理和可靠stop协议：

```powershell
& $nb1 service status
& $nb1 service status --json
```

构造器用当前token核验普通用户/SID/session，再固定查询 `\B1UG-nb1-<SID>`；不能传任意任务名、账号或远程机器，不读取完整XML、action、命令或环境。每次查询复核身份；PowerShell来自系统API及owner/DACL/reparse检查，固定脚本读自己的SID也显式使用TokenAccessLevels.Query。不会提权或回退其他身份。

JSON原样保留Windows查询schema：`presence`、`ownership: not_checked`、有限task属性与failure。查询出错先输出部分状态，再非零退出；构造阶段身份/信任拒绝不编造报告。`present`只说明同名存在，不代表managed；Scheduler ready/running/LastTaskResult也不是daemon PID、HTTP、USB在线或熄灯证明。GetTask的缺失HRESULT尚未有普通用户原生合同，0x80070002暂为unknown/not_found_unverified，不报absent。

Windows `install/start/stop/uninstall` 和 `status --config-only` 在任何查询器构造前拒绝，不修改任务或配置。默认软件测试只注入fake reader；普通用户原生PowerShell/COM查询仍待独立Windows验收，跨编译和管理员host观察不替代。底层边界见[taskquery说明](taskquery/README.zh-CN.md)。

## Windows：人工注册、状态与移除（独立人工流程）

检查生成的 `nb1-task.ps1` 后，手动运行它才会注册任务：

```powershell
.\nb1-task.ps1
$taskName = 'B1UG-nb1-' + [System.Security.Principal.WindowsIdentity]::GetCurrent([System.Security.Principal.TokenAccessLevels]::Query).User.Value
Get-ScheduledTask -TaskName $taskName
Get-ScheduledTaskInfo -TaskName $taskName
Start-ScheduledTask -TaskName $taskName
& $nb1 control --endpoint http://127.0.0.1:8931 devices
```

脚本注册当前账号的交互登录触发器与普通权限principal；账号SID使任务名称在不同用户之间区分。注册不会立即启动daemon；下次登录会触发，立即运行须另行 `Start-ScheduledTask`。已存在同名任务时脚本报错，不会使用 `-Force` 替换。运行是否获准还取决于本机任务计划策略与账号权限。

停止与卸载：

```powershell
Stop-ScheduledTask -TaskName $taskName
Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
```

任务计划程序会展开action参数中的环境变量。因此目标路径和参数放在UTF-16LE Base64启动器中，而不是直接拼成任务action的命令行；启动器通过 `ProcessStartInfo` 的 `UseShellExecute=false` 启动nb1，并使用Windows原生参数规则保护尾部反斜杠、空格、百分号和引号。外层脚本保持ASCII，避免Windows PowerShell5.1对无BOM UTF-8的差异。审阅 `$encoded` 时可使用 `[Text.Encoding]::Unicode.GetString([Convert]::FromBase64String('复制这里的编码值'))` 只解码、不执行。

任务不以管理员或LocalSystem账号运行，单次执行时限显式设为无限，避免默认72小时后停止；失败最多重启3次，间隔1分钟。这里没有实现SCM控制协议，也不会执行 `sc create`。任务管理器或任务计划的停止可能强制结束进程，不能据此保证设备清理已完成。

## 退出与验证边界

macOS和Linux管理器先请求进程退出，配置给20秒退出预算；当前daemon自身有10秒清理等待。若原生I/O阻塞或管理器最终强杀进程，不能保证最后的熄灯和自主播放停止已完成。Windows任务停止也不是等价的优雅关闭协议。退出后应检查新设备读回，不能把“进程已停止”当作“灯已完成清理”。

当前测试覆盖XML参数往返、systemd转义、PowerShell编码和字符串、无效输入先于文件创建、已有文件不被替换。macOS管理候选另以临时home和fake launchctl覆盖安装与独立start/stop、幂等、不重启、外部同名项、归属核对、未知/修改文件、同内容inode替换、符号链接、二进制检查、管理锁、查询异常、取消、bootstrap/bootout不确定结果及配置保留。Linux独立manager另有临时配置与fake systemctl测试；CLI用fake工厂验证按当前平台选择、动作参数、错误状态保留和unsupported不构造管理器，未执行真实systemctl。另有仅在Windows运行的实际子进程argv往返测试。这些检查不等于已在三平台安装服务或验证重启、注销、USB权限及真机生命周期；本机当前daemon不会被这些测试停止。

## 官方依据

- [Apple：LaunchAgent、登录加载与ProgramArguments](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)；当前 `bootstrap/bootout/print`、GUI域和退出码语义以目标Mac的 `launchctl help`、`man launchctl` 与 `man launchd.plist` 核对。当前Mac只读核对缺失服务print退出113、GUI域print成功；其他错误均不当作缺失。`man launchctl` 明示新式print输出不是稳定API，因此本候选严格识别有限格式，识别失败保守拒绝停止/删除，未据此宣称新老系统生命周期均已验收。
- [systemd.service：命令行、前缀及变量替换](https://www.freedesktop.org/software/systemd/man/262/systemd.service.html)、[systemd.syntax：引号与转义](https://github.com/systemd/systemd/blob/main/man/systemd.syntax.xml)。
- [Microsoft：ScheduledTask principal](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtaskprincipal?view=windowsserver2025-ps)、[登录触发器](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasktrigger?view=windowsserver2025-ps)、[任务设置](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasksettingsset?view=windowsserver2025-ps)。
- [Microsoft：ExecAction环境变量](https://learn.microsoft.com/en-us/windows/win32/taskschd/execaction)、[EncodedCommand编码](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_powershell_exe?view=powershell-5.1)、[原生命令行引号规则](https://learn.microsoft.com/en-us/cpp/c-language/parsing-c-command-line-arguments?view=msvc-170)、[无限执行时限](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-executiontimelimit)。
