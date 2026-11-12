# 使用 blink1-control Skill

[English](agent-guide.md)

[Skill](../skills/blink1-control/SKILL.md)告诉 Agent 如何使用已有的 nb1 CLI、HTTP API 或 MCP 连接。它不自带驱动，不自动启动 daemon、注册 MCP 或增加权限。Skill 应与 nb1 版本匹配；实际帮助和 MCP schema 才能说明当前可用的接口。

[同版候选指南](candidate-install.zh-CN.md#独立接入-mcp-或-skill)说明独立 MCP 与 Skill 产物的核验和解压方式。

## 安装到一个项目

从对应版本的源码复制完整的 `skills/blink1-control` 文件夹，放到项目的 `.agents/skills/` 下，保留 `references/` 和 `agents/`。目标已有同名 Skill 时，先核对内容再决定是否替换。

在该项目打开新的 Codex 会话，用 `/skills` 检查是否发现它。可以显式写 `$blink1-control`，也可以直接描述 blink(1)/nb1 任务。Codex 先用名称和描述选择 Skill，选中后才读主文；随包 metadata 允许自动触发。详见 [OpenAI 官方文档](https://learn.chatgpt.com/docs/build-skills)。

例如：

```text
$blink1-control 预览一个 lime 色的 pulse，两次，不要播放。
用 nb1 让我的 blink(1) 青色呼吸两次，结束后熄灭。
把这个效果保存成本机 Pattern，不要播放。
```

网页 CSS 动画、网页彩虹图标及无关 USB 设备不属于这个 Skill。

## 提供实际连接

daemon 模式需要显式启动 nb1，或使用已配置的用户服务：

```sh
nb1 server --devices DEVICE_SERIAL
```

HTTP CLI 可以用 `--endpoint` 或 profile 的 `server.listen` 选择地址；[MCP 适配器](../adapters/mcp/README.md)使用自己的 `--endpoint`，不读取 nb1 YAML。应说明目标地址和设备，尤其是本机有多套安装时。连接失败时由用户选择启动或显式直连，Skill 不授权自动启动或静默切换 USB。前台 direct 有独立生命周期。

MCP 宿主可能另行要求保存或播放工具的审批。被拦截不等于保存或执行成功；只授权所需服务和工具，不应为了加载 Skill 放宽 shell 沙盒。配置说明见 [Codex MCP 工具策略](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。

Agent 应先核对工具或 `nb1 control --help`，预览表达式，再执行用户请求的任务。HTTP202 只代表接受，Job 终态才代表执行结束。丢失回执时保留原地址、输入和 Job ID；记录不存在不等于从未播放。

需要选择回合结束灯光时，见 [Codex notify 指南](../examples/command-notify/CODEX.zh-CN.md)：它区分外部 notify JSON 参数与 stdin Stop hook，默认预览，并提供无需全局安装的单次调用配置。蓝色表示回合结束，播放结果应检查 Job 报告。

## 按需读文档

主文保留表达式选择、预览／执行／跟踪和恢复原则。以下引用随 Skill 一起复制，单独安装后也能读到：

| 需要了解 | 文档 |
| --- | --- |
| CLI JSON 与 MCP 参数、必填／默认、颜色形式 | [表达式与字段](../skills/blink1-control/references/expressions.md) |
| 命名颜色、本机 Pattern | [本机作品库](../skills/blink1-control/references/patterns.md) |
| 显式 direct、配置、设备存储边界 | [直连与配置](../skills/blink1-control/references/direct-and-config.md) |
| 完整限制、HTTP 错误和示例 | [API v1](api-v1.md)与 [OpenAPI](api-v1.openapi.json) |

本机 Pattern 与设备 RAM、Flash、开机效果分开处理。预览、Job 终态、带时间的 RGB 读回和相机观察也是不同证据，不能互相替代。平台边界见[验收文档](daemon-validation.zh-CN.md)。

## 验证 Agent 能否使用

正例与不应触发的请求都要测。先用软件设备观察实际请求、播放次数与 Job 状态，不只看最终文字。可以覆盖只预览／保存、单次有限播放、丢失回执恢复，以及 daemon 不可达时不自动切换。

Codex 可以显式指定 Luna：

```sh
codex exec --model gpt-6-luna
```

按可用配置选择推理等级，再用自己的连接和任务验证。模型选择见 [OpenAI 官方文档](https://learn.chatgpt.com/docs/models?surface=cli)。有限测试通过不代表所有模型、硬件和异常场景都已验证。
