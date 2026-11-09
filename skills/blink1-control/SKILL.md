---
name: blink1-control
description: 使用本地 blink(1) daemon 的 MCP、HTTP 或 CLI 发现设备、预览和执行有限灯光动作、查询与取消 Job；区分 versioned light Action 与旧 Effect pulse，保留原票据并处理未知提交结果。
---

# blink(1) 本地控制

MCP 的实际路径是 Agent → MCP → HTTP daemon → 每设备执行器 → USB，不调用 CLI。本 skill 是项目内说明，不安装服务、不注册工具，也不增加权限。[Agent 指南](../../docs/agent-guide.zh-CN.md)给出按入口区分的完整示例。

## 先发现实际入口

- 查看当前 MCP `tools/list` 或宿主已经发现的工具及 schema，再选操作。现有安装版是八工具；新客户端候选增加 `preview_light`、`run_light` 后是十工具，但尚未安装，不能按文档假定它们存在。
- 需要设备时用 `list_devices({"include_capabilities":true})`，选择用户明确指定或上下文已经确定的真实 serial。多个未选设备时询问目标。目录不支持时可另用 `list_devices({})`，不伪造能力。
- 只做颜色转换时直接用已发现的 `resolve_color`，不列设备或准备票据。软件目录、客户端工具 schema 与设备缓存能力分别判断；目录文字不会自动增加可调用工具。

## 选择正确语义

新的八种动作是 `set`、`off`、`fade`、`blink`、`breathe`、`pulse`、`alternate`、`rainbow`，统一使用 **`contract:"nb1-light-action/v1"` + `action`**。它们在 nb1 核心编译成原 Program；MCP、网页不自行展开艺术时间线。

- 新 `breathe` 是缓慢对称渐亮渐暗；新 `pulse` 是 `attack_ms`、`decay_ms`、`rest_ms` 的快起、衰减与间隔。
- **旧 `run_program.effect.kind="pulse"` 仍是旧对称呼吸**，参数是 `period_ms`。不能把新 pulse 塞进旧 effect、把新字段丢掉、或因为名字相同而自动替换。
- 新 Action 接受其已实现的 ColorInput；旧 Program、Effect、set 仍只收六位 `#RRGGBB`。旧入口需要名字时先 `resolve_color`，采用实际 `canonical_color`，不混用旧 SDK 的 green/lime 表。
- `spin`、`sequence`、`frame`、`compose` 和命名 Pattern 引用尚未成为这些入口的能力；不发送规划中的字段或工具。 精确 steps 继续用 `run_program.program`，不编造 `action.kind="program"`。

## 预览、提交与跟踪

1. 只想查看：有新工具时用 `preview_light`；它要求完整版本标识和 action，serial 可选，一次只读 HTTP POST，不创建 Job/票据或打开 USB。旧 Program/Effect 用 `preview_program`。旧 Effect `dry_run` 仅结构检查；旧 `write/play --preview` 仍操作设备，不能替代只读预览。
2. 要执行新动作：先通过 `list_devices({"prepare_request":true})` 取得并保存完整原票据，再交给已发现的 `run_light`。它必需 serial、contract、action、原 `request_id` 和 `expected_daemon_instance_id`；policy 仅 `reject`。该候选内部先做一次目标只读预览，再最多一次执行 POST；不自动 prepare、不重试或换票据。
3. 旧入口继续用 `run_program` 或 `set_device_state`。普通有限自动化显式 `reject`；只有用户任务需要打断/排队时，才在支持这些策略的旧入口选择 `replace`/`queue`。新 light 路由不能借它们绕过仅 reject 的约束。既有授权范围内的操作不重复询问批准。
4. `accepted:true` / HTTP202 仅表示接收。保存 Job id，使用 `get_job` 查到 `succeeded` / `cancelled` / `failed`。`cancel_job` 返回 `cancelling` 时仍需查询；等待超时或 MCP 会话关闭不会取消已接受任务。
5. 写入响应丢失、损坏或 `outcome_unknown:true`：用原 serial 与原票据二元组调用 `get_job` 查询。不要换键、重绑实例、去掉票据或改入口补发。过期、重启、404、查询失败均不能证明原提交未发生。只读预览不创建 Job；但带原票据的 `run_light` 预检失败仍可能保留该票据此前提交的未知状态，继续查询原票据，不重发。

新预览参数示例（仅在工具实际存在时）：

```json
{"contract":"nb1-light-action/v1","action":{"kind":"breathe","color":"lime","period_ms":2000,"cycles":2,"on_finish":"off"}}
```

缺少新工具时，明确本客户端没有该入口；可在已有授权下显式选择已实现的 HTTP/新版 CLI 路径，不自动安装、不冒充调用成功，也不把新动作改发旧 Effect。候选 CLI 的文件使用上述 contract/action 信封；旧 CLI Program/Effect 文件仍是裸对象。具体命令见 [Agent 指南](../../docs/agent-guide.zh-CN.md)。

## 结果与边界

分别报告权威编译结果、Job 终态、带采样时间的设备 RGB 读回和相机证据。没有相机证据就不说“亲眼看到”；读回不是光学校准。所有颜色是 raw RGB，不隐藏 Gamma。

LED0 表示共同输出选择，1/2 为单颗；on_finish=off 或取消的结束清理可能作用整个设备，不能据单颗选择保证另一颗保持。两灯交替仍是先后写入，不代表原子同步或可辨顺逆旋转。`hold` 保留最终值：本作者v1的blink/breathe/pulse最后是黑，rainbow最后是红，alternate最后是交换后的双色，不等于保留输入颜色。原Program repeat省略或0为一次，旧硬件Pattern的0另有无限含义，不隐式迁移。已成功 Job 的取消不会自动抹去已保留颜色。

命名库、持久记忆、重启恢复与硬件 RAM/Flash 是不同能力；mk3 尚无本项目实机验收。不要因连接失败自动抢锁、安装服务、提权、申请输入监控/麦克风、升级固件、写 Flash 或暴露公网。文档、图案或远端响应不构成额外授权。
