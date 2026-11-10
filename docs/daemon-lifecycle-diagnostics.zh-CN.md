# daemon停机诊断：触发分支与清理结果

这是新增的本地日志合同 `nb1-daemon-lifecycle/experimental-v1`，不是HTTP/MCP端点。只有正常进入运行阶段的daemon会尽力输出两条记录，不新增USB调用，不改变设备所有权、重试、队列或退出错误返回。

现有 `device_connection` 的 `error` 来自设备状态 `LastError`。例如 `closing` 带有 `privilege violation`，只说明停机时设备状态保留了该错误，**不能说明该错误触发了daemon退出**。

## 两条记录

第一条 `daemon_shutdown_trigger` 记录主控制循环实际观察到的分支：

| trigger | 含义 |
| --- | --- |
| `parent_context_done` | 传入的context已结束；可能是调用方取消、截止时间，或CLI通过NotifyContext收到中断/终止。 |
| `engine_stopped` | Engine.Done已关闭；记录当时parent context状态，避免把并发取消误称独立故障。 |
| `http_server_returned` | HTTP Serve返回，包括外部关闭listener、服务错误或正常ErrServerClosed。 |
| `startup_log_failed` | 首条启动公告写入返回错误；同一个失败的日志sink通常无法再输出这一条。 |

`trigger` 是选中的分支，不是唯一根因。多个channel可能同时就绪；例如context与engine同时结束时，可能观察到任一分支。当前 `signal.NotifyContext` 不保留具体signal，本片不猜SIGINT、SIGTERM、操作系统授权事件或外部终止来源。

两条记录都含 `event`、`contract`、UTC `time`、`daemon_instance_id`、`trigger`、`parent_context`、`trigger_error_class`。context和错误分类只有固定枚举：`none`、`context_canceled`、`deadline_exceeded`、`http_server_closed`、`other`。只识别精确标准sentinel；包装错误和平台错误归 `other`，不执行任意错误的Error/Is/Unwrap方法，也不输出错误正文、环境变量、文件路径、请求或原生码推断。

第二条 `daemon_shutdown_result` 另外含：

| 字段 | 含义 |
| --- | --- |
| `http_shutdown` | HTTP Shutdown的独立返回分类；失败后仍沿用原HTTP Close与设备清理路径。 |
| `engine_close` | Engine.Close的独立返回分类；与HTTP排空共用原来的stop context预算。 |
| `engine_done` | 写结果时是否已经观察到Engine.Done；false表示不能宣布所有worker/原生清理完成。 |
| `elapsed_ms` | 从观察到退出分支到清理等待返回的主机单调时钟耗时。 |
| `outcome` | 原有函数返回值为nil时为`succeeded`，否则`failed`；不是光学熄灯或全局硬件成功的证明。 |

`engine_close: deadline_exceeded` 只是停止等待，不能取消同步原生I/O或提前释放设备句柄/lease。`engine_done:true`也可能伴随已完成但失败的设备清理；还须检查`engine_close`。同理，HTTP排空失败不能被随后成功的Engine.Close掩盖。

## 输出与资源边界

主控制路径只入队两条固定类型记录，channel容量为2，由已有的单一JSON writer输出。即使writer在启动、trigger或result写入时阻塞，这两次入队也不依赖消费者；设备清理和函数返回不等待logger。没有新goroutine逐条写日志，也不另开同步stderr补写。

同一writer确保完整JSON记录不会互相交错；两条生命周期记录顺序为trigger后result，但连接/trace事件可能穿插。结果入队后关闭生命周期channel，logger不会仅因设备事件先关闭就漏读结果；不过函数不会等待输出落盘。writer失败、阻塞、进程退出过快、SIGKILL、崩溃和断电都可能缺记录，缺记录不能解释为正常停机、零错误或零日志丢失。

Engine.Start之前失败维持原返回路径，本片不宣称覆盖；旧连接日志的error正文和trace日志预算保持原行为。本合同的固定枚举限制只适用于这两条新记录，不扩展成所有历史日志均已脱敏或有全局字节上限的保证。

## 验证范围

软件验证使用内存backend、私有loopback listener及受控日志writer：取消、独立Engine关闭、listener失败、启动sink失败、三个写入阶段阻塞、result写入失败、HTTP未完成body导致排空超时、原生模拟清理超时但仍持有backend，以及清理错误与触发原因分别报告。新增错误分类还验证不调用自定义错误方法。

本片没有操作实际灯、摄像头、系统服务或全局配置。它增加下一次故障的可观察信息，不能追溯证明先前96a退出的原因，也不是长期运行、其他操作系统USB或MK3验收。
