# 实验高层 Orbit 编译器

`nb1-light-action/experimental-v2` 只接受 `kind: "orbit"`，生成明确的 `nb1-frame-program/experimental-v1`。这是纯编译器；结果不说明设备在线、执行器已启用或实机观感已验收。调用方仍须检查设备和执行能力。

```json
{
  "contract": "nb1-light-action/experimental-v2",
  "action": {
    "kind": "orbit",
    "colors": ["red", {"space":"hsv","h_deg":240,"s_pct":100,"v_pct":100}],
    "leds": [1, 2],
    "period_ms": 3000,
    "cycles": 3,
    "segments": 4,
    "on_finish": "off"
  }
}
```

省略值时：颜色红/蓝、LED 顺序 `[1,2]`、周期 3000ms、播放 1 轮、4 段、结束熄灭。所有默认值在返回的 `action` 中展开；显式 `null` 不代表省略。

- `colors` 恰好两个值，沿用颜色 v1 的名称、hex3/hex6、RGB8、HSV 输入；编译后为原始 RGB8，不加软件 gamma。
- `leds` 可为 `[1,2]` 或 `[2,1]`。这表示逻辑方向；两条 USB 指令仍依次发送。
- `segments` 仅为 4 或 8：4 段是简单长线性渐变候选，8 段保留更多目标轨迹。两者采用固定采样，不在运行时计算浮点曲线。
- 每段间隔为 50–600000ms，全部间隔恰好相加为 `period_ms`。硬件 fade 向上量化为 10ms 整数倍，覆盖该段间隔。
- `cycles` 上限为 `floor(1024 / segments)`，整体名义时长还必须满足帧程序的 24 小时上限。初始化只做一次；返回的名义时长含初始化和末段等待，未包含 USB 和操作系统延迟。
- `on_finish` 为 `off` 或 `hold`。真正的任务成功还要求执行器完成最后渐变、清理与读回。

返回的 `recipe_id` 为 `orbit-paired-linear-4/experimental-v1` 或 `orbit-paired-linear-8/experimental-v1`，与冻结的 FrameProgram 一起记录。旧 `nb1-light-action/v1` 的 `alternate.transition="orbit"` 以及已存旧 Program 不会被重新解释。

`testdata/recorded-paired4.json` 与 `recorded-paired8.json` 是 2026-11-10 本地 MK2/FW204、原生 30fps、零重试实验的纯参数快照，来自 `artifacts/orbit-visual-lab/paired{4,8}-gap10-take1/spec.json`。测试逐个比对初始化颜色、每段目标、fade 与间隔；不包含摄像头图像或个人信息。该夹具一致性不代替产品 daemon 的新一轮实机验收。
