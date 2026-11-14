# 容器入口与权限边界

本机控制硬件优先使用原生 CLI 或用户级 daemon。容器入口用于 Linux 分发验证，目前不代表容器内 USB、热插拔或后台服务已经验收。

`make build_docker` 在固定 digest 的 Go 1.27.3 / Debian bookworm 构建阶段编译 Linux CGO，不再复制开发机上交叉编译的二进制。最终镜像只有运行环境和 CLI，使用 UID/GID 65532，直接执行 `/usr/local/bin/nb1`，默认 `--help`，不操作 USB。更新基础镜像时需重新核对 digest 并运行同一验证。

`make run_docker` 使用只读根文件系统、删除全部 capabilities、禁止通过执行文件获得新权限，仅给 `/tmp` 和私有用户目录提供有界临时空间。临时目录随容器退出消失。镜像的普通用户是默认配置；这些额外运行限制由启动参数提供，不能仅凭 Dockerfile 声称所有启动方式都采用了它们。Docker 的 [USER](https://docs.docker.com/reference/dockerfile/#user) 与 [容器运行参数](https://docs.docker.com/engine/containers/run/) 定义了这些配置。

`validation/nonroot-container` 分支的 CI 在原生 Linux amd64、arm64 上实际构建镜像，再在无网络、无宿主设备挂载的条件下验证帮助入口、UID/GID、capabilities、NoNewPrivs、只读根目录、私有可写目录与动态库。结果单独记录，不能代替 USB 或 daemon 生命周期验证。

后续如需 Linux 容器操作硬件，应先确认选定设备节点和宿主权限，再仅映射需要的节点、匹配普通用户或特定设备组。不要使用 `--privileged`、挂载整个 `/dev`、放宽所有设备权限，也不要挂载 Docker socket。USB 节点重建、设备租约与宿主其他进程的协调仍需专门测试。

当前 HTTP API 只监听 loopback，普通 `-p` 映射不能把容器 loopback 变成宿主可访问服务。若评估 Linux host network，需明确它改变网络隔离；本入口不默认启用、不改变 API 的 Host/Origin 检查。macOS、Windows Docker VM 的 USB 转发与 host network 不在本次支持证据内。容器内也不负责安装宿主 launchd、systemd 或 Windows 任务。

本改动不发布镜像、不登录 registry、不安装 Docker，不需要本机额外权限。是否适合生产使用，仍需结合目标系统、设备权限和实际 USB 测试判断。
