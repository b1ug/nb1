# Homebrew 接入草案：真实发布输入尚缺，当前不可安装

这是一份本地审查材料，**没有可安装的 Formula、tap 或新 Release**。`nb1.rb.in` 不是 `.rb` 配方；`release-input.required.json` 的版本、URL、SHA、源码提交、最低macOS和组件范围均未填写。生成器遇到这些缺项就拒绝输出，不填虚构下载地址或校验和；测试中的合成 Formula 文本仅在内存渲染，CLI 负例使用会自动清理的临时fixture JSON，禁止将合成输入写成 `.rb` 配方。

当前材料只涉及打包设计及离线检查，没有运行brew、创建全局tap、安装、发布、调用USB或启动服务。tap仓库与Formula名称尚未最终批准，模板暂用 `Nb1`，不得把它当成已存在的安装命令。

## 1. 两条路线，先把差别讲清楚

| 路线 | 需要什么 | 优点与边界 |
| --- | --- | --- |
| 源码 Formula → Homebrew bottle | 一个真实、不可变的新源码版本/归档URL/SHA，确认对应commit与功能；声明Go/C工具链，实际brew构建/test/audit。 | 先在本机原生CGO编译；以后由Homebrew流程构建bottle供免Go安装。源码Formula并不需要先有五平台upstream二进制。当前模板只准备这一条的Mac草案。 |
| upstream预编译包 | 先把Actions临时目录制成按OS/arch可验证的tar.gz/zip，补稳定下载、链接依赖、OS下限、安装/升级/回退；再选择适合的Homebrew分发定义。 | 安装更快，但本轮artifact-only尚未交付这些包。不能把Actions临时地址、整个Actions zip或upstream压缩包直接称为Homebrew bottle。 |

