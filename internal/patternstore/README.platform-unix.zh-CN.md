# Darwin / Linux 文件事务边界

公共 Store 每次操作打开一个短事务，不长期缓存库文件或持有锁。目录中只接受 `writer.lock` 和 `patterns.json`；事务自身暂时增加一个不可预测的 `.pending-*` 文件。锁文件保留在原位，不能在释放锁后删除，以免其他进程锁住不同 inode。

最终库目录要求当前有效 UID 所有、实际目录、0700；文件要求当前 UID 所有、普通文件、0600、单硬链接。已存在父路径的别名先规范化（兼容 macOS 的 `/var`），最终库目录本身拒绝链接。随后以打开的目录 fd 锚定 `openat` / `renameat`；文件先以 `fstatat(AT_SYMLINK_NOFOLLOW)` 检查类型，再以 `O_NOFOLLOW` 打开并核对 inode。FIFO、目录、设备、链接和被替换的锁/快照拒绝。现有权限不自动修复。

跨进程通过非阻塞 `flock` 串行化合作调用。写入者已经持锁时，其他调用先得到 Busy，不因看见合法的临时文件误报损坏。未知项或崩溃遗留的临时文件保留并报错，等待明确检查；不自动清空、迁移、覆盖或执行。

读操作保存已读快照的内容与身份；发布前再次检查目录、锁、快照内容和身份。完整新数据写入同目录私有临时文件并 `Sync` 后，使用 `renameat` 一次替换快照。临时文件的 descriptor 保持到清理结束，避免已释放 inode 被复用后误删新文件；清理只针对本事务绑定的 inode。

`publish` 明确返回 `(published bool, error)`：

- `false` 加错误：本次尚未确认成功 rename，旧快照没有被本次发布替换；目录/锁初始化可能已完成。
- `true` 加 `nil`：完整新快照已发布，文件同步、目录同步与发布后的绑定检查完成。
- `true` 加错误：rename 已成功；目录同步、发布后复核或关闭暂存句柄失败，不能报告“旧库未变”。上层应重新读取固定记录确认结果。

Darwin 的扩展 ACL 可能在 0700/0600 之外授予访问，因此额外通过已打开 fd 读取 `ATTR_CMN_EXTENDED_SECURITY`，保守拒绝任何扩展 ACL（包括显式空 ACL）。只有明确的无 ACL 布局或 `KAUTH_FILESEC_NOACL`、且已确认本地卷支持 ACL 检查和 flock 时才接受；未知、不支持、截断、异常引用均拒绝。新建目录继承的 ACL 也拒绝，不自动移除 ACL。产品不调用 chmod、shell 或 Cgo。Linux POSIX ACL 的有效 named-user/group 权限受反映在 group mode 位中的 mask 限制，因此精确 0700/0600 已阻断额外有效授权。

这里不形成恶意同 UID 进程的隔离边界。目标是本机文件系统上的合作进程，不据此承诺网络盘/云同步目录的跨客户端语义、断电耐久或硬件 Flash 持久化。公开 Store 层另外验证规范快照、摘要和整包限额；底层事务不编译效果、不创建 Job、不接触 USB。Darwin ABI依据 Apple [getattrlist(2)](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/getattrlist.2.html) 与本机 SDK 的 `sys/attr.h`、`sys/kauth.h`；固定 8192 字节缓冲区及返回长度、mask、offset、payload 长度均受检查。

Darwin 临时目录的测试覆盖实际跨进程锁、拒绝链接/非普通文件/宽权限、替换检测、提交前后故障与外来文件保留。Linux 的交叉编译只证明可编译；原生执行须查看对应 CI 回执。Windows 使用独立文件实现及独立验证，不由本文件推断支持。
