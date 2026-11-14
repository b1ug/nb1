# light 动作编译契约 v1

`nb1-light-action/v1` 把基础操作与艺术动作放在同一个作者入口。`CompileJSON` 接受裸 Action；`CompileRequest` 接受以下完整请求。二者只做纯编译，不访问设备、不创建 Job。

```json
{"contract":"nb1-light-action/v1","action":{"kind":"breathe","color":"lime","period_ms":2000,"cycles":2}}
```

结果中的 `contract` 是作者语义版本，`program_compile_contract` 是保持原义的 `nb1-program-effect/v1`。结果同时包含规范 Action、可直接提交给既有执行器的 Program、`nominal_duration_ms`、`total_steps`、所需能力名。HTTP/CLI 可以使用同一编译入口，再沿用现有设备、Job、票据和取消规则。

## 动作与参数

所有动作都接受 `on_finish`，只能是 `off` 或 `hold`。基础动作默认 `hold`；艺术动作默认 `off`。除 `alternate` 外，`led` 默认 0，可为 0、1、2。0 使用固件的共同输出选择，不启动两个相互抢设备的任务。

| kind | 适用参数与默认 | 确定的名义时间 |
| --- | --- | --- |
| set | `color` 必填 | 10ms；默认完成后保留颜色 |
| off | 不接受 color | 10ms；默认把所选输出设黑并保留 |
| fade | `color`、`duration_ms` 必填；10–600000ms，10ms量化 | duration_ms + 10ms |
| blink | `color` 必填；`on_ms:250`、`off_ms:250`，各10–3600000；`cycles:1` | (on_ms + off_ms) × cycles |
| breathe | `color` 必填；`period_ms:2000`，330–1200000；`cycles:1` | period_ms × cycles |
| pulse | `color` 必填；`attack_ms:60`、`decay_ms:240`，各10–600000且10ms量化；`rest_ms:700`，0–3600000；`cycles:1` | (attack_ms + decay_ms + rest_ms + 30ms) × cycles |
| alternate | `colors:["red","blue"]`，恰好两个颜色；`leds:[1,2]`，也可 `[2,1]`；`period_ms:2000`；`cycles:1`；可选 `transition:cut/linear/sine/orbit` | period_ms × cycles；省略/cut最小40ms，其余最小1300ms，最大1200000ms |
| rainbow | 不接受 color/colors；`period_ms:3000`，130–1200000；`cycles:1` | period_ms × cycles |

`cycles` 必须为1–10000，且**本作者契约额外限制执行总步数不超过1024**，因此不同动作实际可接受的最大 cycles 不同。编译后还受共同 Program 的24小时总预算、每步 fade/hold 上限限制。这里没有修改旧 Program 的 repeat/steps 限制含义。

参数名区分大小写；未知、不适用、重复（包括转义重影）、null、负零、小数或指数形式的时间/次数均拒绝。整数不隐式取整。ColorInput 完整复用已有版本：Hex3/Hex6、148个冻结CSS颜色名、rgb8 对象及 hsv 对象；HSV 允许其既有规则下的小数，不被时间字段的整数约束误伤。整份输入最多65536字节、JSON最大嵌套32层，每个颜色另外受既有4096字节上限约束。

## 波形与10ms开销

现有执行器每一步等待 `fade_ms + hold_ms + 10ms`。编译器不忽略这10ms。

- **blink** 两步直接亮、直接黑，各自 hold 为 on/off 名义阶段减10ms，边沿没有淡变。
- **breathe** 先设黑（10ms），再执行16个渐变目标。余下 `period_ms - 10` 按16份累计整数比例划分；每份减10ms后向下量化为10ms fade，剩余0–9ms放入hold，所以总周期精确。
- **pulse** 先设黑，再以 attack_ms 渐亮，最后以 decay_ms 渐黑并保持 rest_ms；三个步骤的30ms开销单独体现在名义周期里。默认60ms攻击、240ms衰减、700ms停顿，与对称呼吸有明显区别。
- **alternate，省略 transition 或 cut** 先依次写两个LED成为 A/B，再依次写成 B/A；每相位两次10ms开销从 hold 中扣除。两颗灯的USB写入仍有先后，**不是同时渐变，也不表示可辨的顺逆旋转**。
- **rainbow** 固定红→黄→绿→青→蓝→品红→红。初始红10ms，剩余时间按六段分配并使用与breathe相同的量化/补余办法；这些是在饱和RGB色相六条边上的线性淡变。

这些是原始RGB值，不暗加Gamma、亮度校准、相机曝光补偿。名义时间不包含USB调用、调度、准入/结束清理的额外耗时，不承诺硬实时。

呼吸的冻结强度表（万分比）是：

```text
0, 381, 1464, 3087, 5000, 6913, 8536, 9619, 10000,
9619, 8536, 6913, 5000, 3087, 1464, 381, 0
```

