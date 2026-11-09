# API 文档样例与离线校验

`docs/openapi.json` 保持 OpenAPI3.2.1 / JSON Schema2020-12，覆盖源码库存的44个method+canonical path（其中3个是bundle条件静态资源）；另加4个显式GET别名，使操作名`import`/`preview`/`preview-ref`仍能作为普通图案ID，避免OpenAPI静态path优先级掩盖模板GET。因此实际文档是43个path /48个operation /111个schema；别名用`x-alias-of`标识，不是新增能力。

来源固定为日常源码`1d89eae92278f24a68fff1a34e3c1e696a962c3c`与开发源码`f8bb267b5cf8b952ce6a8cdbd86e8b18971028ae`。每个操作的`x-availability`和`x-api-level`区分已存在接口、未安装开发接口与显式Store依赖。此处是源码/文档证据，不是新daemon探测、安装或硬件验收。

开发源码已完成 P4 typed 身份/摘要与原子准入、P5 完整来源验证 resolver、P6 固定 Ref 预览/运行/原票据查询/绑定取消。daemon 显式懒选独立 `patterns-v2`，支持 `--pattern-v2-dir` / `NB1_PATTERN_V2_DIR`；Go handler 未配 option 时库操作501，配置错误503。构造/纯作者preview/读缺失库不创建，只有有效写入进入事务才可能创建，后续失败也可能留目录。所有开发能力尚未安装到日常1d89。新Pattern三policy与旧light/raw Frame仅reject的差异保留；独立本地CLI作者候选eee1eb9另已提供get/preview-draft/save（未安装、不播放）；新v2执行客户端继续分片，MCP文档仍保留日常12工具。
低层Program HTTP支持replace/queue/reject并允许旧无票据提交；高级light、raw Frame HTTP及legacy Frame core入口仅reject+完整票据，新Pattern核心可按三policy准入冻结帧。Frame JSON仅省略repeat/on_finish才默认1/off，显式0/空串拒绝；不能照搬Go typed Normalizer的补默认规则。详情以各schema与`x-runtime-validation`为准。

## 样例来源

- `generate.go` 从实际纯编译器产生八动作、4/8 Orbit、FrameProgram等输入/规范化结果；还通过`httpapi.New(nil)`和`httptest.NewRecorder`获取实际preview/capability响应。P6 引用预览额外使用纯 P1 生成的真实已验证 Record 与内存 exact Resolve；不创建Engine、文件Store、监听器、准入票据或真实Job。
- `actual-samples.json` 有52项：原38项继续保留（32纯codec、5无Service handler、1合成FrameJob），增加8个P6身份/请求、4个无Service内存记录handler预览、2个合成Pattern Envelope。所有合成Job/Envelope只验证wire字段，不冒充接收、票据或执行结果。
- `route-inventory.json` 是本次只读库存的冻结副本，检查器以method和路径模板形状核对44个canonical操作；`{id}`与既有`{jobId}`命名差异不改变URI语义。
- P3请求/响应直接读取仓库原有`internal/httpapi/testdata/pattern-v2`，不由文档schema反向生成golden。

## 复现

生成样例仅编译本地已存在的依赖，不联网、不访问硬件；模块未缓存时会明确失败，不安装额外内容：

```sh
GOWORK=off CGO_ENABLED=0 GOPROXY=off GOSUMDB=off go run -mod=readonly ./docs/contract-fixtures
```

检查器需要现有Python环境中的`jsonschema>=4`与`referencing`（文档检查依赖，不是nb1运行依赖）。本轮使用本机已缓存jsonschema4.26.0；未安装或下载包：

```sh
python3 docs/contract-fixtures/check_openapi.py --report /tmp/nb1-openapi-check.json
```

它验证JSON、111个schema的Draft2020-12结构、全部本地ref、operationId/path参数、库存、实际样例、既有P3golden与结构负例。Registry只注册本地文档，拒绝外部ref，不请求外部网络。它不是完整OpenAPI metaschema认证。

结构schema无法证明原始数字词法（标准`integer`会接受数学上的1.0）、无重复键、Unicode scalar、原始字节/层数、颜色字典、聚合时长/写入预算、canonical顺序、SHA或高级来源编译绑定。检查器特意保留三类“schema通过、runtime拒绝”的样例：小数时间词法、错误expected_revision、格式正确但不匹配的execution_digest。`x-json-number-lexeme`/`x-runtime-validation`明确标注这些边界；不能把schema通过当作设备可执行或完整性通过。

合法未知source/recipe的已存记录使用Read schema；新导入使用Known/Write schema且仍需runtime完整验证。Store会排序并规范化合法导入输入；输入本身不必预先排序/逐字节canonical，存储与导出输出才要求精确canonical。

P6 的POST原始报文限4096字节，Ref/票据均闭合ASCII；query四标量必须唯一，不接受body/空分隔段。schema只能描述结构/拼写，不能证明无重复键、请求窗口、Ref与完整来源、Envelope与实际回执一致。动态pattern_execution只报告service+resolver接线，不调用Store，也不保证库健康或设备执行。检查器包含四种合法接线状态及反例、P6请求缺失/越权字段、union错配与runtime-only摘要差异；当前离线检查118项（含3个实际host条件资源观察）。

Web host源码56c4aef支持两种完整bundle，但该源码仍固定旧Web fe32507产物；新增/demo/patterns.html/js/css各自404，只有明确import完整扩展dist才200。静态路由+3不增加硬件动作API，也不意味着日常已升级。generate.go额外写resource-observations.json：直接无Service Recorder探三路径，旧bundle验证标准404，新bundle时比对嵌入文件字节/安全headers；不创建listener/设备。工具schema和真实stdio证据仍绑定MCP c37，错误头补片679只作实现注记，不改写证据来源。
