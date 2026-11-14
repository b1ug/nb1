# Windows 本机 Pattern 存储边界

这是 `internal/patternstore` 的 Windows 文件事务实现说明。它属于隔离开发候选，尚未接入当前运行中的 daemon、HTTP、CLI 或 MCP；不控制设备，不写硬件 RAM／Flash，也不在启动时自动播放图案。

## 当前选择的文件系统

实现仅接受**固定本地盘上的 NTFS，且卷提供持久 ACL**。UNC／网络共享、设备命名空间、可移动盘、FAT／exFAT 和其他文件系统明确返回不支持；不通过复制、其他目录或更宽松权限静默降级。实际保存目录由公共 Store 配置选择，本模块不会猜测另一个路径。

在读取或创建管理对象前，路径检查拒绝非法 UTF-8、控制字符、备用数据流语法、末尾空格／点以及保留设备名。保留名检查覆盖 `CONIN$`、`CONOUT$`、`COM¹/²/³`、`LPT¹/²/³` 和扩展名前带空格的设备名，例如 `NUL .txt`。[Microsoft 文件命名说明](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)

最终管理目录本身不能是重解析点。已存在的父路径可以先解析为规范位置；解析结果须再次通过本地固定盘检查，再打开管理对象。新建目录前还会检查父目录所在卷的 NTFS／持久 ACL 能力，避免在不支持的位置先创建再拒绝。卷根可以作为合法父目录，但不能直接用作图案库。管理对象的打开和发布再使用已取得的目录句柄作为锚点。

## 用户归属与权限

新建管理目录、`writer.lock` 和临时快照时，安全描述符显式指定当前进程 token 的用户 SID 为 owner，设置受保护的 DACL。允许主体仅为当前用户、SYSTEM 和 Administrators；当前用户须有完整访问权限。每次操作也会检查已存在的管理对象，不把 Unix 的 `0700/0600` 数字当作 Windows ACL 验证。

管理对象的 DACL 必须具有 `SE_DACL_PROTECTED`，避免以后从父目录继承更宽权限。已有父目录可由当前用户、SYSTEM 或 Administrators 拥有；其他主体可以读取／遍历父目录，但不能写入、删除子项、修改权限或归属。仅父目录上的 inherit-only 模板 ACE（例如 CREATOR_OWNER）不授予该父目录自身访问权；新建管理子项仍使用显式受保护 DACL。[Microsoft 安全描述符格式](https://learn.microsoft.com/en-us/windows/win32/secauthz/security-descriptor-string-format)

实现不自动修复已有权限，不接管其他用户的文件，不要求管理员授权。源码的这些行为并不能替代普通用户 token 下的原生运行验证；GitHub runner 若使用管理员 token，也不能据此宣称普通用户场景已经验收。

## 锁、读取和发布

库只认 `writer.lock` 和 `patterns.json` 两个固定名称。未知文件和中断遗留的临时文件保留并报错，不自动清空目录。枚举以每批至多四个名称进行，遇到首个未知项就停止，避免先读入无界目录列表。

每次操作取得短事务：

1. 打开并核验管理目录，句柄不共享删除权限。
2. 对持久 `writer.lock` 使用 `LockFileEx` 非阻塞独占锁。已有锁文件时先取锁再枚举，因此另一正常写入者的暂存窗口应返回 Busy。
3. 校验目录及锁的路径仍对应原句柄。管理文件必须是普通磁盘文件、非重解析点且只有一个硬链接。
4. 相对目录句柄读取当前快照，记录文件身份和完整字节；发布前再次核对。读取失败或超过预算不能产生可发布状态，发布也不能超过该次成功读取所设预算。

发布使用同目录随机临时文件，`NtCreateFile` 以“必须新建”方式返回句柄。完整写入并调用 `File.Sync` 请求 `FlushFileBuffers` 后，仍持有该临时文件句柄，通过 `NtSetInformationFile(FileRenameInformation)` 相对已打开目录替换固定快照；不先删除旧文件，不进行复制回退。[Microsoft 文件共享规则](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew)、[重命名结构及规则](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information)

如果发布前失败，只对原来仍持有的临时文件句柄设置删除标记，再关闭句柄；不先释放对象身份、再按路径猜测并删除。发布已经成功后，绝不把快照当临时文件清理。目标被其他不共享删除权限的句柄占用时，发布应失败并保留原快照。

底层 `published` 返回值区分“替换尚未发生”和“替换已发生但最终确认失败”。后者由公共 Store 报告 `ErrCommitUncertain`；调用者应重新查询当前库，不能声称旧库一定未变，也不能盲目回滚或换一个请求重试。

## 尚不能据此作出的结论

- 此处没有可移植的 Windows 目录 `fsync`，文件同步和同目录替换不构成掉电耐久证明。
- 锁协调遵守同一协议的进程；这不是对同一 SID 下恶意进程的隔离沙箱。
- 当前回执只证明 Go 1.18 的 Windows amd64／386／arm64 测试二进制跨编译，以及 Go 1.18／当前 Go 的 Windows 目标 vet 通过；本机是 macOS，没有执行这些 Windows 二进制。
- Windows 原生测试、其 token 状态和实际 NTFS 行为须以之后的 CI 结果单独验收。符号链接测试若因测试账号缺少创建权限而跳过，应明确保留该缺口。
- 这些文件事务证据不等于 API 集成、图案引用执行、USB、系统服务、登录重启或 mk3 硬件验收。
