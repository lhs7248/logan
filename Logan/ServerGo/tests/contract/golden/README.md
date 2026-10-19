# Java Controller 与 DTO 基线

这些 JSON 来自 `tests/legacy-java` 对未改动 Java 源码的实际执行，不是 Go 实现生成的预期。

- `run.json`：14 个确定性 Controller 用例的运行索引和契约清单指纹。
- 每个用例文件：HTTP 状态、关键响应头、响应原始字节（Base64）。
- `java-models.json`：原 Java 模型经 Jackson 序列化的固定样本。

测试使用 JDK 17、Spring MVC 5.2.2、Jackson 2.10.1。隔离工程将 Lombok 更新为 JDK 17 可编译版本，补充 Servlet/Annotation API；未更改原服务依赖。
Service 接口是测试替身，未启动 Spring 完整应用、数据库或 OSS；未包含生产 servlet Filter 与代理行为。
这些文件不证明正常上传、真实日志协议、真实SQL、下载签名、完整CORS或错误容器页面兼容。

只包含人工固定参数，不含真实设备、密钥和签名URL。完整 HTTP 采集产生的数据保存在 `.gitignore` 排除的 captures 目录，不自动接受为新基线。

重新采集先运行 `make java-baseline`，比较 captures/java-controller 与本目录差异，查明差异后再更新基线；不要以 Go 响应覆盖 Java 基线。
