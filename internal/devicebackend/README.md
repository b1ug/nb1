# mk2 运行时设备适配

本适配器只覆盖已验证的 mk2 基础 profile：`c` 精确目标色、关闭 tickle 的 `D`、停止 pattern 的 `p`，以及 `r` / `S` / `v` 查询。不提供 pattern/Flash/固件写入，也没有把重试安装到通用 GID、旧 SDK 或旧 CLI 命令中。

## 有界重试

1000任务真机试运行曾在第326项后遇到原生 `general error`。这证明存在需要处理的错误，尚不足以确定 USB/固件/主机的具体根因。本策略是本项目的受限恢复选择，不能称为官方时序保证。

每个受支持 SDK 调用最多执行3次：第一次失败后等待5ms，第二次失败后等待15ms。只接受当前 GID Darwin 已知的完整原生叶错误文本 `general error`、`device is busy`、`I/O Timeout`；没有字符串包含匹配。关闭、断连、离线、不支持、非法输入、短响应及逻辑验证失败不会因此被重试。这些候选错误也可能是永久故障，最终失败会保留原生 cause、操作阶段和尝试次数。

- 查询重试重新调用完整的 SDK 查询事务（发命令再读响应），不会只重复读取旧响应。
- 重复设色总是使用相同的 LED、RGB 和 fade 时长。失败的写入可能已经到达设备，因此是“至少一次目标设置”，不是精确一次；重发可能重新开始硬件渐变，执行器从最后成功返回后再等待 fade/hold。
- hold 清理只采集一次目标，再分别重试每个固定颜色写入；不会因失败重跑整段清理并重新采样不同颜色。
- 每个原生调用仍在同一个设备 worker 内同步执行。最多20ms额外 backoff 不等于原生 I/O 或 Close 有截止时间；异常阻塞仍须另行处理。
- 成功清理仍需要 pattern 状态和 RGB 读回，重试不会跳过验证。输出不匹配的250ms等待继续独立存在。

## 可观察性

`driver.Metrics() []control.TransportMetric` 提供按 serial 与固定操作类型汇总的 calls / attempts / retries / failures / last_error；不会为每一种颜色创建新指标。`LastError` 是历史观测，可能已经恢复；终结失败计入 `Failures`。快照并发安全、不会泄露内部可变引用，并在同一 serial 重开句柄后累计保留。

`SnapshotMetrics(driver)` 另提供每设备累计 recovered/exhausted 次数及最近重试阶段，用于本地验证。没有隐式 stderr 日志。原生错误目前缺乏稳定类型，待 GID 提供原生错误码后，应替换窄范围文本识别。

## 官方资料核对

本地归档 `blink1ref/c-blink1-tool/blink1-lib-lowlevel-hidapi.h` 与当前[官方 low-level helper](https://github.com/todbot/blink1-tool/blob/main/blink1-lib-lowlevel-hidapi.h)直接调用 feature API，没有提供可照搬的 mk2 通用重试时序。[官方库](https://github.com/todbot/blink1-tool/blob/main/blink1-lib.c)中有 EEPROM/旧读色/诊断等特定流程的50ms等待及序列号 EEPROM 写入的局部重试，不应用作所有 mk2 请求的固定延迟或通用策略。mk2固件源码中 LED 每10ms更新，这是 LED 渐变时序，不等于 USB 故障恢复契约。

fake lamp 测试覆盖瞬态恢复、连续三次失败、永久错误不重试、完整查询重发、hold 快照保留、单阶段重试和并发指标快照；它们不替代真机长跑及真实拔插。
