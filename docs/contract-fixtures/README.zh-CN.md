# API 文档样例与离线校验

`docs/openapi.json` 保持 OpenAPI3.2.1 / JSON Schema2020-12，覆盖源码库存的37个method+canonical path；另加3个显式GET别名，使操作名`import`/`preview`仍能作为普通图案ID，避免OpenAPI静态path优先级掩盖模板GET。因此实际文档是36个path /40个operation /106个schema；别名用`x-alias-of`标识，不是新增能力。

来源固定为日常源码`1d89eae92278f24a68fff1a34e3c1e696a962c3c`与开发源码`4d625891cc421c037807e2e6a193174016c05c71`。每个操作的`x-availability`和`x-api-level`区分已存在接口、未安装开发接口与显式Store依赖。此处是源码/文档证据，不是新daemon探测、安装或硬件验收。

P3 Pattern v2的保存/读取/导入导出仅有Go Option显式注入，默认开发server未配置v2库时返回501；纯preview不依赖Store。它不运行图案，不注册P4的run/query/cancel。旧v1线形和既有数值范围保留；补充遗漏的错误码及纠正共享说明不会改变运行行为。

低层Program HTTP支持replace/queue/reject并允许旧无票据提交；高级light、raw Frame HTTP及当前Frame core均仅reject+完整票据。Frame JSON仅省略repeat/on_finish才默认1/off，显式0/空串拒绝；不能照搬Go typed Normalizer的补默认规则。详情以各schema与`x-runtime-validation`为准。

## 样例来源

- `generate.go` 从实际纯编译器产生八动作、4/8 Orbit、FrameProgram等输入/规范化结果；还通过`httpapi.New(nil)`和`httptest.NewRecorder`获取实际preview/capability响应。它不创建Engine、Store、监听器、票据或真实Job。
- `actual-samples.json` 有38项：32个纯codec样例、5个无Service实际handler响应、1个明确标记`synthetic-Go-wire-struct`的FrameJob。该合成结构只验证wire字段，不冒充接收/执行结果。
- `route-inventory.json` 是本次只读库存的冻结副本，检查器以method和路径模板形状核对37个canonical操作；`{id}`与既有`{jobId}`命名差异不改变URI语义。
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

它验证JSON、106个schema的Draft2020-12结构、全部本地ref、operationId/path参数、库存、实际样例、既有P3golden与结构负例。Registry只注册本地文档，拒绝外部ref，不请求外部网络。它不是完整OpenAPI metaschema认证。

结构schema无法证明原始数字词法（标准`integer`会接受数学上的1.0）、无重复键、Unicode scalar、原始字节/层数、颜色字典、聚合时长/写入预算、canonical顺序、SHA或高级来源编译绑定。检查器特意保留两项“schema通过、runtime拒绝”的样例：小数时间词法和错误expected_revision。`x-json-number-lexeme`/`x-runtime-validation`明确标注这些边界；不能把schema通过当作设备可执行或完整性通过。

合法未知source/recipe的已存记录使用Read schema；新导入使用Known/Write schema且仍需runtime完整验证。Store会排序并规范化合法导入输入；输入本身不必预先排序/逐字节canonical，存储与导出输出才要求精确canonical。