每通道按 `(channel × weight + 5000) / 10000` 整数除法取整。白色2000ms一周期的17个RGB灰度目标为 `0,10,37,79,128,176,218,245,255,245,218,176,128,79,37,10,0`；16个渐变均110ms，hold依次 `4,4,5,4,4,5,4,5,4,4,5,4,4,5,4,5` ms，加初始10ms正好2000ms。

## alternate 的环绕过渡实验

此本地实验只扩展 `alternate` 的可选 transition 字段。省略时仍输出原4步，规范 Action 中也不插入新字段；显式 `cut` 保留字段，但使用相同4步 Program。其他动作拒绝 transition。

```json
{"contract":"nb1-light-action/v1","action":{"kind":"alternate","transition":"orbit","colors":["red","blue"],"period_ms":3000,"cycles":2}}
```

`linear`、`sine`、`orbit` 都先设置起始两色，再走32组目标，每组依次更新两个LED，总计66步/周期。phase取0到32，theta=2π×phase/32。linear 的混色权重为三角形0→1→0；sine 与 orbit 的权重为 `(1-cos(theta))/2`。两灯目标分别是 `mix(A,B,w)` 和 `mix(B,A,w)`。

orbit 在混色上叠加亮度：第一灯为 `0.6+0.4*sin(theta)`，第二灯为 `0.6-0.4*sin(theta)`。亮度在0.2–1之间移动，相对颜色混合形成四分之一周期的偏移。它营造两点间的环绕感，**不代表两点硬件能真实分辨顺时针或逆时针**。

固定权重表（万分比）为：

```text
0,96,381,843,1464,2222,3087,4025,5000,5975,6913,7778,8536,9157,9619,9904,
10000,9904,9619,9157,8536,7778,6913,5975,5000,4025,3087,2222,1464,843,381,96,0
```

固定sin表（万分比）为：

```text
0,1951,3827,5556,7071,8315,9239,9808,10000,9808,9239,8315,7071,5556,3827,1951,
0,-1951,-3827,-5556,-7071,-8315,-9239,-9808,-10000,-9808,-9239,-8315,-7071,-5556,-3827,-1951,0
```

运行时只使用整数。设固定权重W与sin样本S，orbit亮度分子为 `30000±2*S`、分母50000。每通道先计算混合分子 `A*(10000-W)+B*W`，再乘亮度分子，最后统一加250000000并除500000000，**只做一次最终四舍五入**。中间值使用int64；不添加Gamma、光学校准或浮点计算。

红/蓝输入的关键目标如下（每列依次为第一灯、第二灯）：

| phase | linear / sine | orbit |
| --- | --- | --- |
| 0 | `#ff0000 / #0000ff` | `#990000 / #000099` |
| 8（四分之一） | `#800080 / #800080` | `#800080 / #1a001a` |
| 16（一半） | `#0000ff / #ff0000` | `#000099 / #990000` |
| 24（四分之三） | `#800080 / #800080` | `#1a001a / #800080` |
| 32（结束） | 回到phase0 | 回到phase0 |

两灯的每组目标**不是原子同时下发**。既有执行器会等待第一步 fade+hold+10ms，再执行第二步。因此每周期先预留起始两步20ms，将 `period_ms-20` 按累计整数比例精确分成64个顺序slot；每slot使用 `fade=floor((slot-10)/10)*10`，余数留作hold。最小周期1300ms使每slot至少20ms、fade至少10ms。真实USB与调度耗时仍额外存在。

phase32的颜色与起始相同，重复周期不会在目标颜色上跳变；每个周期仍包含那两次起始写入。最多15周期（990步），16周期由既有作者执行总1024步守卫拒绝。LED顺序可交换，但没有新增方向字段或并行设备控制。

## 兼容性与当前边界

旧 Effect v1 的 `kind:pulse` 仍是旧对称波形（1000ms示例为480ms渐亮、490ms渐暗及三个10ms开销）。本包的新 `pulse` 只能通过明确的新作者契约调用，不重解释旧效果、保存的Program或快照。

本作者v1明确绑定 `nb1-program-effect/v1`、`nb1-color-input/v1` 及10ms步进/淡变量化。若这些依赖语义版本或时序常量改变，编译入口拒绝运行；必须显式保留旧验证器或定义新的作者契约，不能沿用同一版本标识却悄悄换输出。

`hold` 保留最终输出：blink/breathe/pulse最后是黑色；rainbow最后是红色；alternate省略/cut最后是交换后的双色，linear/sine/orbit最后回到对应起始目标。它不表示“保留输入color”。`off` 沿用执行器既有结束清理，会关闭设备输出。`sequence`、`spin`、多设备同步和新的多输出 frame 尚未实现；原始 Program 继续走既有 `run` 入口。
