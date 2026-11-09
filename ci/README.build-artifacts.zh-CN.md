# 编译候选：只生成 Actions 验证产物

`build.yml` 保留 master push/PR，并提供手动入口；没有 tag、Release、tap、镜像推送或写权限。它原生构建现有 runtime 矩阵的五个目标：Linux amd64/arm64、macOS arm64/amd64、Windows amd64。只构建 nb1，MCP 的独立模块测试继续在 runtime.yml；正式套件是否捆绑 MCP 尚未决定。

构建明确使用 CGO=1、GOWORK=off、只读公开模块版本和固定 Go1.27.3。软件检查核对二进制自身的 GOOS/GOARCH/CGO/VCS 身份、干净源码、SDK/GID 的精确版本及 sum，再运行固定的高级 Pulse 纯预览。预览显式使用临时配置、无效 endpoint 和一个不能充当锁目录的普通文件；不读取用户配置，不连接 daemon，不枚举/打开 USB，不建任务。它不会运行旧 `ls -a`。

每个 `nb1-candidate-SOURCE-OS-ARCH` Actions artifact 包含二进制、LICENSE、build.json、preview.json、SHA256SUMS。build.json 保留工具链、公开模块和嵌入 Web manifest 绑定，标明 `release:false`。脚本拒绝已有输出目录，失败时 workflow 不上传成功产物。`verify` 对缺失、额外文件、符号链接和 checksum 不符返回非零。

这不是正式安装包或 Release：Actions 归档并不承担可执行模式、长期下载 URL、签名或升级回退契约。SHA256SUMS 只检查包内文件是否一致；同时改文件和校验和不能由它识别，它不是独立来源认证或签名。以后生成 tar.gz/zip 与 tap 时另做干净解压/安装验证。软件构建/预览也不证明最低OS、旧系统、实体USB、MK3或所有Linux/Windows设备行为。

本地只读软件预览（需已构建二进制和Python3）：

```sh
python3 ci/check-build-artifact.py preview --binary /absolute/path/nb1
python3 -B -m unittest discover -s ci -p test_build_artifact.py -v
```

`make preview` 只调用上述隔离配置的纯预览，不额外调用会读取用户配置的版本命令。产物构建身份由 stage 读取二进制自身的 Go build info 核对。现有完整 tests/race/vet、Go1.18 CLI源码下限、SDK/GID Go1.13下限、MCP Go1.25下限、服务和容器验证均未被这个产物工作流替代。
