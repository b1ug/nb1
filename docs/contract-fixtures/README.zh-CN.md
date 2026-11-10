# API 文档样例与离线校验

当前已安装runtime为固定`6ca5a14e9f7fec8a5766e79f5bdef3fafef66d8f`，独立受控推广有57项只读后验，另4项隔离只读UI检查；证据在API手册的部署段。文档检查本身不运行HTTP、daemon、CLI、MCP、USB、摄像头或安装，不把外部执行资格重跑一次。

## 统计口径

源码库存是**46个规范method/path**：历史1e的44个，加`GET /demo/i18n.js`与`GET /demo/locales.js`。两项都是条件本地静态资源，不是新设备动作。OpenAPI为了不让静态操作名遮住合法Pattern ID，保留原来的4个普通ID GET别名：

- `GET /v1/patterns/import` → `/v1/patterns/{id}`。
- `GET /v2/patterns/import` → `/v2/patterns/{id}`。
- `GET /v2/patterns/preview` → `/v2/patterns/{id}`。
- `GET /v2/patterns/preview-ref` → `/v2/patterns/{id}`。

因此是**50个OpenAPI operation／45个path／111个schema**，不是遗漏4条路由或增加4套能力。别名仍用`x-alias-of`标识。111模型、14个公开JSON输入示例和20个完整MCP工具对象都保持原字节；CLI现有十命令的import/export使用原存储路由，不新增MCP工具或设备RAM/Flash/Notes API。

## 保留的原始证据

- `actual-samples.json`仍是52项原始纯codec／无Service Recorder／明确标注的合成wire形状样例，来源f8bb；合成Job不等于真实任务。
- P3请求／响应直接取自仓库原有`internal/httpapi/testdata/pattern-v2`，不由schema反向生成。
- `resource-observations.json`保留历史host56c/basic bundle的3个404；`resource-observations.current.json`这个旧文件名保留历史96a/f90的3个200，内容与来源没有重写为6ca。
- `numeric-lexeme-observations.json`保留历史96a的8个真实无Service Recorder词法对照，原始`-0/0.0/0e0`字符串不先解码再重编码。
- `candidate-resource-inventory.json`是固定6ca源码／嵌入manifest的**文件清单投影**，11个公开资源及隐藏路径，不是本轮HTTP观测。它与外部独立安装／只读UI证据分别标识。

`generate.go`是固定96a/f90的历史复现工具。它现在在任何输出写入前检查该历史bundle，其他源码／产物会拒绝；不要在6ca工作树执行它来伪造新捕获。本轮没有运行这个Go生成器，也没有重跑已通过的Go测试。当前清单与模型检查用下面的纯离线Python检查器。

## 离线检查

使用已存在的`jsonschema>=4`与`referencing`环境，不安装或下载包：

```sh
python3 docs/contract-fixtures/check_openapi.py --report /tmp/nb1-openapi-check.json
```

它检查Draft2020-12结构、本地ref、operationId／参数、46规范库存与4别名、111模型、原始golden及结构负例，验证新文件清单与嵌入字节。Registry仅注册本地文档，不请求外部网络。它不是完整OpenAPI metaschema认证，也不证明原始JSON词法、重复键、Unicode scalar、SHA／来源编译对应、聚合时长、设备支持或物理效果。原先特意保留的“schema通过、runtime拒绝”例仍保留，不能把结构校验当作执行资格。

未知source／recipe的合法已存记录仍通过Read schema作为只读数据保留；新导入须通过Known／Write及运行时完整性验证。保存、受理、可信终态、RGB读回与光学观察是不同证据。
