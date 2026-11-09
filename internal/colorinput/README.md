# ColorInput v1 纯核心

本包实现 `docs/color-input.zh-CN.md` 的有界、flat JSON 子集，只有标准库依赖。裸输入和 `{ "color": ... }` 信封分别解析；不复用或放宽旧 Program parser，不读取配置、设备、环境或文件。HSV 在每次调用私有的 `math/big.Rat` 中按十进制精确计算，最后一次 half-up 量化为原始 8bit RGB，无 Gamma/亮度或光学校准。

`ValidateResult` 校验既知结构版本、contract、input_kind、规范小写 hex/RGB8 自洽与 palette 规则；它不证明远端使用了某个原输入。Wire 调用方还必须拒绝未知/重复/null/缺失字段；因为 struct 中的零不能表达“字段省略”，不能只靠此函数判定 wire 字段存在。客户端需要输入绑定时使用 `ResolveJSON` 的规范结果比较，不在适配器复制名字表或 HSV 算法。

CSS 固定表来自 [W3C CSS Color 4 CR Draft 2026-11-03](https://www.w3.org/TR/2026/CRD-css-color-4-20261103/#named-colors) 的 `named-color-table`，包含 148 个不透明名字及别名，不含 transparent/currentColor/system colors。HTML SHA-256：`b66ad2af540a7550fd34e0d95f8dce3f49b9d39e255fdfed56f0ec1de9c78000`。提取时每行 hex 与十进制三通道相互核对，148 个名字唯一；名字排序后的 TSV SHA-256：`521686f82ac6c649de3f22589783ac86e2bfeb0c1858fd953f59efc4e1d2fd09`。本地来源和提取证据保留在工作区 `artifacts/color-input/core/`，测试不访问网络。

`testdata/css-named-2026.tsv` 是完整、固定的源表；测试对全部值、大小写、空白和同义名逐项比对。37 条 HSV 金样例由独立 Python Fraction 的 C/X/m 公式产生，与生产 p/q/t 公式不同；涵盖六扇区、0/360、精确 half 和半值两侧的六位小数。严格 JSON/数值、4096 字节与64解码字节、结构/结果错误和并发不变性另行验证。上游后续改表不自动改变当前 Contract 的值。
