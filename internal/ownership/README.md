# 本地设备所有权

`Acquire(dir, serial)` 用操作系统锁协调 daemon 与 direct CLI。先把设备别名/枚举编号解析为真实 serial，再申请租约；同一 serial 的第二个持有者立即得到可由 `errors.Is(err, ownership.ErrBusy)` 判断的错误。不同设备独立申请。

```go
lease, err := ownership.Acquire("", serial) // 空目录使用当前用户默认目录
if err != nil {
    return err
}
defer lease.Close()
device, err := openDevice(serial)
if err != nil {
    return err
}
defer device.Close() // 后注册，先关闭设备，再释放所有权
```

- 默认目录为 `os.UserConfigDir()/nb1/device-locks`，使用持久应用目录，避免缓存清理重建锁文件。Unix 创建目录0700、文件0600，拒绝共享权限的现有目录、最终目录符号链接和锁文件符号链接。Windows 继承用户应用目录的 ACL；Unix 权限数字不等于 Windows ACL。锁文件必须是普通文件，FIFO 等特殊文件会被拒绝。
- 文件名为 serial 的 SHA-256，不把任意设备字符串当路径，也不写 PID、时间或用户信息。
- macOS/Linux 使用非阻塞 `flock`；Windows 使用 `LockFileEx` 的排他、立即失败标志。锁由打开的文件描述符持有，进程退出后由操作系统释放。没有依赖 PID 检测或超时清理。
- `Lease.Close` 可重复或并发调用。必须先停止原生 I/O 并关闭 HID，随后才释放 lease。原生调用尚未结束时提前释放会允许新控制器进入。
- **不要删除锁文件**，即使当前没有持有者。文件持久存在不表示设备忙；删除后重新创建会形成两个不同的锁对象。
- 这是同一用户、相同目录、遵守约定的控制器之间的协调。第三方官方工具不会自动遵守，仍依靠原生 HID 独占/权限。不同用户或 Windows service 账号的统一所有权需要额外设计；不要把可写的公共目录当作已验证替代品。
- 目录应在本机文件系统；网络文件系统的锁语义不在当前支持范围。

测试不访问 USB。当前 Mac 已运行跨进程竞争、重复/并发 Close、持有进程不调用 Close 直接退出后的再申请，以及文件名/目录权限检查。Windows/Linux 交叉编译仅证明这些平台源码可编译，原生运行仍需各自验证。

依据：[Windows LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex)、[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html)。
