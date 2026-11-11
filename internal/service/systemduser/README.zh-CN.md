# Linux用户服务管理候选

此包只管理当前普通用户的固定 `nb1.service`，接口为 `Install(ctx, Config, start)`、`Status(ctx, configOnly)`、`Start(ctx)`、`Stop(ctx)`、`Uninstall(ctx, stop)`。它使用父包的 `service.Config` 和 `service.Render`，返回独立 `Status`，已接入当前平台的 CLI 工厂。`nb1 service install --devices SERIAL` 默认由 CLI 取得当前可执行文件路径；需要指定另一份程序时使用 `--binary`。本包继续验证可执行文件与服务配置，不自动启动其他用户或系统级服务。

本轮在Mac的临时目录和fake runner中验证软件协议；没有在Mac运行systemctl，也没有执行真实Linux安装、启动、停止、权限或USB操作。Linux user-manager的真实验证留给后续专门CI。

## 安装、活动和启用分别处理

生产构造只在Linux普通UID下可用，拒绝root、sudo身份切换；固定选择系统的 `/usr/bin/systemctl` 或 `/bin/systemctl`，不搜索用户PATH、不接任意unit、UID、host、machine或shell命令。配置根与应用配置、库和设备锁共用 `userconfig.RootDir()`；Linux默认是 `~/.config`，非空 `XDG_CONFIG_HOME` 必须是绝对路径且为安全的当前用户目录。最终归属还必须与manager的 `FragmentPath` 对上，客户端和manager的XDG环境不一致不会被当作安装成功。

| 接口 | 默认行为 |
| --- | --- |
| Install | 先查同名单元，写完整托管unit，reload并确认无覆盖，再建立固定default.target启用链接，reload复验；默认下次用户manager启动可能自启 |
| Install(start=true) | 完成上述持久安装后再请求立即start；active不等于HTTP或USB就绪 |
| Status(configOnly=true) | 只读磁盘unit/链接，不创建目录或锁，不调用systemctl |
| Status(configOnly=false) | 另读机器属性，区分loaded、active和UnitFileState |
| Start | 只启动已有完整托管配置；不顺带启用登录启动、不restart已有active进程 |
| Stop | 只请求停止，保留unit和现有登录启用；不删除链接或配置 |
| Uninstall(stop=false) | loaded但inactive/failed且无job时可卸载；活动单元必须显式stop=true |
| Uninstall(stop=true) | 只停止归属已确认的活动单元，再移除本工具链接和unit |

unit位置是 `<用户配置根>/systemd/user/nb1.service`，启用链接固定为 `default.target.wants/nb1.service`，链接目标必须是该unit的精确绝对路径。`managed_enabled_for_login` 仅描述本工具这条持久链接；`unit_file_state` 是manager看到的更广启用状态。不配置linger、系统服务、登录前常驻或退出登录后的运行保证。

## 机器接口与归属

查询使用 `systemctl --user --no-pager --no-ask-password show --all --property=... nb1.service`，只请求Id、LoadState、ActiveState、SubState、FragmentPath、SourcePath、DropInPaths、Transient、NeedDaemonReload、UnitFileState、Job及NoNewPrivileges。不会读取Environment、任意进程参数或人类status文本，也不用PID猜测归属。[systemctl官方文档](https://www.freedesktop.org/software/systemd/man/latest/systemctl.html)将show定义为机器可解析接口；各属性含义见[官方D-Bus接口](https://www.freedesktop.org/software/systemd/man/latest/org.freedesktop.systemd1.html)。

属性缺失、重复、未知、截断或矛盾时状态未知。只有完整且自洽的not-found属性以及可接受返回码才报告absent；用户bus不可达不能被当作unit不存在。无job的show输出可能是空值，按官方打印实现处理；未完成job不被start/stop替换。

改变已加载单元前须确认：完整托管文件、当前UID/固定unit、正确FragmentPath、空SourcePath/DropInPaths、非Transient、无需reload、启用状态和本工具链接相符。masked、runtime启用、未知覆盖或来源均拒绝操作。非空drop-in目录也不会被接管。开始和停止前重读最初unit的全部内容、inode和链接；外层Uninstall的快照会传入内部Stop，不能在第二次查询时接管另一份新配置。

## 保留未知文件与部分失败

unit首行嵌入版本、UID、unit名和明确配置的托管标记，读回必须与完整重渲染内容逐字节相同。手工文件、修改后的文件或身份不一致均不覆盖。文件和目录须为安全归属，不接受unit或子目录symlink；新目录0700、新unit0600。先完整写入、同步临时文件，再以不覆盖现有目标的方式发布。相同配置幂等，不做原地升级替换。

不调用 `systemctl disable`：它可能移除比此前enable创建的更多链接。只移除本工具固定且重新核验的default.target链接，随后reload并复验disabled；若仍enabled，说明还有外部引用或未知状态，保留unit，不去删除其他链接。最后才删除unit、reload并确认not-found。必要reload使用当前用户manager，不请求停止或修改其他unit。[enable/disable差异](https://www.freedesktop.org/software/systemd/man/latest/systemctl.html#disable%20UNIT%E2%80%A6)与[用户unit搜索路径](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html)是此策略的依据。

每一步失败都保留当前已知文件和动作记录，不自动重试控制、不强杀、不删除未知链接，也不假装已经回滚。独立持久管理锁在 `<用户配置根>/nb1/systemd-user-management`，不触设备锁。锁只协调本工具；最后检查与Remove并不是抵御同UID故意竞态的原子事务。

## 权限与验证边界

不使用sudo、force、Polkit自动授权或shell；`--no-ask-password`禁止交互授权。只读查询预算3秒，整个改变操作预算30秒。超时针对私有systemctl命令，不承诺取消原生I/O或完成硬件清理。停止后的inactive/failed也不证明灯已熄灭，应用和硬件须另行验证。

已加载属性还要求 `NoNewPrivileges=yes`；false或无法读取时拒绝接管改变。最终集成须使用已经加入该选项的父Render。此隔离分支基于旧父模板，fake runner中的该属性用于协议回归，不能被称为实际Linux执行证明。NoNewPrivileges限制通过exec获得额外权限，并非完整sandbox；本包不盲加未经USB、目录和运行环境验证的额外限制。[官方NoNewPrivileges说明](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#NoNewPrivileges=)

实际Start前还验证binary仍为安全的可执行普通文件，拒绝新symlink或他人可写文件；Stop/Uninstall不会因binary消失而失去清理能力。普通用户本身已有的权限、manager会话环境和不合作同UID进程不能据此被完整隔离。

安装时会把已核验的当前用户配置根目录写入托管定义，并通过unit的`XDG_CONFIG_HOME`固定给子进程。这样即使用户manager继承了其他会话的变量，也不会把配置、默认库或设备锁转向另一个账号的目录；不修改任何目录权限。portable render可显式给`--user-config-root`，原生安装不接受与manager上下文不同的根目录。
