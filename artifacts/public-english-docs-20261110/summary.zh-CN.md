# 公开英文文档片：2026-11-10

本片从干净 `1e2e7650e2cff6686d95cb9908953e5fd370f2bc` 创建独立 worktree `validation/nb1-public-english-docs`，分支 `docs/public-english-20261110`。只修改公开文档并增加本地校验/盘点材料；未修改 Go 生产代码、SDK pin、冻结 MCP 工具对象、schema 快照或任何实际安装配置。

## 完成内容

- 新增 [完整英文 API 手册](../../docs/api-reference.md)：High/Low 职责、八种 v1 Action、实验 Orbit、颜色、Program、FrameProgram、旧 Effect、任务/原票据/取消、策略差异、Pattern v1/v2、CLI/MCP、全部 HTTP 索引、错误组、111 模型目录与14输入例子。
- README 默认链接英文手册；llms.txt 和例子目录增加英文入口。中文手册、中文 README、中文例子说明保留原字节。
- OpenAPI 254 个字段的人工说明/summary/info.title 变为英文。历史1d89/f8bb说明与当前工作区安装96a分开，不再让介绍文字暗示 P3/P6 仅在未安装原片里存在。所有非许可说明值完全相等，`x-availability` 原样保留。
- 安装状态仍按96a的 HTTP44、CLI七命令、实际stdio MCP16 和旧 Web bundle；源码后续候选才有 CLI第八项 list、MCP20作者工具、transport trace 与固定英文 Web。文档提交不更新已安装字节。
- 主机 Pattern 是 create-only：修改周期等内容必须另用新 ID；保存成功、原任务接受、可信终态和物理效果各自判断。publication_unknown 与 outcome_unknown 不混用。
- SDK仍固定 b3；81ae只作为已审未pin背景。设备RAM播放槽、Flash与notes API仍是研究提案，不在此手册虚构 nb1 endpoint。

## 校验

执行 `python3 artifacts/public-english-docs-20261110/validate.py`，结果见 [validation.json](validation.json)：

| 项目 | 结果 |
| --- | --- |
| HTTP规范操作 | 44条，逐条与固定结构库存一致；占位符id/jobId按同一位置比较 |
| OpenAPI | 43 paths、48 operations；含4个普通ID GET别名；非说明结构完全相等 |
| schema模型 | 111个，手册完整链接，内部`$ref`均可解析 |
| 独立JSON示例 | 14个，原字节与SHA256完全相等 |
| OpenAPI内嵌example/examples | 6个原始JSON片段，逐字节完全相等 |
| 手册JSON代码块 | 7个，与原中文手册数据一致；作者中文title保留 |
| 本地链接 | 275个目标存在，JSON Pointer可解析 |
| MCP冻结对象 | 原16 complete tool objects不变；12/16/20快照文件原字节/SHA均不变 |
| 本地库/锁/配置/Go源 | 不变；未操作daemon、USB、浏览器、摄像头或用户服务 |
| git空白检查 | `git diff --check`通过 |

校验器只读本地git固定源和工作树文件，不访问网络或硬件。本片没有新增物理资格，也没有重跑任何硬件窗口。公开材料没有复制私有录像、相机图片或原始私有日志。

独立只读复核为 [PASS_WITH_LIMITS](review.json)，已修正并二次核对历史commit拼写、led1/led2字段拼写、GET回执误继承的POST上限文字，以及错误分组标题后的空行。没有剩余复核阻塞项；限制仍是本地文档校验，不提升安装或物理资格。

## 仍未完成

公开文档默认英文已落地本片指定入口，但运行时默认文本并未在本片全部英文化。Go AST盘点发现197个中文字符串字面量，分布11个生产文件；详见 [gaps.zh-CN.md](gaps.zh-CN.md) 与 [逐字面量清单](runtime-chinese-inventory.json)。这是后续运行时国际化待办，不能把本片称为整个CLI/服务默认文本已经英文。

源码默认英文Web已在父基线完成，但是否安装与实际使用必须单独核对；本片不改变96a安装状态，不注册宿主MCP、不创建开机服务、不合并PR或发布Release。中文内部规划站维持原样。
