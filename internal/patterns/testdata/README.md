# 独立 canonical 金样例

`golden.json` 由独立审阅者只按冻结文字合同写 `generate.py` 生成；其生成阶段未阅读或调用 Go codec。Python 仅使用标准库，选择有限的 steady/pulse/Program 正例，不是产品导入 parser。

用 `python3 internal/patterns/testdata/generate.py` 可重建这 16 个样例和 9 个内容身份关系。`python3 -O` 同样验证并得到相同数据，检查不用可移除的 `assert`。原始独立收据保存在工作区 `artifacts/pattern-codec/independent-golden/validation.json`。

Go 测试使用 `canonical_hex` / `preimage_hex` 解码得到精确字节并对比实际 codec，**不对整个文本 fixture 的换行做内容身份判断**。这样 Windows Git 的文本换行转换不会改变金样例语义。域前缀中的 LF 是明确的 byte 0x0a，canonical JSON 本身无尾换行。

`pulse-800x2-chinese-defaults` 的真实 revision 是 `sha256:d799c66cb9580f8342068ff586e7d61cd4377961fc8aab629f7e67cd370960b0`。其中两段 fade 为 380/390ms、repeat=2、结束 off。其他样例覆盖 Program/Effect 同执行不同来源、默认/显式与转义等价、全部 C0、HTML/slash、U+2028/U+2029、emoji、NFC/NFD、ID 大小写和标题空格。
