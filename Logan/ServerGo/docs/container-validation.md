# 隔离容器验收

本入口只验收合成配置下的镜像与容器基础能力，不读取生产配置，不启动或停止原 Java，不执行迁移、OSS 写入或生产切流。业务、数据库和维护开关关闭；`/readyz` 必须精确返回 503，不能作为兼容或发布成功。

## 环境与执行

准备可丢弃的 Linux Docker Engine，显式指定专用 Unix socket。脚本拒绝默认 socket、非 Logan 专用路径和隐式 context；授权开关不能证明该引擎确实隔离，执行者仍需核对其来源。不要使用生产 Docker Engine。证据目录必须新建且位于仓库外。macOS VM 需要在相同绝对路径共享测试目录（可写）；构建上下文由 Docker 客户端发送，演练夹具复制到测试目录，不要求共享整个 checkout。

从 ServerGo 执行，替换路径为实际独立运行环境：

```sh
python3 script/verify-containers.py \
  --docker /absolute/path/docker \
  --host unix:///private/tmp/logan-runtime/docker.sock \
  --allow-isolated-runtime \
  --out /private/tmp/logan-container-evidence-unique \
  --work-dir /private/tmp/logan-bind-fixture-unique \
  --keep-image
```

脚本直接构建本目录 Dockerfile，使用随机镜像标签、资源名及归属标签；不推送、不 prune、不触碰其他项目。实际读取 UID、只读挂载、capability、NoNewPrivs、cgroup v2 内存/CPU/PID 限额及 tmpfs 配额。Linux 命名卷中的合成文件为 UID 10001、0400；可选 bind 文件仅证明只读挂载，不证明真实宿主私钥所有权。

若隔离 VM 的构建网络需要代理，可显式传 `--build-proxy http://VM可达地址:端口`，只通过 Docker 的 HTTP_PROXY/HTTPS_PROXY 构建参数传入，不写入最终镜像环境。该选项拒绝账号、密码、查询参数和路径；不要提交敏感代理配置。Daemon 拉取基础镜像的代理需在独立运行环境另行设置，构建参数不能替代它。

基础服务启动后核对存活、指标和 readiness；SIGTERM 要求 90 秒内退出 0。重启前保留 tmpfs 标记，重启后确认消失，再次退出。没有运行活跃业务，因此容器内在途业务排空、任务恢复、OOM、CPU 压力和空间耗尽仍需专门验证。镜像静态链接检查不代替漏洞扫描或供应链审核。

退出码 0 仅表示所列基础检查通过；1 表示失败；2 表示运行环境不可用；130 表示中断。摘要和各命令日志保存在 `--out`。资源清理核对归属标签，清理失败不得报成功。默认回收全部自建资源；`--keep-image` 仅保留摘要列出的本地镜像用于下一步演练，演练结束需核对标签再删除该镜像。

## 隔离往返演练

先在同一独立引擎准备 Python fixture 镜像，再使用上一摘要中的本地 Go 镜像标签：

```sh
python3 script/rehearse-cutover.py \
  --docker /absolute/path/docker \
  --host unix:///private/tmp/logan-runtime/docker.sock \
  --allow-isolated-runtime \
  --work-dir /private/tmp/logan-rehearsal-work-unique \
  --go-image local-verifier-image:local \
  --fixture-image python:3.12-alpine \
  --report /private/tmp/logan-rehearsal-evidence-unique.md
```

演练创建内部网络和独立网关，通过容器内部 HTTP 探针访问，不发布宿主端口。Java 端是明确标注的 **Python 模拟器**，Go 端使用实际构建镜像；它验证关闭入口、停止旧端、启动新端以及反向回滚顺序和路径改写。Go 阶段只开放诊断 `/ping`，业务持续 503。模拟器不证明原 Java 行为，合成网关不证明 nginx 配置和 reload；本演练也不证明真实共享数据库/OSS 的兼容回滚或单写入者数据安全。

生产切换仍必须满足[发布手册](release-runbook.md)全部门槛。最新实测与限制见[进度记录](progress.md)。

活跃请求/Native/Web任务的排空、真实数据库租约接管，以及实际Tomcat/Go读接口往返使用后续[故障与兼容演练](recovery-rehearsal.md)，不要用本页基础容器脚本代替。

## 脚本回归

```sh
python3 -m unittest discover -s tests/tools -v
python3 -m unittest discover -s tests/rehearsal -v
```

这些回归覆盖危险端点拒绝、结果判定、优化模式、清理归属和故障处理，不能代替真实 Docker 执行。
