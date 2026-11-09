# 本地 CLI 的 light 动作入口

此入口与本地 Web Demo 共用 `nb1-light-action/v1`。文件必须是完整信封；裸 Effect、裸 Program 和未知版本都不能混入。

```json
{
  "contract": "nb1-light-action/v1",
  "action": {
    "kind": "breathe",
    "color": "lime",
    "period_ms": 2000,
    "cycles": 2,
    "on_finish": "off"
  }
}
```

## 预览

```sh
nb1 control light preview examples/runtime/light-breathe.action.json
nb1 control light preview examples/runtime/light-breathe.action.json --serial 20005601
nb1 control --mode direct light preview examples/runtime/light-breathe.action.json
```

默认由 daemon 通过 `POST /v1/light/preview` 编译。`--serial` 只检查该 daemon 已配置设备的缓存能力，不表示任务已准入或已验证光学效果。输出保留动作规范化结果和 Program 预览，两者必须一致，并绑定原始输入。

显式 direct 预览只调用同一个纯 Go 编译器，不连接 daemon、不发现设备、不获取所有权锁、不打开 USB。direct 预览不能提供 `--serial`。所有预览都拒绝 policy 和请求票据参数。

## 执行

```sh
nb1 control light run 20005601 examples/runtime/light-breathe.action.json
nb1 control light run 20005601 examples/runtime/light-breathe.action.json --wait
```

新 light run 的有效策略固定为 `reject`。省略 `--policy` 也不会继承旧命令的 replace 行为；显式 replace／queue 会被拒绝。旧 set、run、effect 命令的默认策略保持原样。

daemon 模式在未提供票据时仅 Prepare 一次完整请求标识，再向 `POST /v1/devices/{serial}/light` 提交一次原始 Action。客户端使用共享编译器校验输入和返回内容，但不会把本地编译的 Program 改发到旧 `/jobs` 路由。完整最终请求体也受 65536 字节限制。

成功输出包含 `request_ticket`、`job`、`replayed` 和 `daemon_instance_id`。只在 HTTP 202、唯一且正确的实例／重放头、回执位置、设备、规范 Program 和步骤进度都匹配时确认接受。

如果请求或响应的结果不确定，命令以非零状态退出，并保留同一个 `request_ticket`，同时给出 `outcome_unknown:true`。不会换一个标识重发，也不会回退到旧 Program 提交或直连 USB。输出的 `accepted:false` 在结果未知时表示“未能确认接受”，不能据此断言任务没发生。

`--wait` 用原票据查询任务，并要求返回同一个已接受 Job。等待超时不取消 daemon 中的任务；已确认接受但完成状态未知时，输出可以同时是 `accepted:true` 和 `outcome_unknown:true`。

调用者也可以先准备并自行保存票据，再把完整一对参数交给 light run：

```sh
nb1 control prepare
nb1 control light run 20005601 examples/runtime/light-breathe.action.json \
  --request-id REQUEST_ID --expected-daemon-instance-id DAEMON_INSTANCE_ID
nb1 control request 20005601 REQUEST_ID --expected-daemon-instance-id DAEMON_INSTANCE_ID
```

自动 Prepare 的票据随本次结果输出；这不是掉电或进程强杀后的持久日志。需要在进程异常终止后仍找回请求时，应在执行前显式准备并保存票据。

## 显式 direct 执行

```sh
nb1 control --mode direct light run 20005601 examples/runtime/light-breathe.action.json
```

Action 在接触设备前完整校验，然后复用已有 Engine、设备所有权锁和退出清理。direct 执行总是在当前进程内等待，不因省略 `--wait` 就留下后台服务。它不接受 daemon 票据。

正常完成且 `on_finish:hold` 时，最后的物理颜色可以保持；这不表示进程外还有 worker、Job 查询、回执或自动恢复。中断中的任务会通过已有清理路径尝试熄灭并释放所有权；客户端超时不能保证瞬间打断正在进行的原生 USB 调用。daemon 模式的任务、监测与历史则由长期运行的服务继续管理。
