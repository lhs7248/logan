# Go 独立部署候选

镜像和容器的可重复隔离验收见[容器验收说明](../docs/container-validation.md)，每轮实测范围见[进度记录](../docs/progress.md)。

此配置仅新增独立的 `logan-go-candidate` Compose 项目，构建上下文明确指向 ServerGo；不替换现有 Java、nginx、Compose、API 或数据库，不创建数据库、不运行迁移。当前交付不执行生产启动或流量切换。Docker 健康仅证明 `/livez` 返回成功；`/readyz` 必须继续返回 503，不能把健康标记当作生产放量依据。

需要 Docker Engine 和 Docker Compose **2.30.0+**（使用 `env_file.format: raw`）。基础镜像沿用工程固定的 Go 1.27.0 和 Alpine 3.22.1；正式部署前须核对补丁、漏洞扫描和组织镜像策略。CGO 关闭并使用 `stdjson`。2026-10-16 已在隔离 Linux arm64 Docker Engine 实际构建和运行，amd64 仍仅交叉编译；实测不代表正式发布批准，范围和证据见进度记录。

## 私密配置

从 ServerGo 目录操作。把 `deploy/go.env.example` 复制到**仓库外**的私密文件，设置 0600 权限，在编辑器中填写；它不是可直接验收的业务配置。设置下列三个变量为仓库外实际绝对路径（不要把凭据放在命令行或 shell 历史）：

```sh
export LOGAN_GO_ENV_FILE=/absolute/private/path/go.env
export LOGAN_GO_SECRET_DIR=/absolute/private/path/keys
export LOGAN_GO_IMAGE=logan-servergo:candidate
```

这些路径只是占位说明，必须替换。`LOGAN_GO_SECRET_DIR` 必须事先存在，Compose 不会自动创建。Web 启用时其中必须有 `web-rsa.pem`，目录只读挂载至 `/run/logan-secrets`。Linux Docker 宿主应让目录可由 UID/GID 10001 遍历、私钥可由该用户读取，例如目录 0700、文件 0400、属主 10001:10001；Docker Desktop 文件共享须在容器内实际验证。只读挂载不会修复宿主权限。不要使用样例或测试私钥。禁用 Web 时可挂载一个已存在的空私密目录。

程序的 DSN、AES 和 OSS 凭据只支持环境变量，不能编造 `_FILE` 变量。只有 Web RSA 支持文件路径。raw env_file 的值原样传入，不写引号、不使用 `${...}` 展开；保护私密文件和 Docker 管理权限，容器检查和完整 `compose config` 输出都可能泄露环境变量。只运行 `config --quiet` 校验，证据不得收录凭据。Docker 构建排除 `.env*`、私钥、captures 和常见生成目录；私密资料仍应始终存放仓库外。

模板全部业务开关默认 false，仅适合基础探针。在隔离环境用真实依赖验证业务前，必须显式设置 `LOGAN_DB_ENABLED=true`、`LOGAN_NATIVE_ENABLED=true`、`LOGAN_WEB_ENABLED=true`、`LOGAN_NATIVE_QUERY_ENABLED=true`，填写真实隔离 MySQL DSN、与客户端匹配的 AES key/IV、OSS endpoint/region/bucket/prefix/凭据，并提供 RSA 私钥。表和显式迁移必须由独立授权流程提前确认；启动不会替你迁移。`LOGAN_ENV=production` 只会增加配置约束，不代表通过生产验收。501、禁用功能和 readiness 503 均不能算兼容成功。

维护默认关闭且由 Compose 的 `LOGAN_GO_ENABLE_MAINTENANCE` 控制，私密 env 中的同名程序开关会被覆盖。**不要在生产数据上开启候选维护进程**：开启即绑定数据库 OSS 命名空间并立即清理。只有在迁移、命名空间、保留策略、备份与 Java 清理停用都获批后，才可在单独的授权部署中显式设为 true；详见[运维边界](../docs/operations.md)。

## 预检和隔离运行

以下命令只适用于已授权的本机或隔离环境，当前文档不授权生产运行：