Homebrew官方区分源码Formula与预编译cask；homebrew/core的收录政策要求源码或跨平台可移植产物，平台相关binary-only归cask。这个core收录规则不等于对所有自有tap作统一判定。本片建议手写源码Formula，避免依赖GoReleaser已经弃用的`brews`发布器，最终仍需Homebrew实际审查。[Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)、[Acceptable Formulae](https://docs.brew.sh/Acceptable-Formulae)、[GoReleaser旧Formula发布器](https://goreleaser.com/customization/publish/homebrew_formulas/)

## 2. Actions候选离可安装Release包还缺什么

现有工作流只生成 `nb1-candidate-SOURCE-OS-ARCH` Actions artifact：二进制、LICENSE、build.json、cli-help.json和SHA256SUMS；CGO1、公开SDK/GID版本、干净VCS和Web manifest有本机软件核对。它没有正式版本、tar/zip安装布局、稳定Release资产、签名、最低OS或跨平台干净安装结论。SHA256SUMS只证明包内当前字节一致，不防文件和checksum一起被改，不是独立来源认证。

建议下一片包装约定（**名字/目录尚是提案**）：

```text
nb1_VERSION_OS_ARCH/
  bin/nb1                 # Windows为nb1.exe
  build.json              # 精确source/tag、GOOS/ARCH/CGO、工具链、依赖、Web manifest
  share/doc/nb1/README.zh-CN.md
  share/licenses/nb1/LICENSE
  share/licenses/dependencies/...  # 来自真实依赖的notice/source归属
  SHA256SUMS
```

Unix用tar.gz保留可执行位；Windows用zip。归档只允许一个确定顶层目录、有限文件数/总字节、普通文件和受控目录，不放用户配置/图案库/锁文件/凭据，不自动展开到全局路径。拒绝绝对路径、`..`、符号/硬链接和大小写冲突；校验包外SHA及解包后的文件清单，再在新的私有临时目录检查当前 CLI 帮助。这样把解压/布局问题与USB验收分开。

最终Release还需真实tag与对应source、资产URL和单独下载校验值；旧v0.0.1保留。它早于本模板要求的 API v1 CLI 命令，不能把2023旧包当新功能的Homebrew来源。root与MCP是两个Go module；`nb1-mcp`若独立用`go install`发布，标签需 `adapters/mcp/vX.Y.Z`，root标签不会自动替代子module标签。[Go多模块标签](https://go.dev/doc/modules/managing-source)

## 3. 原生动态库与平台补证

| 平台 | 已有代码事实 | 下一包的实际检查 |
| --- | --- | --- |
| macOS arm64/Intel | GID原生后端链接CoreFoundation/IOKit；非外部hidapi/libusb；CGO必需。 | 每架构Mach-O与`otool -L`、最低部署版本、非开发机下载运行；签名/公证策略另定。模板最低macOS必须显式填，不把CI runner名称自动变成承诺。 |
| Linux amd64/arm64 | GID内嵌HIDAPI/libusb头，链接rt；现有规则无libusb/libudev pkg-config。 | ELF interpreter/NEEDED、glibc/内核和目标发行版；真实普通用户设备权限与USB另测。不要凭印象添加系统依赖，也不默认sudo/chmod广泛授权。 |
| Windows amd64 | GID链接Setupapi/hid，CGO/MinGW。 | PE/DLL imports、MinGW runtime是否随包需要、干净Windows无开发环境运行、路径空格/升级回退。Homebrew草案不覆盖原生Windows；Windows仍走自己的zip分发。 |
| Windows386 | 有WOW64软件和StoreV2 NTFS证据。 | 32位依赖/OS/USB仍独立；不在首轮增加发行架构。 |

这些检查不能由Linux容器或交叉编译替代。历史SDK b3/GID37矩阵7/11是平台中断下的分项证据，不是当前候选五平台全绿；MK3没有实体。外层nb1 MIT与依赖的完整notice范围要沿用现有GID来源审计，模板 `license "MIT"` 描述nb1项目声明，不替代内嵌依赖归属。审查工作区的 `gid/docs/dependency-sources.md` 有逐项记录；[GID源码](https://github.com/b1ug/gid)不能只按最外层MIT推断全部内嵌来源。

## 4. 当前源码Formula模板具体做什么

首个模板仅准备 **Mac原生arm64/Intel、nb1-only**。Linux Homebrew与MCP并包没有在模板里假称支持；它们仍是后续目标。`min_macos`由未来发布负责人明确选择，生成器仅识别sequoia/tahoe，不把任何一个设成默认或已批准下限。

- 声明Go build依赖，CGO=1、GOWORK=off、GOTOOLCHAIN=local；不用当前Makefile里的时间/主机/分支注入。
- 按官方当前Go流程，在fetch阶段下载并verify锁定module，install阶段通过`std_go_args`及`-mod=readonly`构建，不在构建阶段临时下载未知依赖。Homebrew当前Go可能与Actions固定Go不同，最终需记录实际编译器，不承诺两者逐字节相同。[Go Formula指引](https://docs.brew.sh/Language-Specific-Formulae)
- 源码归档没有`.git`，Go build info未必有VCS。模板把显式source_commit/版本写入现有config的GitCommit/GitSummary供version查看；这不是独立证明那个归档确实对应此commit，仍须发布前核对tag/archive/hash。doctor的Go VCS字段不得因此冒称存在。
- `test do`仅检查安装后二进制的 `--help`、`control --help`、`control run --help`，使用显式临时config并核对 API v1 命令存在，不枚举USB或连接daemon。此检查不编译Action、不预览Sequence，不验证HTTP或硬件行为；Formula的功能与原生安装验收仍需另做。[Formula功能测试](https://docs.brew.sh/Formula-Cookbook)
- 没有service、post_install、权限修改、自动自启、MCP宿主注册或用户库初始化。安装与启用daemon分别由用户明确选择。

这里仅做Python本地fixture、生成文本的Ruby语法检查，以及用当前二进制实际执行模板的help调用；没运行Homebrew DSL、brew style/audit/install/test/bottle，不能称模板已通过Homebrew验收。

## 5. 离线工具如何审查

```sh
python3 -B -m unittest discover -s packaging/homebrew -p 'test_*.py' -v
python3 packaging/homebrew/render_formula.py \
  --input packaging/homebrew/release-input.required.json --check-only
```

可选真实命令检查（需要Ruby和当前Mac二进制）：`NB1_HOMEBREW_TEST_BINARY=/absolute/path/nb1 python3 -B -m unittest discover -s packaging/homebrew -p 'test_*.py' -v`。它在内存中渲染fixture，用最小DSL适配执行模板原有的三个help调用，不运行brew、不写合成`.rb`。Mac候选Actions在构建后执行同一检查；默认五项离线校验不设置该变量时会跳过这项真实命令检查。

第二条当前必须非零退出，因为真实release值缺失。生成器还拒绝重复/未知/过大JSON、null/空值、错误SemVer、移动branch/latest/Actions URL、伪造格式的hash、缺省最低系统和未实现的MCP捆绑；synthetic fixture只能check-only，不能写`.rb`。若日后值齐全，显式`--out`只新建所指定的本地`.rb`，不覆盖现有文件、不创建tap目录、不联网或运行brew。

**离线验证只能检查形状和绑定关系，不能证明URL现在可下载、tag真指向指定commit、SHA来自可信发布者或软件可安装。** 这些事实必须从真实已选发布包取得；目前全都没有填写，因此本片没有产出任何可安装的`.rb`。`remote_url_and_source_identity_verified:false`会出现在工具状态中，避免把本地格式检查当成远端认证。

## 6. MCP选择、命名与后续切片

当前生成器只支持nb1-only，这是当前能力边界，不是用户永久放弃MCP。后续可选：

1. nb1和nb1-mcp分开包/配方，各自版本与Go下限清楚；适合只用CLI的用户。
2. 同套件显式含两binary，清单分别记录来源/module/版本；release仍要处理root与子module标签。MCP不需要USB库，也不该为了它申请设备权限。

建议下一步依次：批准包布局和版本/组件范围→从固定源码生成本地有界tar/zip并验证→真实发布输入齐备后渲染本地Formula→获准后在干净Mac arm64/Intel做brew style/audit/build/test/upgrade/uninstall→再建立tap远端与Homebrew瓶装CI。Linux/Windows安装与实灯支持逐项补证，不用tap计划替代它们。

`taps`可由自己的repo提供，GitHub命名通常以homebrew-开头；bottle需要Homebrew安装后打瓶和对应DSL。当前没运行任何这些命令。[自建Tap](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)、[Bottles](https://docs.brew.sh/Bottles)
