# 真实依赖集成与故障恢复验证

## 入口与证据

从 ServerGo 执行 `python3 script/integration.py --suite all`，也可指定 mysql 或 oss。测试只在设置 `LOGAN_IT_ALLOW_WRITES=1` 后执行。缺依赖时报告 blocked、退出 2；执行失败退出 1；所有选中套件通过才退出 0。没有测试、跳过测试或编译成功都不视为运行通过。JSON 摘要和经过凭据脱敏的测试日志写入权限受限的临时目录，末尾打印证据位置。

普通 `make test/race/vet/build` 不加载 integration 标签，避免自动触及数据库/云存储。真实集成明确使用 `-count=1`，不把 Go 缓存结果当作本次实测。只验证编译时使用：

```sh
go test -c -tags 'stdjson integration' -o /tmp/logan-mysql-integration.test ./tests/integration/mysql
go test -c -tags 'stdjson integration' -o /tmp/logan-oss-integration.test ./tests/integration/oss
python3 -m unittest discover -s tests/tools -v
```

## MySQL

设置 `LOGAN_IT_MYSQL_DSN`，其数据库名必须以 `logan_it_` 开头，指向专用测试控制库，账号需有 CREATE/DROP DATABASE 权限。测试会创建新的随机 `logan_it_<32位十六进制>` 数据库，加载原 Java README 四表和迁移 001/002，只删除本次创建的库，不修改控制库或原业务库。禁止填入生产 DSN；名称校验不能代替真实环境隔离。测试账号、网络和主机必须隔离。

```sh
# 从本机私有配置注入 LOGAN_IT_MYSQL_DSN，不把真实凭据提交到仓库。
export LOGAN_IT_ALLOW_WRITES=1
python3 script/integration.py --suite mysql
```

覆盖上传 intent/任务事务、引用保护、保留期删除、持久化对象回收、竞争维护租约与旧 fence 拒绝；真实子进程领取对象后被 SIGKILL，由另一个进程接管；Native 解析进程到达解析屏障后 SIGKILL，等待真实 30 秒数据库租约到期，替代进程完成单次发布，再次投递保持详情 ID。还有解析发布失败后的事务回滚与重试。

测试不是两套完整 HTTP 服务部署：故障子进程调用真实 repository 和真实 MySQL，解析回调使用合成 staged 数据，OSS 删除在数据库套件中只确认 outbox 状态，不执行云端删除。因此不能把它扩展为完整 SDK→HTTP→OSS→MySQL 链路或所有网络分区场景已验收。

可选的 `tests/integration/compose.yaml` 仅提供隔离 MySQL 基础设施，使用显式固定版本镜像、回环端口和临时数据目录，不合并或修改原部署。需先设置 `LOGAN_IT_MYSQL_IMAGE`（例如经核对的 `mysql:8.4.9`）与测试专用 `LOGAN_IT_MYSQL_ROOT_PASSWORD`，再执行 Docker Compose。等待健康后配置控制库 DSN；完成后只关闭此测试项目。当前机器无 Docker，Compose 尚未执行验证。版本矩阵需要分别运行，不能把 8.4 的结果当作 5.7/8.0 的证明。

## OSS

配置下面的变量，桶必须是专用测试桶，测试账号权限限制在测试前缀。不要把真实值写入脚本或命令历史。

- LOGAN_IT_OSS_PRIVATE_BUCKET=1：确认隔离私有桶。
- LOGAN_IT_OSS_MODE：aliyun 或 compatible。
- LOGAN_IT_OSS_ENDPOINT、LOGAN_IT_OSS_REGION、LOGAN_IT_OSS_BUCKET。
- LOGAN_IT_OSS_ACCESS_KEY_ID、LOGAN_IT_OSS_ACCESS_KEY_SECRET，可选 SESSION_TOKEN。
- LOGAN_IT_OSS_PREFIX：以 `logan-it/` 开头、以 `/` 结尾；套件再追加随机子前缀。

aliyun 模式要求 HTTPS 与 aliyuncs.com 域名；自定义域名或本机替身只能用 compatible，结果不得标为真实阿里云实测。测试只清理预先登记的两个精确对象键，不列举或删除其他对象。

```sh
export LOGAN_IT_ALLOW_WRITES=1
python3 script/integration.py --suite oss
```

覆盖二进制 PUT/GET 字节一致、签名下载且禁止跟随重定向、取消后的正文关闭、预取消 PUT、重复删除和缺失对象读取失败。不代表 IAM 权限变更、真实网络分区或上传中断后最终一致性已经验证。异常退出可能留下本次随机前缀，需结合测试账号的受限生命周期策略回收。

## 当前实测结果

2026-10-16 在临时独立 MySQL Community 8.4.11、macOS arm64 上执行 MySQL 套件通过，包含真实 SIGKILL/租约恢复；详细摘要见本阶段进度记录。该实例使用全新数据目录、回环端口和随机凭据，没有使用原业务数据库。

OSS 缺少端点、桶与凭据，报告 blocked；仅完成套件编译和缺配置失败保护。未执行真实云写入。MySQL 5.7/8.0、Linux 容器、多套完整服务、多机网络故障、真实 SDK 历史数据和全链路容量均仍待验收。readiness 保持原先的 503，不以新增脚本或局部集成通过作为切流许可。

官方参考：[MySQL 下载](https://dev.mysql.com/downloads/mysql/8.4.html)、[官方 MySQL 镜像](https://hub.docker.com/_/mysql)。

后续Linux/MySQL、全20接口和SDK结果见 [端到端验收](e2e-acceptance.md)，上文未测项是历史阶段范围；真实OSS仍无凭据。
