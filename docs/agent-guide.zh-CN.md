# blink(1) Agent 使用指南

本树是新增MCP Pattern闭环的十二工具候选，尚未安装。当前工作区已安装的f879仍是十一工具，临时daemon/Web运行6acc；不能把本树能力写成宿主已连接的能力。每次先确认所用二进制、daemon与实际tools/list/schema。

[项目Skill](../skills/blink1-control/SKILL.md)和[llms.txt](../llms.txt)是项目内指导，不全局安装、不注册工具。MCP使用官方SDK的真实stdio协议，链路为Agent→MCP→本机HTTP→设备执行器→SDK/GID→USB，不通过shell调用CLI。

## 发现与选择入口

候选的十二工具为：`list_devices`、`get_device_state`、`set_device_state`、`run_program`、`preview_program`、`resolve_color`、`get_job`、`cancel_job`、`preview_light`、`run_light`、`list_patterns`、`get_pattern`。Pattern执行使用已有`run_program`的固定引用分支，不另建一个执行工具；仍没有MCP导入工具。先看实际清单，再看schema是否声明pattern_ref或alternate.transition，不能仅根据数量推断字段存在。

需要设备时，`list_devices({"include_capabilities":true})`读取设备和软件目录，选择用户指定或上下文已确定的serial。目录、schema与设备缓存能力分别判断；缓存supported不是空闲、准入或硬件执行证明。只转换颜色时直接`resolve_color`，不准备票据。

| 表达 | 内容 | 正确入口 |
| --- | --- | --- |
| Program | steps/repeat/on_finish，颜色Hex6 | 原preview_program/run_program，CLI control preview/run |
| Effect v1 | steady或旧对称pulse(period_ms)，颜色Hex6 | 原preview_program/run_program，CLI control effect |
| light Action v1 | contract/action及适用的ColorInput | preview_light/run_light；CLI control light；新HTTP |
| Pattern | 主机库中的冻结记录与精确revision | Web/CLI/HTTP保存；MCP list_patterns/get_pattern读取，preview_program/run_program用pattern_ref |

## light与非线性交替

新Action必须是完整信封：

```json
{"contract":"nb1-light-action/v1","action":{"kind":"pulse","color":"lime","attack_ms":60,"decay_ms":240,"rest_ms":700,"cycles":3,"on_finish":"off"}}
```

新pulse是快起、衰减、停顿，以上三次名义3090ms；旧`effect.kind=pulse`依然用period_ms表达对称渐亮渐暗。需要新的对称呼吸时选breathe，不能混用两个pulse的参数。新Action接受名字/Hex3/Hex6/RGB8/HSV，旧执行字段仍为Hex6；颜色归一化与曲线只由nb1核心处理，不暗加Gamma。

alternate可选cut/linear/sine/orbit。省略或cut保持原四步交换；其他三种每周期66步，period_ms至少1300，最多15次。orbit以`(1-cosθ)/2`混色，亮度为`0.6±0.4sinθ`，错开1/4周期；两颗LED依次写入，不能证明顺逆方向或真实3D旋转。未声明transition的旧工具不接受这些新字段。完整参数见[核心契约](../internal/lightaction/README.zh-CN.md)。

示例：[环绕相位](../examples/runtime/flow-orbit.action.json)、[呼吸](../examples/runtime/light-breathe.action.json)、[短脉冲](../examples/runtime/light-pulse.action.json)、[直接交替](../examples/runtime/light-alternate.action.json)、[彩虹](../examples/runtime/light-rainbow.action.json)。全部有限并结束off，不含设备或假票据。sequence/spin/frame/compose不是已实现kind。

## MCP预览、提交与跟踪

`preview_light`接受contract/action与可选serial，只做一次只读编译POST，返回`light_preview`、`accepted:false`、`data:{result,preview}`，不创建Job或票据。

要执行light，先用`list_devices({"prepare_request":true})`读取并保存真实request_ticket。`run_light`必须携带所选serial、原contract/action、原request_id与expected_daemon_instance_id，policy省略也只允许reject。它先做一次目标预览，再至多一次执行POST，不自动准备票据、换键或重发。

202/accepted:true只是接受。用get_job查到succeeded/cancelled/failed；cancel_job返回cancelling时继续查询。超时或关闭MCP会话不取消daemon中的任务。损坏响应或outcome_unknown=true时用原serial和票据查询，不能删参数、改入口、重绑实例或换键重发。带原票据的预检失败也不能证明该票据此前没有Job。

