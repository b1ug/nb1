# 用户级常驻启动配置

`nb1 service render` 只生成可审阅的启动配置，不安装、启用、注册、启动或停止服务。当前的daemon是前台运行的 `nb1 server`，由操作系统的用户级管理器保持运行。

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

## macOS：人工安装、状态与卸载

先检查生成的plist：

```bash
plutil -lint org.b1ug.nb1.plist
```

确认二进制位置和目标设备后，在当前图形登录用户的终端手动执行：

```bash
mkdir -p "$HOME/Library/LaunchAgents"
cp org.b1ug.nb1.plist "$HOME/Library/LaunchAgents/org.b1ug.nb1.plist"
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/org.b1ug.nb1.plist"
launchctl print "gui/$(id -u)/org.b1ug.nb1"
nb1 control --endpoint http://127.0.0.1:8931 devices
```

停止、取消下次登录启动并删除该配置：

```bash
launchctl bootout "gui/$(id -u)/org.b1ug.nb1"
rm "$HOME/Library/LaunchAgents/org.b1ug.nb1.plist"
```

`KeepAlive`在加载后保持进程运行，避免用单纯的 `kill` 作为永久停用方法。这里没有安装系统LaunchDaemon，也没有为GUI应用配置SMAppService。默认plist未指定stdout/stderr文件，设备状态通过API/CLI读取；若需要文件日志，可先人工加入指向已存在目录的 `StandardOutPath` / `StandardErrorPath`，再加载配置。

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

当前测试覆盖XML参数往返、systemd转义、PowerShell编码和字符串、无效输入先于文件创建、已有文件不被替换。另有仅在Windows运行的实际子进程argv往返测试。生成器通过本机软件检查不等于已在三平台安装服务或验证重启、注销、USB权限及真机生命周期。

## 官方依据

- [Apple：LaunchAgent与ProgramArguments](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)；当前 `launchctl bootstrap/bootout/print` 语法也可在目标Mac运行 `man launchctl` 核对。
- [systemd.service：命令行、前缀及变量替换](https://www.freedesktop.org/software/systemd/man/262/systemd.service.html)、[systemd.syntax：引号与转义](https://github.com/systemd/systemd/blob/main/man/systemd.syntax.xml)。
- [Microsoft：ScheduledTask principal](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtaskprincipal?view=windowsserver2025-ps)、[登录触发器](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasktrigger?view=windowsserver2025-ps)、[任务设置](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtasksettingsset?view=windowsserver2025-ps)。
- [Microsoft：ExecAction环境变量](https://learn.microsoft.com/en-us/windows/win32/taskschd/execaction)、[EncodedCommand编码](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_powershell_exe?view=powershell-5.1)、[原生命令行引号规则](https://learn.microsoft.com/en-us/cpp/c-language/parsing-c-command-line-arguments?view=msvc-170)、[无限执行时限](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-executiontimelimit)。
