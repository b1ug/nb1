# 用户级常驻启动配置

`nb1 service render` 只生成可审阅的启动配置，不安装、启用、注册、启动或停止服务。macOS候选另有 `install/status/uninstall` 管理当前用户的托管LaunchAgent；这些是显式操作，不会随普通CLI启动自动执行。daemon本身仍以前台 `nb1 server` 运行，由操作系统用户级管理器保持运行。

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

## macOS：候选用户级安装、状态与卸载

先用上面的 `service render` 生成并检查配置。render仍可在任意平台运行，只输出审阅文件：

```bash
plutil -lint org.b1ug.nb1.plist
```

确认二进制位置和目标设备后，可以在当前普通用户的图形登录会话中显式安装。以下命令会修改真实用户配置；本轮开发验证仅在临时目录和模拟launchctl中执行，没有替本机安装、启动或卸载服务。

```bash
nb1 service install --binary /absolute/path/to/nb1 --devices 20005601
nb1 service status --config-only --json
nb1 service status --json
```

`install` 默认先只读查询launchd，再写入 `~/Library/LaunchAgents/org.b1ug.nb1.plist`，不会立即bootstrap。**plist进入LaunchAgents后，下次图形登录可能自动加载**，因此这已经是持久安装，不能把它称为仅生成文件。没有可查询的GUI域、查询失败或已有归属不明的同名注册项时，安装失败，不会把它们当作“未加载”。工具不使用sudo，不写系统LaunchDaemons，不接收任意UID或label。

`status --config-only` 完全不调用launchctl，也不创建目录或锁，只报告磁盘文件；其 `registration: not_checked` 不能表示服务已运行或已经注销。普通 `status` 只读查询固定的 `gui/<当前UID>/org.b1ug.nb1`，分别返回 `config_state` 与 `registration`。`registration: registered` 仅表示launchd注册，不承诺进程正在运行、HTTP可用或USB在线。`nb1 doctor` 可另查API版本和已有设备快照；doctor显示的是当前CLI构建，不核对daemon二进制或实例身份。

需要本次立即加载时必须显式加 `--start`：

```bash
nb1 service install --binary /absolute/path/to/nb1 --devices 20005601 --start
nb1 service status --json
nb1 doctor --serial 20005601 --json
```

立即加载前应正常停止占用同一监听端口或设备的手工daemon；安装器不会代停、替换或强杀它。服务先绑定端口，再尝试硬件所有权，注册成功也可能因端口占用、配置或设备问题而尚未提供API。`KeepAlive` 会保持已加载服务，不应把单纯 `kill` 当作永久停用。相同托管配置已注册时，重复install不会重启或kickstart进程。

卸载默认先查注册状态。已注销的完整托管plist可直接删除；仍注册的服务须显式 `--stop`：

```bash
nb1 service uninstall --json
nb1 service uninstall --stop --json
```

`--stop` 只有在磁盘托管标记完整、UID/label一致、当前注册项的顶层path/program精确匹配这份配置时，才会对单个固定service-target调用 `bootout`，随后确认注册项消失再删除plist。不会对整个域操作，不使用 `kickstart -k`、`kill`、`enable/disable` 或可能无限等待的 `bootout --wait`。查询异常、格式无法识别、停止结果不明、文件被修改时均保留配置并返回错误；没有受控plist的孤立注册项也不会被接管。

托管标记直接嵌在plist中，只含版本、固定label、当前UID与明确指定的配置，不采集任意环境变量。读取时必须与完整重渲染内容一致；旧版render文件、人工编辑后的配置和未知来源plist一律拒绝覆盖/删除。临时文件写全并同步后才以不覆盖目标的方式发布，最终文件为0600；目录与文件必须属于当前用户，拒绝符号链接或其他用户可写路径。独立管理锁在 `~/Library/Application Support/nb1/service-management`，不使用设备锁目录，也不删除持久锁文件。停止前与删除前均重新比较最初文件的完整内容和inode，已观察到替换或同inode改写就拒绝操作。管理锁只协调本工具的进程；这些检查与最后的Remove并非原子事务，不承诺防住同UID不合作进程故意卡在检查与操作之间的竞态。

当前不做原地升级替换：相同配置幂等，二进制路径、设备或监听等配置变化时先审阅并正常卸载再安装。旧的人工安装项需要先人工核对来源并正常卸载，候选不会擅自接管。这里没有系统级LaunchDaemon、SMAppService、登录前常驻保证或自动日志轮转；默认plist仍不指定stdout/stderr文件。Linux/Windows的自动管理明确返回unsupported，仍保留跨平台render和下方人工部署说明。

## Linux：人工安装、状态与卸载

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

## Windows：人工注册、状态与移除

检查生成的 `nb1-task.ps1` 后，手动运行它才会注册任务：

```powershell
.\nb1-task.ps1
$taskName = 'B1UG-nb1-' + [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
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

当前测试覆盖XML参数往返、systemd转义、PowerShell编码和字符串、无效输入先于文件创建、已有文件不被替换。macOS管理候选另以临时home和fake launchctl覆盖安装、幂等、外部同名项、归属核对、未知/修改文件、符号链接、查询异常、bootstrap/bootout不确定结果及卸载保留。另有仅在Windows运行的实际子进程argv往返测试。这些检查不等于已在三平台安装服务或验证重启、注销、USB权限及真机生命周期；本机当前daemon不会被这些测试停止。

## 官方依据

- [Apple：LaunchAgent、登录加载与ProgramArguments](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)；当前 `bootstrap/bootout/print`、GUI域和退出码语义以目标Mac的 `launchctl help`、`man launchctl` 与 `man launchd.plist` 核对。当前Mac只读核对缺失服务print退出113、GUI域print成功；其他错误均不当作缺失。`man launchctl` 明示新式print输出不是稳定API，因此本候选严格识别有限格式，识别失败保守拒绝停止/删除，未据此宣称新老系统生命周期均已验收。
- [systemd.service：命令行、前缀及变量替换](https://www.freedesktop.org/software/systemd/man/262/systemd.service.html)、[systemd.syntax：引号与转义](https://github.com/systemd/systemd/blob/main/man/systemd.syntax.xml)。
- [Microsoft：ScheduledTask principal](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtaskprincipal?view=windowsserver2025-ps)、[登录触发器](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasktrigger?view=windowsserver2025-ps)、[任务设置](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasksettingsset?view=windowsserver2025-ps)。
- [Microsoft：ExecAction环境变量](https://learn.microsoft.com/en-us/windows/win32/taskschd/execaction)、[EncodedCommand编码](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_powershell_exe?view=powershell-5.1)、[原生命令行引号规则](https://learn.microsoft.com/en-us/cpp/c-language/parsing-c-command-line-arguments?view=msvc-170)、[无限执行时限](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-executiontimelimit)。