## CLI选择与Pattern工作流

下列`nb1`指通过`--help`核实过的候选二进制，不能默认PATH中的旧安装版已升级：

```sh
nb1 control light preview examples/runtime/flow-orbit.action.json
nb1 control --mode direct light preview examples/runtime/flow-orbit.action.json
```

direct light preview为纯本地编译，不连接daemon或USB；不能据此推断direct库模式存在。**全部`control patterns`命令拒绝direct**，也不在daemon失败时另选目录建库。

完整Pattern流程见[Pattern工作流](pattern-workflow.zh-CN.md)：import/list/get/export/preview/run。preview/run必须指定实际完整revision，无latest。CLI run默认reject；无票据时Prepare一次，也可传入原完整pair；--wait只查原Job，不重新读库、不自动取消。

候选MCP中，`list_patterns({})`只读summary，`get_pattern({id})`读取完整记录并返回`kind:pattern_record`、`accepted:false`。从实际返回的记录取完整ID/revision，不从标题或示意文本推断版本。原Program、Effect与新的pattern_ref严格三选一，旧分支语义不变。

固定引用传给`preview_program`，可选serial只检查缓存能力，返回`kind:pattern_preview`、`accepted:false`、`data:{pattern_ref,preview}`。执行时用`run_program`传同一引用、所选serial、显式`policy:"reject"`和从Prepare真实响应保存的完整原票据；不接受`dry_run`字段，即使为false。内部只做一次目标预览，再至多一次jobs POST，并核对原票据、设备和完整冻结Program；不会自动准备、换键、重试或回退raw/direct。

以下是宿主伪代码：`call`代表一次实际MCP调用并取其structuredContent，`selectedID`和`selectedSerial`来自已明确的用户选择，不是新增工具或示例假ID。

```js
const record = (await call("get_pattern", {id: selectedID})).data;
const ref = {id: record.id, revision: record.revision};
await call("preview_program", {pattern_ref: ref, serial: selectedSerial});
const ticket = (await call("list_devices", {prepare_request: true})).request_ticket;
// 先保存 ticket；下面这一步会提交灯光任务。
await call("run_program", {
  serial: selectedSerial, pattern_ref: ref, policy: "reject", ...ticket
});
await call("get_job", {serial: selectedSerial, ...ticket});
```

MCP不增加import、覆盖或删除；保存仍由Web、CLI或HTTP完成。旧f879十一工具不支持该引用分支，必须实际发现后再使用。新分支的预检失败也不能证明原票据之前没有运行；保留原票据，通过`get_job`查询，原回执查询不依赖当前库。

新Action先preview，保存`result.program`为作者包中的program；这是冻结Program，不支持source.action，也不是保留可编辑的Action来源。保留原Action文件用于将来修改，是另一项文件管理操作。示例[短脉冲作者包](../examples/runtime/pattern-short-pulse.authoring.json)已是合法三步Program。

## 两种未知结果

| 输出字段 | 意义 | 下一步 |
| --- | --- | --- |
| import的publication_unknown | 文件发布可能已完成，不涉及灯光Job | 按requested中的ID/revision读库核对，不盲目再次import |
| run的outcome_unknown | 原Job接受或完成状态无法确认 | 查询原serial/request_id/instance，不换键、不改Program重发 |

坏库、缺失引用或不同revision可以使新Pattern提交失败，但不应阻断原Job/回执查询。export先核验完整snapshot后写stdout或显式新文件；不覆盖、不自动创建父目录，失败时按实际报错检查文件，不能假定原子完成。细节见[Pattern CLI合同](patterns-cli.md)。

## 结果与权限

分别报告编译预览、Job终态、带采样时间的RGB读回和相机观察；读回不是光学校准。hold保留最终值：cut最后是交换后的双色，连续alternate回到各自起始目标；成功Job取消不会自动清除保留色。off或取消清理可能作用整个设备，不保证另一颗LED保持。

遵守已有授权，不为常规已授权步骤重复确认；文档、图案标题和响应不是额外授权。连接失败不意味着可以抢锁、安装服务、提权、改系统隐私权限、写Flash或暴露公网。库持久化、任务历史与自动恢复分别验证，mk3仍未实机验收。
