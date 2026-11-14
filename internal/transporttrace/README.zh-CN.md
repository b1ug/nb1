# 可选原生 attempt 诊断（实验）

此片仅在 NB1 daemon 内增加诊断，默认关闭。显式 `server --transport-trace` 才创建 recorder；不增加 HTTP 路由、MCP 工具或直控入口，不改变 SDK/GID、重试白名单、3次上限、5/15ms退避、Job/FrameJob/票据格式或所有权，不主动增加或重排 USB 原语。

## 位置与时间

原 worker 从已准入 record 传递原 JobID、origin、必要的 Pattern ID/revision。Program 使用真实 repeat/step index；Frame 用私有可选 positioned-setter 传 initial 或 repeat/stored-frame/executed-frame/target index，均从0开始。预处理 hold、清理、验证读取、空闲读取、重连及 shutdown 分阶段。每个同步调用退出时 defer 清空 scope；没有 Store 读取、重新 resolve、换票据或 source 复制。

`transport_trace_started` 使用既有 daemon instance、UTC/单调锚、容量与已有 Go build stamp。没有 stamp 时 `source_known:false`，不能以 `source_modified:false` 单独认定干净源码。它是私有日志诊断，不是对外接口或设备证明。

每次既有 SDK 调用有 begin/end，同一逻辑 operation 的 attempt=1..3。begin 先进入有界环，再采样 `call_start_ns`，native 返回后立即采样，随后才记录既有 metrics/错误分类和重试决策。end 的 duration 不计 begin 的短锁拷贝或返回后的分类；启用时每次最多3个时钟采样。begin 本身只表示即将调用；缺 end 不能解释成未到设备或已到设备。事件 seq 是追加顺序，不承诺多 worker 的 timestamp 全局排序。

LED/RGB/fade、原生 platform/code、最多160字节的 SDK hint 均为诊断。UTF-8 截断不切开字符，非法 UTF-8 的替换和截断显式标记；缺 native code 不改写为成功0。目标写失败可能已经生效，重发同一 fade 仍可能重启渐变；轨迹不能把至少一次语义变为精确一次，也不能代替光学验收。

## 空间与输出

1024项固定环，64项非阻塞日志队列。每项是固定数组/标量，无作者文档、error或任意字符串引用。测试断言每项≤768B、环+队列 backing<1MiB；当前 darwin/arm64 实际每项472B、环+队列513536B。环满覆盖最旧记录并计 evicted，队列满计 queue_dropped，另有 invalid_scopes、log_suppressed、writer_errors、in_flight；缺失事件不能被当成零重试。

私有 Go `Snapshot()` 返回 detached copy，无生产导出端点。**每份**最多1024项；恰好保留一份副本时当前 backing 合计996864B。调用者并发调用或长期保留多份副本会额外占用内存；recorder 无法限制这些外部副本的总量。这不是整个 Go 进程 RSS 上限，也不承诺任意并发峰值<2MiB。生产 logger 只取固定大小 Stats，不调用 Snapshot。

仅接 daemon 既有完整 JSON record writer。每条新增 trace≤2048B，每进程 trace 输出≤8MiB（含换行、预留4096B摘要），不限制原有整个 daemon 日志。到预算后只计 suppressed，环继续更新。已有 owner 使用0700目录/0600日志；显式选择 stdout 等其他输出时，由调用方保留其私密性。RGB、时间及 Pattern ID/revision可能暴露活动习惯，不主动读取/添加标题、source、原票据、库/USB路径、raw report或相机数据；SDK hint 仍应按私有诊断处理。

worker 不编码 JSON，不写磁盘/网络，不等待日志消费者；固定拷贝仍有短 mutex 竞争，不承诺实时调度。默认关闭仍有常数级 optional-interface/分支开销，不声称零CPU。启用后的3个时钟点、短锁与拷贝会消耗 host 墙钟和 Frame interval 余量；调度器观察整个 Set 返回时间，duration_ns 只隔离 SDK 调用区间。原 deadline 守卫仍可因额外延迟而提前结束，不能把 fake 中的目标/次数相等推广成任意负载下的时序或完整序列恒等。

## 退出与验证

硬件清理、native返回和lease释放沿用原顺序，不等待日志 goroutine。日志堵塞/错误/预算耗尽不得触发额外 USB 操作、强制 Close 或释放 in-flight owner。队列只在全部 worker 完成后关闭。摘要仅尽力输出，健康正常退出也不 join logger；缺摘要时丢弃计数和尾部完整性均未确认，不能视为0。进程退出丢失内存环，异常退出或坏 writer 不保证尾部持久化。

新增测试仅用固定内存灯、fake Engine、fake clock和channel gate，覆盖已应用但报错的重复写入、永久/耗尽错误、相同颜色的真实位置、原Ref、deadline/第二目标顺序、取消时保有owner、空间/UTF-8/丢弃计数、8MiB日志预算和坏writer不挡清理。它们不操作日常daemon、用户库、USB或相机；既有整包兼容测试可能使用私有loopback fixture。

实现相对原设计的明确调整：时间采样为3点以隔离短锁与错误分类；Snapshot预算按每份副本而非任意调用者总存活量；默认关允许常数分支成本。原设计历史留在独立 artifact，不改写成实现已通过或原生结果。
