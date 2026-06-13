# 测试目录索引

当前阶段只初始化测试目录，不实现交易核心测试。

## 目录约定

- `tests/unit/`: Go 和 Python 的单元测试，后续用于配置、schema、状态机、账表计算等小粒度测试。
- `tests/integration/`: Redis Stream、9092 API、盘后对账和 Meridian 数据接入的集成测试。

## 当前状态

- 已创建测试目录骨架。
- 已增加 Go 配置加载、结构化日志、HTTP envelope 和 API 健康检查骨架的单元测试。
- 已增加统一交易 schema 的基础校验和状态机单元测试。
- 已增加 Redis Stream 命名、环境变量兼容、消息摘要解析和 Redis URL 脱敏测试。
- 已增加 PostgreSQL migration 静态检查，确认首版账本表和关键约束存在。
- 暂无交易核心测试用例。
- 9092 文档门户可通过 `/tests` 查看本索引和测试目录树。

## 后续计划

1. 增加 Redis Stream envelope schema 测试。
2. 增加 API handler 请求解析和校验测试。
3. 增加订单状态机和成交去重测试。
4. 增加 PostgreSQL migration 测试。
5. 增加盘后对账样例数据测试。