```sh
docker compose -f deploy/compose.go.yaml config --quiet
docker compose -f deploy/compose.go.yaml build logan-go
docker compose -f deploy/compose.go.yaml run --rm --no-deps --entrypoint /bin/sh logan-go -ec 'id; test -w /var/lib/logan-tmp; test ! -w /; test -r /run/logan-secrets/web-rsa.pem'
docker compose -f deploy/compose.go.yaml up -d logan-go
docker compose -f deploy/compose.go.yaml ps
curl -i http://127.0.0.1:18080/livez
curl -i http://127.0.0.1:18080/readyz
```

权限预检的私钥读取断言用于 Web 已启用场景。确认 uid/gid 为 10001，临时目录可写、根目录不可写，再检查容器内临时目录权限为 0700。每个容器临时文件使用独立的 512MiB tmpfs，计入 1GiB 内存限制；上传/解析并发和明文预算需结合容量压测调整，磁盘满、OOM 和重启会丢失临时文件。SIGKILL 恢复需要真实依赖验证，不能凭本配置宣称完成。

默认只绑定宿主 `127.0.0.1:18080`，容器内仍监听 8080。`LOGAN_GO_BIND_ADDRESS` 和 `LOGAN_GO_PORT` 仅用于明确批准的访问调整。`/metrics` 在候选配置中开启；`logan-go-ops` 为同项目监控提供容器网络，允许访问外部 MySQL/OSS，因此不是 internal 网络。宿主防火墙和 Docker 网络成员资格必须限制访问；不把指标公开到公网。其他宿主上的监控需走授权的私有网络方案。

限制为 1 CPU、1GiB 内存、128 PID、4096 文件描述符和最多 3×10MiB json-file 日志；它们是初始候选预算，不是容量承诺。容器非 root，根文件系统只读、移除全部 capability、禁止提权。`init: true` 转发 SIGTERM 并回收子进程；`stop_grace_period: 90s` 大于默认 `LOGAN_EXIT_WAIT=60s`，调整退出预算时必须一起延长平台宽限。restart 默认关闭便于暴露失败。

停止候选仅作用于此 Compose 项目：

```sh
docker compose -f deploy/compose.go.yaml stop -t 90 logan-go
```

流量切换、Java 保留和回滚验收使用上层发布步骤；此配置本身不改变入口、不停止 Java、不删除数据或卷。回滚应先关闭所有业务入口、排空并停止 Go 写入/解析/清理，确认恢复条件后启动已验证 Java，再恢复入口；数据库和 OSS 的兼容与副作用需要另外核对，容器停止不能撤销已发生的业务写入。

## Candidate resource tuning

The template sets Native concurrency, Native Query workers and Web workers to 1 without changing per-request protocol or size budgets. Native Query can need up to 32MiB compressed + 128MiB plaintext + 256MiB staged = 416MiB per task; other requests and Web still compete for the 512MiB tmpfs. GOMAXPROCS=1 matches the CPU limit. GOMEMLIMIT=384MiB is a soft Go heap limit, not an RSS guarantee; tmpfs is charged against the 1GiB container memory limit. Verify mixed-load capacity and allocate memory/tmpfs before raising any worker count or request budget. These candidate limits do not guarantee success for every valid request combination. Retention settings are explicit review values even while maintenance is disabled.

The external key directory and private key must be accessible to UID/GID 10001 as described above. Do not use chmod 0777 to work around ownership errors.

## Immutable release overlay

`compose.go.yaml` builds an isolated candidate for evaluation. `compose.release.yaml` is an inert overlay for a future, separately approved release: it removes the build definition with `!reset`, requires `LOGAN_GO_RELEASE_IMAGE`, always pulls that image, and uses `unless-stopped` restart behavior. A manually stopped service stays stopped. Compose 2.30.0+ is required for this combined configuration as well.

The operator must verify that `LOGAN_GO_RELEASE_IMAGE` is the approved immutable `repository@sha256:<64 hexadecimal characters>` reference and record the verified digest and provenance; Compose interpolation checks presence, not digest validity or approval. After all release gates and separate authorization, the release command shape is:

```sh
docker compose -f deploy/compose.go.yaml -f deploy/compose.release.yaml config --quiet
docker compose -f deploy/compose.go.yaml -f deploy/compose.release.yaml up -d --no-build --pull always logan-go
```

These commands are documentation for an approved future release, not production approval. No image pull or release is performed by adding this overlay. `/readyz` remains 503 and continues to block production acceptance. Use the same two Compose files for release status and stop operations so the effective model stays consistent. The candidate-only build commands above do not establish an immutable release artifact.
