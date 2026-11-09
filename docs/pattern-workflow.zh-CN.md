# Pattern：从预览到保存、复用

本页只说明兼容v1的`control patterns`与MCP固定引用流程；不是高级Pattern v2作者格式。历史基线1d89有12工具，整合候选96a有16；旧f879十一工具与6acc临时daemon属于历史，不再作为当前部署说明。先确认实际CLI帮助、会话tools/list和daemon版本。高级v2的source/recipe保留、CLI七命令及新图案书见[分版CLI合同](patterns-cli.md)和[完整API手册](api-reference.zh-CN.md)，不把v2交给旧patterns导入。

所有Pattern库命令只走daemon，拒绝`--mode direct`。库位置由daemon的`--pattern-dir`/`NB1_PATTERN_DIR`决定；客户端只显示返回路径，不据此打开文件或改用另一份库。读取不存在的库为空，不创建目录；导入才允许创建。

## 一份可直接检查的作者包

[pattern-short-pulse.authoring.json](../examples/runtime/pattern-short-pulse.authoring.json)包含一个名为`short-pulse`的记录：先设黑，60ms渐亮，再240ms渐暗并停顿700ms；重复三次，结束off。每周期计入三步的30ms余量，共9步、3090ms。文件没有serial、执行票据或自动播放指令。

以下命令从本仓库根目录执行，`nb1`替换为已核实的候选二进制。导入只保存，不亮灯：

```sh
nb1 control patterns info
nb1 control patterns import examples/runtime/pattern-short-pulse.authoring.json
nb1 control patterns list
nb1 control patterns get short-pulse
```

import整包校验，保留原输入字节提交一次；同ID同内容幂等，同ID不同内容整包冲突。整库最多128条、规范snapshot最多1MiB，不能通过分次导入绕过。

list返回有界摘要，不等于完整内容或设备能力验证。get返回经过校验的完整Record，含source、compiled_program、compile_contract和真实revision。库中的冻结Program与标题、来源共同参与内容身份；修改任何一项都不能继续使用旧revision。

## 导出为新文件

```sh
nb1 control patterns export short-pulse
nb1 control patterns export short-pulse --output ./short-pulse.snapshot.json
nb1 control patterns export --output ./library.snapshot.json
```

省略ID导出全库；指定ID只导出该记录，均为可重新导入的规范snapshot。`--output`只创建新文件，不覆盖已有文件或符号链接，不自动建父目录。响应验证失败不会先打开输出文件；写入/同步/关闭失败会报告输出路径并保留可能不完整的文件，不能把它当作成功备份。`library.path`不参与输出位置选择。

## 精确revision预览与执行

先从get或import的真实返回中复制完整`sha256:...`到变量`PATTERN_REVISION`，不要填写“latest”或文档里的示意摘要。设备变量`SELECTED_SERIAL`取自本次明确选择的实际设备。

```sh
nb1 control patterns preview short-pulse --revision "$PATTERN_REVISION"
nb1 control patterns preview short-pulse --revision "$PATTERN_REVISION" --serial "$SELECTED_SERIAL"
```

不带serial仅检查冻结Program；带serial只检查daemon缓存的设备能力。两者都不创建Job或保留执行权。

需要执行且已经在授权范围内时，先准备并保存票据：

```sh
nb1 control prepare
```

将其真实返回的request_id和expected_daemon_instance_id分别存入`REQUEST_ID`和`DAEMON_INSTANCE_ID`，原样使用：

```sh
nb1 control patterns run "$SELECTED_SERIAL" short-pulse \
  --revision "$PATTERN_REVISION" \
  --request-id "$REQUEST_ID" \
  --expected-daemon-instance-id "$DAEMON_INSTANCE_ID" --wait
```

这一步会亮灯。run固定采用reject，显式replace/queue拒绝；先一次目标预览，再至多一次执行POST。也可省略完整票据pair，让CLI只Prepare一次并随结果输出，但这份输出不是进程强杀前的持久日志。需要提前保存身份时使用上述显式prepare流程。

202/accepted:true不等于完成。--wait绑定原Job和冻结Program，只查原票据，不因库变化重新解析或执行。查询失败、超时或引用消失时继续保留原身份：

```sh
nb1 control request "$SELECTED_SERIAL" "$REQUEST_ID" \
  --expected-daemon-instance-id "$DAEMON_INSTANCE_ID"
```

不要换request_id、重绑instance或改成原始Program补发。即使本次引用预览失败，原票据此前也可能已经执行；Job/回执查询不依赖当前库。取消只针对已知Job，cancelling仍需等待终态；本页不自动取消或重发。

## 保存高级Action的结果

先检查[flow-orbit.action.json](../examples/runtime/flow-orbit.action.json)：

```sh
nb1 control light preview examples/runtime/flow-orbit.action.json
nb1 control --mode direct light preview examples/runtime/flow-orbit.action.json
```

新Action会返回`result.program`。创建作者包时，将这个完整Program放入记录的`program`字段，保留steps/repeat/on_finish，再用patterns import导入。不要把整个result或action放进去，也不要写`source.action`。

这保存的是该次编译得到的冻结Program。将来若要编辑Action，需要另行保留原Action文件、重新预览并明确处理新版本；库不会替你重新编译或偷偷更新旧revision。可参考短脉冲作者包的合法结构。

## MCP固定版本流程与两种未知

十二工具候选支持`list_patterns({})`和`get_pattern({id})`只读发现，分别返回`pattern_list`与`pattern_record`，均为`accepted:false`。`get_pattern`验证完整Record摘要并保留冻结Program；旧Effect展开由daemon的Store负责。

先读取实际工具schema。支持后，可给`preview_program`传入`pattern_ref:{id,revision}`与可选serial。返回`pattern_preview`，data保持HTTP的`{pattern_ref,preview}`，不创建Job或准备票据。完整revision取自读取的记录，没有latest别名。

播放时先获取并保存原票据，再给`run_program`传serial、原pattern_ref、显式`policy:"reject"`、request_id与expected_daemon_instance_id。内部一次目标预览后至多一次执行提交，并核对返回Job的完整Program；`dry_run`字段不能与pattern_ref同时出现，即使为false。原Program/Effect分支的dry_run零HTTP语义保留。

引用预检失败不证明该票据以前没有执行。未知结果时通过`get_job`查原serial与原票据，不重新读库、不换票据重发。MCP不提供导入/覆盖/删除；保存仍由Web、CLI或HTTP完成。候选能力不代表已安装，旧十一工具客户端不能假装支持引用。

导入错误的`publication_unknown`表示文件可能已经保存，应按requested中的ID/revision读取核对；它不是灯光执行的未知结果，导入不会创建Job。运行错误的`outcome_unknown`才用于原Job状态不明，应查原票据。两种情况都不自动重复写入。

更多字段与文件失败规则见[现有Pattern CLI合同](patterns-cli.md)。本页不改变该合同，也不安装项目Skill、客户端或服务。
