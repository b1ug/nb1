# 本地图案库 CLI（候选）

图案库由 daemon 的 `--pattern-dir` / `NB1_PATTERN_DIR` 管理，默认位置属于运行 daemon 的账号。CLI 只访问本机 daemon；不会打开其返回的路径、隐式启用 direct、创建第二份库或回退到 USB。

```sh
nb1 control patterns info
nb1 control patterns list
nb1 control patterns get build-success
nb1 control patterns import ./patterns.json
nb1 control patterns export --output ./backup.json
nb1 control patterns export build-success
nb1 control patterns preview build-success --revision sha256:<实际摘要>
nb1 control patterns run 20005601 build-success --revision sha256:<实际摘要> --wait
```

info 显示 library 元数据；list 显示版本化库描述与有界 summaries；get 返回完整验证过的 Record。列表不能证明设备支持或执行结果。旧 Program 的总步骤可超过1024（例如单步重复2048次），仍受既定 repeat 和24小时名义时长限制。

import 先完整验证本地包，再把原始字节发送一次，不播放、不准备票据、不重试。输出 publication_unknown 与待核对的 ID/revision；响应丢失或发布结果不明时，重新读取这些记录核对，不盲目再导入。此保存状态与运行的 outcome_unknown 分开。

export 先校验完整规范快照，再写 stdout 或用户指定的 --output 文件。文件以独占创建方式写入，拒绝覆盖已有文件或符号链接，不自动创建父目录。显式空 ID 或空 --output 拒绝。写入、同步或关闭失败时保留该文件并报告路径，内容可能不完整；不会自动删除、覆盖或换路径重试。远端返回的 library.path 仅供显示，不决定本地写入位置。

preview 必须指定实际完整 revision，返回原引用与冻结 Program 预览；可选 --serial 只检查缓存设备能力。没有 latest 或仅 ID 自动选版本。

run 的有效 policy 固定为 reject，显式 replace/queue 被拒绝。没有票据时只 Prepare 一次；可同时提供原 --request-id 和 --expected-daemon-instance-id。随后做一次目标预览并至多发送一次 /jobs POST，核对返回的原票据、Job ID、冻结 Program 与步骤数。

--wait 只查询该票据下同一个已接受 Job，核对同一 Program，不再解析图案库，不重发、不自动取消。查询失败时仍输出原票据与最后一个确认的 Job，并标记 outcome_unknown。即使当前库丢失、损坏或引用不存在，也不能据此断言旧请求没有执行。

这一增量未修改旧 light、Program、Effect 或 MCP 工具的行为。状态以实际安装版本为准；本候选没有自动部署或修改客户端配置。
