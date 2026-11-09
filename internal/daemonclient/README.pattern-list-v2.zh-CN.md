# 高级图案列表：CLI与HTTP客户端候选

本片在96a已有七命令之外新增 `control patterns-v2 list`，目前是独立开发候选，尚未更新本机已安装96a。它只发一次 `GET /v2/patterns`，不逐项取Record、不预览、不准备票据或播放，不访问USB。旧v1 `control patterns list`及其响应不变。

列表保留高级库、排序摘要、完整Ref、来源合同、执行类型/合同、执行摘要与服务端verification；合法未知来源/recipe条目照常显示，库的read_only状态不会令已知条目被静默漏掉。

客户端检查闭合字段、Unicode、2MiB既有响应预算、128项上限、ID唯一排序、合同/摘要拼写、执行union、验证原因、ready/read_only与可用性的一致性。空列表必须是 `[]`，不接受null/缺失字段、重复键、孤立surrogate或运行回执头（包括空值）。返回路径只是服务端展示信息；POSIX、Windows盘符和完整UNC均不依赖客户端OS，也不会被当作本地路径打开。

**列表没有完整来源，不能独立复算revision或执行摘要。** 这里的verification是服务端广告，不等于客户端已复验完整Record、设备可执行或用户同意播放。需要完整来源与编译身份时用已有 `patterns-v2 get ID`；需要执行时仍以精确Ref和原票据走核心准入。

新命令只接受daemon模式，不接受运行policy/wait/票据，不自动起daemon或回退direct。连接/格式错误返回原错误，不再请求第二份列表，不用v1接口代替。

本片软件验证使用临时库、真实HTTP handler和私有loopback fixture，验证缺失库列举不创建目录、保存后发现排序记录、未知条目保留、单请求、跨OS路径与错误边界。没有真实设备、日常服务或用户图案库操作；尚不构成新跨平台实体USB资格。CLI快照导入导出及MCP高级库作者工具仍为后续片。
