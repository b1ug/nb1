# 本地图案库 CLI：v1 与高级 v2（候选）

> 当前安装说明（2026-11-10）：固定1e2e765已在本工作区运行，CLI高级图案八命令（包含list）、真实stdio MCP20及英文动森Web已接入，安装后38项只读检查通过。原96a／CLI7／MCP16记录属于历史来源；具体当前API与限制见[完整API手册](api-reference.zh-CN.md)。快照import/export客户端仍未安装，硬件RAM/Flash/Notes API仍属独立设计。源码后续英文帮助更改不等于已安装二进制发生变化。

本文按格式分开：`control patterns`是兼容v1，`control patterns-v2`是保留高级来源的独立库；不能交叉当作自动升级。当前工作区已安装96a，包含两组命令；1d89/eee/4e只作为历史资格来源，部署记录见API手册第1节。

## 兼容 v1：control patterns

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

## 高级 v2：control patterns-v2

作者候选eee1eb9已实现get/preview-draft/save；执行候选 `4eac5293bbad413e90ff19e6c8700628a1a4a8ee` 已实现固定Ref preview/run/request/cancel。原七命令与list已进入当前1e，共八命令，daemon-only；原片软件/96a历史实物/1e有限实物与安装证据分别记录。工作区安装看[API手册第1节](api-reference.zh-CN.md)，HTTP对应P3/P6，旧v1命令与格式不变。

v2库由 `--pattern-v2-dir` / `NB1_PATTERN_V2_DIR` 独立选择，默认daemon账号 `UserConfigDir()/nb1/patterns-v2`，不随自定义旧--pattern-dir改变。构造、纯作者preview与缺失库读取不创建目录；有效保存/导入进入写事务时才可能创建，后续失败也可能留目录。既有库读取可能创建锁文件。CLI不会读取daemon返回的本地路径。

```text
nb1 control patterns-v2 list
nb1 control patterns-v2 get ID
nb1 control patterns-v2 preview-draft FILE
nb1 control patterns-v2 save FILE
nb1 control patterns-v2 preview ID --revision SHA [--serial SERIAL]
nb1 control patterns-v2 run SERIAL ID --revision SHA [--policy reject|queue|replace] [--request-id KEY --expected-daemon-instance-id INSTANCE] [--wait]
nb1 control patterns-v2 request SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE [--job-id ID] [--execution-digest SHA] [--wait]
nb1 control patterns-v2 cancel SERIAL ID --revision SHA --request-id KEY --expected-daemon-instance-id INSTANCE [--job-id ID] [--execution-digest SHA] [--wait]
```

preview-draft只收作者 `{id,title,source}`，save再加确认本次完整候选的expected_revision；它不是覆盖权限或当前库版本。保存返回publication_confirmed/publication_unknown与原Ref，不准备票据、不播放；来源、recipe、冻结Program/Frame一同保留。合法unknown记录仍只读，不自动编译替换。

运行使用完整固定revision，不猜latest，不把execution_digest当作作者revision。record_contract固定 `nb1-pattern-record/experimental-v2`，在请求与输出中保留。run的有效默认是reject，显式三policy均可，不修改旧CLI父命令replace默认或旧v1/light仅reject限制。无票据只Prepare一次；有完整原票据零Prepare。与旧v1不同，新run不先Get/preview库，而是单执行POST交给core先查原回执，仅新key再验证来源。

request/cancel和accepted replay不依赖库。输出保留原pattern_ref、request_ticket、执行摘要、typed Job/FrameJob；job字段是带来源的PatternJobEnvelope，不能用旧裸Job/history查询这些任务。accepted表示原任务已确认接受，不是完成；cancel_attempted表示是否尝试过唯一取消POST。

`--timeout`默认30s，Prepare、提交与--wait共享一次命令预算；长图案可用`nb1 control --timeout 2m patterns-v2 run SERIAL ID --revision SHA --wait`（替换真实变量），或保留原票据后request继续观察。

--wait只读原回执，强绑定Ref/实例/request_id/Job ID/摘要、完整冻结执行与单调进度/生命周期；timeout不取消、不重发。已验证终态先于随后context结束，明确failed/cancelled可非零退出但outcome_unknown=false；后端optional错误文案为空不妨碍认定failed终态。

取消先GET绑定，terminal/cancelling零POST，其余最多一次含confirmed_job_id的POST；终态hold不会补关灯。坏响应或丢失保留最后可信before状态、原票据与unknown，即使--wait也不再自动GET来覆盖错误。用户之后可显式request原票据；不因404/410/重启或库失败换新票据补播。

CLI仅对明确status/code白名单认定本次准入拒绝；普通404、未知code/错status、3xx、5xx/501、过期、实例变化和传输/完整性失败都保持unknown。确切白名单与源码见[内部客户端执行说明](../internal/daemonclient/README.pattern-execution-v2.zh-CN.md)，完整HTTP合同见[API手册](api-reference.zh-CN.md)。96a的CLI list/import/export尚未提供，不能用旧v1导入命令写入v2库。

此候选软件验证覆盖Program与Frame、库消失后的原回执恢复、单POST、取消与等待；只用临时库、私有loopback和内存驱动，没有新的USB/视觉验收。开发MCP16与历史12仍分别以各自会话tools/list和部署版本为准。

## 后续候选：高级列表（尚未推广）

开发源码另已接入 `nb1 control patterns-v2 list`，作为第八个命令；独立资格来源9ba82af。它只GET一次 `/v2/patterns`，返回有界且排序的摘要，保留未知来源/recipe为只读条目，不逐项Get、预览或准备票据，不播放。

路径仅供展示，无论服务端使用POSIX、Windows盘符或完整UNC，都不按客户端OS访问文件。列表只能核对广告元数据；完整来源和revision/digest的复验仍用 `get ID`。详细合同与软件范围见[列表候选说明](../internal/daemonclient/README.pattern-list-v2.zh-CN.md)。它没有随文档更新安装到96a；snapshot导入/导出仍是后续片。
