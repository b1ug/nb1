# 颜色输入 v1：归一化契约

当前颜色合同已实现并纳入本地套件；完整API与实际版本边界见[统一手册](api-reference.zh-CN.md)。此接口只计算颜色，不发现/打开设备，不创建 Job、回执或设备锁。v1 Program、Effect、set 继续只接受六位 `#RRGGBB`；旧 CLI/SDK 的色表和 Gamma 行为不变。

## 新入口

`POST /v1/colors/resolve`，`Content-Type: application/json`，无 query，严格信封只有必填字段 `color`。成功 HTTP200；响应固定：

```json
{"schema_version":1,"color_contract":"nb1-color-input/v1","input_kind":"name","canonical_color":"#008000","rgb8":{"r":0,"g":128,"b":0},"palette":"css-named-2026"}
```

input_kind 为 hex6 / hex3 / name / rgb8 / hsv。palette 仅 name 出现，固定 css-named-2026；其他种类必须省略。响应不包含 serial、Job、accepted 或物理支持声明。结构版本、颜色语义和 HTTP 路由分别管理；未来不向封闭 v1 响应随意加字段。

| color 输入 | 结果 | 规则 |
| --- | --- | --- |
| `"#12AbEF"` | `#12abef` | 大小写 hex6 |
| `"#0f8"` | `#00ff88` | 每个 hex3 数字重复 |
| `"green"` / `"lime"` | `#008000` / `#00ff00` | 固定 W3C CSS Color4 2026-11-03 名字表；含不透明名字/同义名，不含透明/系统/上下文色 |
| `{"space":"rgb8","r":0,"g":255,"b":136}` | `#00ff88` | 必填 space/r/g/b；通道0–255普通十进制JSON整数 |
| `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}` | `#008000` | 必填四字段；h0–360°，360等于0；s/v0–100% |

字符串限64个解码后字节，外层只去ASCII空白（空格、tab、CR、LF、VT、FF），名字仅ASCII大小写不敏感。对象字段与 space 值大小写精确。JSON Unicode转义可用于合法ASCII内容，转义后的重复字段仍拒绝。

HSV接受普通非负JSON整数或最多6位小数；不接受指数、负零、NaN/Infinity或字符串数字。先用精确十进制有理数计算标准HSV扇区转换，最后每通道量化 `floor(255×channel+0.5)`，只量化一次，不使用平台浮点近似来决定半值。RGB8不接受1.0/1e0。范围不夹取，超过限制直接拒绝。

整个请求最多4096字节。拒绝未知/重复/大小写别名字段、null、数组、尾随JSON、缺字段及不合形状输入。此子集不支持 rgb() 字符串、HSL、alpha、transparent、currentColor、CSS变量、中文别名、用户palette和random。无隐式Gamma或亮度校正；CSS名字数值只是输入约定，不承诺未校准LED的物理sRGB或视觉等价。

错误：方法405；query400 invalid_request；媒体类型415 json_required；超过4096字节413 body_too_large；颜色/信封语法与语义错误400 invalid_color。保留现有Host/Origin边界。旧daemon 404/501返回不支持，不回退USB。

## CLI 与 MCP

CLI 新命令：`control color INPUT`（名字/hex），或 `control color --file FILE`（文件为裸JSON ColorInput，不是HTTP信封）。二选一。默认由daemon归一化，显式 `--mode direct` 使用同一个纯函数；两者不访问USB/锁。拒绝policy/请求票据，不接受serial，不借旧颜色parser自动回退。

MCP 新工具 `resolve_color`，参数就是HTTP信封 `{"color":...}`；新增颜色工具的早期阶段曾为八个工具；当前12工具及实际schema见统一手册。schema明确string / rgb8 / hsv三个分支，工具只访问新HTTP路由，接受后仍未执行灯光。结果外层kind为 color_resolution、accepted=false，data为上述响应。现有七工具的输入/输出形状不变；目录不增加新字段。使用归一化的 canonical_color 构造现有Effect，再preview/run。

MCP不复制名字表或HSV算法，不导入USB/core包；校验返回结构、已知contract、规范hex与RGB8自洽、input_kind和palette规则。远端归一化是权威，不把自洽响应声称为来源认证。预览/归一化POST失败不产生未知Job结果，不自动重试或直控。

迁移旧配置先确认要保留输入值、实际发送RGB还是视觉效果，再核实旧色表/Gamma/亮度；尤其旧SDKgreen/lime与CSS相反。转换一次、展示精确hex，不能重复补偿。

名字表依据：[W3C CSS Color4 CR Draft，2026-11-03](https://www.w3.org/TR/2026/CRD-css-color-4-20261103/#named-colors)。实现保存固定表和来源摘要，后续上游改动不改变此contract的值。
