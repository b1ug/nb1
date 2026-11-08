# 连接状态事件

`Engine.Events()` 返回单消费者、容量64的内存事件流。只在 connection 或 USB path 改变时发出；初次 Start 记录 connecting，RGB采样、任务进度和已在设备适配器内恢复的重试不产生连接事件。重复的离线错误也不会按轮询频率重复记录。

事件包含全引擎递增 sequence、UTC时间、serial、connection、path和当前 LastError。序号在丢弃事件时也递增，可用于识别缺口。`DroppedEvents()` 是缓冲已满时丢弃的累计数量；不会计入下游日志文件写失败。事件流不是广播，也不是可靠审计日志；当前状态仍应通过 Device/Devices 查询。

所有事件发布和状态快照在 Engine mutex 下完成，但发送使用非阻塞 select。慢消费者不能让 native worker 等待。正常关闭等所有 worker 退出后才关闭事件通道；Close-before-Start 同样关闭通道，不会留下等待者或产生 send-after-close。

daemon 先输出 daemon_listening，再通过同一 mutex JSON writer 输出 device_connection 事件和累计 dropped_events。没有逐次颜色/任务请求载荷、摄像头内容或 SSE/HTTP订阅接口。日志是 best-effort：事件队列可能溢出，事件日志写失败会停止该消费者；后者不把健康设备误报为离线，也不停止执行器。

stdout若永久阻塞，只会阻塞一个日志 goroutine；硬件清理不等待日志 goroutine结束。初始公告写失败仍保留既有启动失败行为。不要通过“未看到日志”断言没有发生连接变化，也不要把日志中的 closed 当作独立的光学熄灯证明。
