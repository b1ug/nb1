# Web 组件与构建边界

[English](web-components.md)

nb1 的三个 Web 入口分别交付，按希望由谁持有设备来选择。

| 名称 | 源码 | 用途与设备持有者 |
| --- | --- | --- |
| nb1 Documentation · 文档站 | [nb1-docs](https://github.com/b1ug/nb1-docs) | 静态英中教程，不申请 USB 权限，不控制灯。 |
| nb1 Embedded Workbench · 内嵌工作台 | [nb1-workbench](https://github.com/b1ug/nb1-workbench) | 内嵌在 nb1，由 `/demo/` 提供页面；调用同源 HTTP API，nb1 daemon 持有 USB。 |
| nb1 Browser Tools · 浏览器工具 | [nb1-browser-tools](https://github.com/b1ug/nb1-browser-tools) | 独立 WebHID 工作台开发预览，以及实验性 Chrome 提醒扩展；网页或扩展直接持有 USB。 |

内嵌工作台和 HTTP API 属于同一个 `nb1 server` 进程，不需要再安装一个 daemon。
使用直连 WebHID 工具前，需要明确让原生 nb1 或其他浏览器控制者释放设备；浏览器
不会参与原生 nb1 的设备锁。

实验扩展不是 Chrome Web Store 正式发行，关闭弹窗后的真实后台提醒验收与浏览器软件
测试分开记录。内部规划和研究页面不属于公开产品入口。

## Go module、源码检出与内嵌资源

GID、blink1-go、colorkit 等 Go module 通过 `go.mod` 提供 Go 代码。前端是独立源码
项目，不作为 Go module 依赖。

前端构建记录源码仓库、精确 commit、干净工作树状态和逐文件哈希。nb1 只导入审过的
`dist` 闭集，在 `internal/webui/manifest.lock.json` 固定提交和 manifest 哈希，再由
`go:embed` 把资源打进程序。公开资源不包含内部录像验收、研究笔记或本机路径；原始
批准形象、模型几何与许可证保留。

普通 nb1 Go 构建直接使用已提交资源，不运行 Node、不下载前端仓库，也不要求
`git clone --recursive`。GitHub 源码归档与 Homebrew 源码构建因此自带所需资源。

Git submodule 可以固定开发者检出的源码，但不会构建或校验 `dist`、收集许可证，
也不能替代内嵌 manifest。本项目以显式资源导入作为发行边界。

## 更新内嵌工作台

按工作台仓库说明构建并审阅干净源码，确认完整 commit 与 `dist/manifest.json` 的
SHA-256，再从 nb1 目录执行：

```sh
python3 tools/import-web-dist.py ../nb1-workbench/dist \
  --source-commit WORKBENCH_COMMIT \
  --manifest-sha256 MANIFEST_SHA256 --check
```

将两个占位符替换为审过的完整值。`--check` 只校验输入，不写入；去掉该参数执行
导入，审阅资源和 lock 的改动，再运行：

```sh
python3 tools/test_import_web_dist.py
GOWORK=off go test -mod=readonly ./internal/webui ./internal/httpapi
```

前端源码和 nb1 宿主分别提交、分别提 PR，通过精确 bundle pin 关联。文档链接与
浏览器工具有自己的发布检查；一个入口发布，不代表另一个已经重建、安装或实机验收。
