# Logan Website

Logan 网站为开发者提供了 App 上报日志与 Web 上报日志的浏览、检索等功能，帮助开发者以直观的方式查看日志。

关于 Logan 想知道更多？[Logan：美团点评的开源移动端基础日志库](https://tech.meituan.com/2018/10/11/logan-open-source.html)

## 启动与访问

LoganSite 是 React 前端，查看真实日志还需要 Java Server 和 MySQL。本文说明现有 [Compose 配置](../docker-compose.yaml) 的 Java 部署链路；其他服务的运行方式以各自 README 为准。

### 方式一：启动完整服务（Docker Compose）

前置条件：安装并启动 Docker（macOS 可使用 Docker Desktop），确保 `docker compose version` 可用，并能访问镜像及依赖下载源。本机不需要另装 Node、Java 或 MySQL。

从 Git 仓库根目录（包含 `Logan/` 和 `Example/` 的目录）执行：

```bash
cd Logan
# 当前目录应包含 docker-compose.yaml、Server/ 和 LoganSite/
docker compose up -d db
docker compose logs --tail=50 db
```

确认 MySQL 已完成初始化并出现 `ready for connections`，再初始化表结构：

```bash
docker compose run --rm db-migrate
```

迁移成功后启动前后端。如果迁移报数据库连接失败，等待数据库就绪后重试；其他迁移错误需先排查。

```bash
docker compose up -d --build backend frontend
docker compose ps
```

使用这里的 `docker-compose.yaml`，不要使用仓库外层工作区中另一份 `docker-compose.yml`。此命令不启动可选的 phpMyAdmin。

| 入口 | 地址 |
| --- | --- |
| Web 日志列表 | http://localhost:3000/#/web-list |
| Native 日志列表 | http://localhost:3000/#/native-list |
| Java API 基础地址 | http://localhost:8888/logan-web |

页面使用 Hash 路由，地址中的 `/#/` 不能省略。点击列表记录进入详情页；Web 详情路由为 `/#/web-detail?tasks=实际任务ID`。没有客户端上报数据时，列表为空。

Docker 部署中浏览器通过同源 `/logan/` 请求，由 [Nginx](./nginx.conf) 转发到 Java 后端，不需要设置开发用的 API 地址。

查看启动或接口错误：

```bash
docker compose logs --tail=100 backend frontend
docker compose logs --tail=100 db
```

停止服务（保留数据库卷）：

```bash
docker compose down
```

现有 Compose 使用示例数据库凭据并映射数据库端口，适合本地验证；部署到共享网络前需调整凭据、端口和访问控制。2026-10-27 已在 Apple Silicon + Colima 环境完成启动验证，但原始构建流程需要下述兼容处理，不能视为原配置直接构建通过。

### Apple Silicon 实测与兼容启动（2026-10-27）

本次实测：数据库迁移成功，Java 接口和 Nginx 同源接口均返回 HTTP 200；浏览器能打开 `http://localhost:3000/#/web-list`。先验证空库列表，再完成下述修复和合成日志上报、首次详情加载验证。日志下载与真实客户端 SDK 上报未验证。

原构建存在两个环境兼容问题：

- Java 构建镜像是 amd64，模拟运行时入口脚本复制 Maven 依赖出现 `bash terminated by signal 11`。临时 Dockerfile 将正式编译步骤改为直接执行 `mvn verify` 后构建成功，未修改仓库 Dockerfile。
- 前端默认 ARM 构建在 `node-sass` 安装时回退到本地编译，因缺少 Python 失败。使用 Buildx 构建 amd64 镜像成功；首次模拟构建较慢。

本机已经安装 Docker CLI、Compose、Buildx 和 Colima。若其他 Apple Silicon 机器没有容器环境，可使用 Homebrew 安装并启动：

```bash
brew install colima docker docker-compose docker-buildx
colima start --cpu 2 --memory 4 --disk 30
```

按 Homebrew 的安装提示配置 Docker 插件发现路径，确认 `docker compose version` 和 `docker buildx version` 均可用。若已有 Docker Desktop，无需再安装 Colima。

在包含 `docker-compose.yaml` 的目录执行以下兼容构建（先按上文启动数据库并完成迁移；需要 Python 3）：

```bash
# 生成临时 Dockerfile，保留仓库原文件
python3 - <<'PYCODE'
from pathlib import Path
source = Path('Server/Dockerfile').read_text()
source = source.replace(
    'RUN ["/usr/local/bin/mvn-entrypoint.sh","mvn","verify"]',
    'RUN ["mvn","verify"]',
)
Path('/tmp/logan-backend-verify.Dockerfile').write_text(source)
PYCODE

# 本次后端实测使用经典构建器；前端使用 Buildx 指定 amd64
DOCKER_BUILDKIT=0 docker build -t logan-backend -f /tmp/logan-backend-verify.Dockerfile Server
docker buildx build --platform linux/amd64 --load -t logan-frontend LoganSite
docker compose up -d --no-build backend frontend
```

以上镜像名对应当前目录默认 Compose 项目名 `logan`；自定义项目名时需同步调整标签。已存在本次构建镜像时，直接运行最后一条命令即可，不要用 `--build` 重新触发原始构建。

连通性检查（绕过本机代理）：

```bash
curl --noproxy '*' -I http://localhost:3000/
curl --noproxy '*' http://localhost:3000/logan/web/latest.json
docker compose ps
```

2026-10-27 后续修复：`WebLogTaskMapper.maxId` 使用 `COALESCE(MAX(id), 0)`，空库返回 0，避免 SQL NULL 映射到 `long` 的异常。真实 MySQL 临时表回归已验证空表与非空表；本地服务已部署修复。

首次详情加载修复（2026-10-27）：Web 解析后的详情改为在事务内同步分批写入，提交成功后才继续标记已解析并查询，避免后台队列尚未落库导致 `not details for`。批次大小仍为 200，写入失败会回滚并向调用方抛出。

通过真实 `/logan/web/upload.json` 为新设备 `codex-first-open-20261027` 上报 3 条合成日志。首次进入前，数据库确认任务 2 状态为 0、详情数为 0；从列表点击一次“日志详情”，3 条日志及单条详情立即正常展示，全程无刷新、无提前请求详情。进入后状态为 1、详情数为 3，后端没有新增错误。服务保持运行，测试内容均标记 `[TEST ONLY]`。

#### 修复验收结果

| 检查 | 结果 |
| --- | --- |
| 空库及非空表最大 ID 查询 | 真实 MySQL 临时表回归通过，空表返回 0，非空表返回最大 ID |
| Web 详情同步写入 | 201 条按 200 + 1 分批，提交后返回；后续批次失败触发回滚并抛错；空输入不写入 |
| 针对性回归 | JUnitCore 共 4 项通过；由于容器无法下载 Surefire provider，未用 Maven Surefire 执行这 4 项 |
| Java 控制器兼容基线 | 13 项，0 失败、0 错误、3 跳过 |
| Go 兼容与构建检查 | `go test -tags stdjson ./...`、`go vet -tags stdjson ./...`、竞态测试和 `./build.sh` 通过 |
| 浏览器验证 | 新上报任务从列表首次进入即显示 3 条日志，单条详情可打开，无刷新 |

测试运行方式与修复边界见 [服务端回归说明](../Server/src/test/README.md)。此次修改仅解决空库查询和首次详情可见性，未处理多个请求同时解析同一任务的去重问题。源码兼容锁仅更新经过核对的 Mapper XML 和 Web 详情服务指纹，未修改控制器契约或 golden 响应。WAR 打包使用跳过测试选项，打包成功不替代上述独立回归结果。

### 方式二：已有后端，只启动前端开发服务

原项目记录的环境为 Node `^10.15.3`、npm `^6.12.0` 或 Yarn `^1.15.2`；Dockerfile 使用 Node 14。项目依赖旧版 `node-sass` 和 Webpack，不保证在 Node 24 等较新环境直接安装成功。使用隔离的兼容环境，依赖升级需另行验证。

从 Git 仓库根目录进入前端目录：

```bash
cd Logan/LoganSite
```

在该目录创建或编辑 `.env.development`，保留其他已有配置。如果 Java 后端由上述 Compose 启动，设置：

```dotenv
API_BASE_URL=http://localhost:8888/logan-web
```

后端在其他机器时替换主机和端口，并保留实际部署的上下文路径。然后执行：

```bash
npm install
npm start
```

也可使用 `yarn install` 和 `yarn start`。打开 http://localhost:3000/#/web-list 。修改环境变量后需重启开发服务。不要同时让 Docker 前端与开发服务占用 3000 端口。

开发方式会跨域访问后端；当前 Java CORS 配置允许 `http://localhost:3000`，请使用该地址打开页面。更换页面主机或端口时，需要同步调整后端允许的 Origin。

### 构建静态页面

在前端目录安装依赖后执行：

```bash
npm run build
```

产物位于 `build/`。默认同源部署需像上述 Nginx 配置一样代理 `/logan/`。如需指定独立后端，可在 `.env.production` 中设置 `API_BASE_URL` 后重新构建，无需修改 `src/common/api.js`；该地址在构建时写入产物，部署后修改环境变量不会改变已构建的页面。

### 页面截图

![Index.html](./docs/list-page.png)
![Index.html](./docs/detail-page.png)

## 主要特性
Logan 网站为来自客户端（Android、iOS以及Web前端）的上报日志提供了基础的浏览检索功能，具体包括：
- 上报任务检索：根据日志上报时间、设备编号或用户标志等条件查找日志上报任务。
- 上报任务概览：概览单一任务中记录的所有日志条目。
- 日志类型筛选：根据客户端记录的日志类型对单一任务中记录的所有日志进行过滤筛选。
- 日志关键字筛选：根据关键字对单一任务中记录的所有日志条目进行筛选。
- 单条日志快速定位：提供MiniMap，可根据日志类型和记录时间快速定位到单条日志。
- 单条日志详情展示：展示某条日志原始记录。

### ⭐️**MiniMap控件**

由于端上存储的日志类型较多，数量较大，采取传统分页表格显示配合筛选条件查看的方式效率依旧很低。因此，Logan 特别在日志详情页设计了一种 MiniMap 控件。该控件具有以下特性：
- 直观地展示日志类型：用不同的颜色绘制刻度，代表不同类型的日志
- 便捷地查看日志概要：鼠标覆盖到任意刻度，会显示日志类型和日志记录时间信息
- 快速定位到目标日志：点击任意位置，能快速查看该条日志详情

![Minimap](./docs/minimap.png)



## 项目结构
```
LOGAN_SITE
├── src
│   ├── app.js
│   ├── app.scss
│   ├── app.test.js
│   ├── index.js
│   ├── store.js
│   ├── common          // common components and global functions
│   │   ├── components
│   │   ├── adapter.js
│   │   ├── api.js
│   │   ├── color.js
│   │   ├── time.js
│   │   └── util.js
│   ├── consts          // constants will use in this project
│   └── views
│       ├── components  // page level components to reuse
│       │   ├── list-page
│       │   │   ├── components  // block level components in page
│       │   │   ├── index.js
│       │   │   └── style.scss
│       │   └── log-detail-page
│       │       ├── components  // block level components in page
│       │       ├── index.js
│       │       └── style.scss
│       ├── native-list
│       │   ├── index.js
│       │   └── redux
│       ├── native-log-detail
│       │   ├── index.js
│       │   └── redux
│       ├── web-detail
│       │   ├── index.js
│       │   └── redux
│       └── web-list
│           ├── index.js
│           └── redux
├── config    // webpack configs
├── public
├── scripts   // npm scripts
├── package.json
├── README.md
└── yarn.lock
```

## License
Logan项目采用MIT许可协议 - 详细内容请查看[LICENSE](https://github.com/Meituan-Dianping/Logan/blob/master/LICENSE)
