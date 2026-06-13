# relay - TI Relay Trader

relay 是量化研究系统的基础数据项目，负责标准化实盘交易柜台接口对接，以及模拟柜台交易账表能力。

## 线程恢复卡片

- Agent 名称: relay
- 工作目录: `/home/ti-relay-trader`
- 对外端口: `9092`
- 最终服务口径: `http://relay-trader.quantstage.com`
- 当前状态: 已推进 P1/P2/P4 部分底座，完成服务模式、配置加载、结构化日志、统一响应 envelope、API/worker 启动骨架、交易 schema、Redis 只读探测和接口测试台骨架；尚未实现交易核心逻辑。
- 最近更新时间: `2026-06-13`
- 恢复方式: 新线程进入本目录后，先阅读本 README 的“线程恢复卡片”“当前进展”“待办事项”“工作日志”，再继续执行下一项待办。

## 项目目标

1. 提供统一的 A 股交易接口，供交易软件和策略调用。
2. 通过 Redis Stream 对接托管机房前置服务，前置层已统一券商结构体和协议。
3. 支持多账户、多 broker、多 gateway 的交易路由和状态管理。
4. 提供盘后对账、历史数据接入、账户盈亏统计和模拟柜台交易账表。
5. 对外暴露稳定服务端口 `9092`，供量化研究系统内其他模块调用。
6. 将关键设计决策、运行状态、接口约定和未完成事项持续沉淀在本 README 中，保证 Codex 线程中断后可以快速恢复。

## 技术栈草案

- Go: 负责 9092 在线服务、标准化交易 API、多账户订单状态机、Redis Stream 对接、实时账表写入和健康监控。
- Python: 负责盘后对账、历史数据拉取、账户盈亏统计、研究侧脚本、验收脚本和批处理任务。
- PostgreSQL: 建议作为交易账表、资产快照、对账结果和盈亏统计的主存储。
- Redis: 用于和托管机房前置服务通信，遵循 `relay.stream.v1` Redis Stream 协议。
- Meridian: 用于 A 股历史数据和行情数据接入，入口为 `http://meridian-data.quantstage.com`。

详细架构见 [docs/ARCHITECTURE.md](/home/ti-relay-trader/docs/ARCHITECTURE.md:1)。

## 职责范围

### 标准化实盘交易柜台接口

- 账户查询
- 资金查询
- 持仓查询
- 委托下单
- 撤单
- 委托回报
- 成交回报
- 柜台连接状态与心跳
- 错误码标准化
- 多账户路由和权限状态

### 盘后与数据服务

- 盘后对账
- 历史数据接入
- 账户盈亏统计
- 资产快照
- 持仓快照
- 对账差异记录
- 研究侧数据导出

### 模拟柜台交易账表

- 模拟账户表
- 资金流水表
- 持仓表
- 委托表
- 成交表
- 交易日与结算状态
- 冻结资金、冻结持仓、可用资金、可卖持仓计算

## 目录结构

```text
.
├── README.md
├── cmd/
│   ├── relay-docs/      # 9092 文档门户入口，不包含交易核心逻辑
│   └── relayctl/        # 运维和联调 CLI，当前包含 Redis Stream 只读探测
├── config/              # 本地配置、示例配置、环境变量模板
├── data/
│   └── simdesk/         # 模拟柜台本地数据与账表样例
├── docs/                # 设计文档、接口文档、状态补充说明
├── internal/
│   ├── api/             # 9092 API 服务骨架、健康检查和配置态账户列表
│   ├── config/          # Go 配置加载、服务模式和账户路由配置模型
│   ├── httpx/           # HTTP request_id、中间件、统一 JSON envelope
│   ├── logging/         # 结构化日志初始化
│   ├── redisstream/     # Redis Stream 命名、消息摘要和只读探测边界
│   ├── trading/         # 统一交易接口 schema、枚举、基础校验和状态机语义
│   └── worker/          # 后台 worker 常驻进程骨架
├── scripts/             # 开发、运维、迁移、数据初始化脚本
├── src/
│   └── relay/
│       ├── api/         # 对外 API 服务，默认监听 9092
│       ├── connectors/  # 实盘柜台、券商、网关适配器
│       ├── schemas/     # 标准化请求、响应、事件、账表模型
│       ├── services/    # 业务服务与编排逻辑
│       └── simdesk/     # 模拟柜台撮合、账表、结算逻辑
└── tests/
    ├── integration/     # 集成测试
    └── unit/            # 单元测试
```

## 端口约定

- 对外服务端口固定为 `9092`。
- 最终服务口径固定为 `http://relay-trader.quantstage.com`。
- 默认监听地址后续建议使用 `0.0.0.0:9092`，方便容器或外部系统访问。
- 当前优先运行文档门户模式，展示项目框架、文档和测试目录，不连接 Redis、数据库或实盘柜台。

## 9092 文档门户

启动命令：

```bash
scripts/serve-docs.sh
```

也可以直接指定监听地址：

```bash
RELAY_DOCS_ADDR=0.0.0.0:9092 scripts/serve-docs.sh
```

页面入口：

| 路径 | 内容 |
| --- | --- |
| `http://relay-trader.quantstage.com/` | 文档门户首页 |
| `http://relay-trader.quantstage.com/healthz` | 文档门户健康检查 |
| `http://relay-trader.quantstage.com/api-console` | Apifox 风格接口测试台 |
| `http://relay-trader.quantstage.com/docs` | 文档列表 |
| `http://relay-trader.quantstage.com/docs/readme` | README |
| `http://relay-trader.quantstage.com/docs/architecture` | 架构草案 |
| `http://relay-trader.quantstage.com/docs/roadmap` | 开发路线图 |
| `http://relay-trader.quantstage.com/docs/data-model` | 数据模型与落盘设计 |
| `http://relay-trader.quantstage.com/docs/trading-api-schema` | 统一交易接口 Schema |
| `http://relay-trader.quantstage.com/docs/api-test-console` | 接口测试台设计说明 |
| `http://relay-trader.quantstage.com/docs/python-sdk` | Python SDK 设计 |
| `http://relay-trader.quantstage.com/docs/operations` | 运行配置与任务管理 |
| `http://relay-trader.quantstage.com/docs/redis-stream-probe` | Redis Stream 只读探测 |
| `http://relay-trader.quantstage.com/docs/third-party-integration` | 前置服务 Redis Stream 对接手册 |
| `http://relay-trader.quantstage.com/tests` | 测试目录索引和测试目录树 |
| `http://relay-trader.quantstage.com/tree` | 项目结构 |

## 外部依赖

- 前置服务 Redis Stream 对接手册: [docs/THIRD_PARTY_INTEGRATION_GUIDE.md](/home/ti-relay-trader/docs/THIRD_PARTY_INTEGRATION_GUIDE.md:1)
- 内网资源文档: `http://doc.quantstage.com`
- Meridian A 股数据源文档: `http://meridian-data.quantstage.com`

注意：不要把内网资源里的密码、Token、生产账号、柜台地址写入仓库。

## 当前进展

- [x] 初始化项目根目录。
- [x] 创建基础目录骨架。
- [x] 创建 README 并写入线程恢复信息。
- [x] 添加目录占位文件，确保目录骨架可被 Git 跟踪。
- [x] 形成 Go + Python 初版技术栈草案。
- [x] 明确前置服务已统一券商协议，relay 聚焦多账户和标准 API。
- [x] 实现 9092 文档门户模式，可通过 Web 查看项目框架、文档和测试目录。
- [x] 在首页提供开发路线图入口，路线图详情通过 `/docs/roadmap` 查看。
- [x] 明确账户交易数据、订单数据、成交数据最终需要落盘，PostgreSQL 作为优先候选。
- [x] 明确真实凭据可放在部署机本地配置文件，后台批处理可采用 cron 管理。
- [x] 明确 9092 正式交易接口后续需要封装 Python SDK 给策略开发使用。
- [x] 参考 Meridian SDK，明确 relay SDK 后续通过内网 HTTP 安装包供策略机 pip 安装。
- [x] 定义服务运行模式 `docs`、`api`、`worker`。
- [x] 增加基础配置加载，支持 `RELAY_CONFIG_PATH` 和 `config/relay.example.yaml` 模板。
- [x] 增加结构化日志，默认 JSON 输出，HTTP 请求带 `request_id`。
- [x] 增加统一 JSON 响应 envelope 和标准错误码骨架。
- [x] 增加 API/worker 启动骨架，API 模式已提供 `/healthz`、`/v1/status`、`/v1/accounts`。
- [x] 定义第一版标准交易柜台接口 schema，覆盖账户、资金、持仓、下单、撤单、订单、成交和事件。
- [x] 记录前置测试环境已启动，后续可基于 Redis Stream 做接口联调。
- [x] 新增 Redis Stream 只读探测命令 `relayctl redis-probe`，支持本地配置和 `HX_REDIS_*` 环境变量。
- [x] 新增 Apifox 风格接口测试台骨架 `/api-console`，用于后续 API 联调。
- [ ] 设计模拟柜台账表 schema。
- [x] 实现正式交易服务模式下的 9092 健康检查接口骨架。

## 待办事项

1. 定义第一版统一柜台接口 schema，包括账户、资金、持仓、下单、撤单、回报事件。
2. 设计 PostgreSQL 交易账表和模拟柜台账表 schema。
3. 增加 Python 盘后对账与账户盈亏统计任务骨架。
4. 继续补充 API、配置、schema、账表迁移的单元测试和集成测试。

## README 状态维护规则

后续每次完成重要工作，都需要同步更新以下内容：

- “线程恢复卡片”的当前状态和最近更新时间。
- “当前进展”的复选框。
- “待办事项”的新增、完成或调整。
- “工作日志”的新增记录。
- 如出现阻塞，记录在“阻塞与风险”。
- 每次项目更新后自动执行一次 Git 提交。

不要在 README 中写入密钥、账号、Token、生产柜台地址或其他敏感信息。

## 阻塞与风险

- 当前无阻塞。
- 9092 当前线上仍运行文档门户模式；API 模式只是工程骨架，尚未连接 Redis Stream、数据库或交易核心。
- 前置测试环境已启动，后续可做 Redis Stream 联调；当前 shell 未配置 Redis URL 或 `HX_REDIS_*` 环境变量，所以本轮没有对真实 Redis 做现场探测。
- 联调凭据只放本地配置或安全渠道，不写入仓库。
- 接口测试台当前只开放页面骨架和只读健康检查测试；交易写接口仍是 `planned`，不会发送未实现请求。

## 工作日志

- `2026-06-13`: relay 初始化项目目录，创建 README，记录端口 `9092`、职责范围、目录结构、恢复方式和初始待办。
- `2026-06-13`: 添加 `.gitkeep` 占位文件，保证空目录在后续提交中保留。
- `2026-06-13`: 根据用户补充信息，形成 Go + Python 技术栈草案；明确 relay 聚焦多账户标准交易 API、盘后对账、历史数据和账户盈亏统计，前置层负责券商协议统一。
- `2026-06-13`: 按用户要求优先实现 9092 文档门户模式，支持 Web 查看 README、架构草案、前置对接手册、测试目录和项目结构；未触碰交易核心逻辑。
- `2026-06-13`: 确认域名映射完成，将 `http://relay-trader.quantstage.com` 记录为最终服务口径。
- `2026-06-13`: 新增 `docs/ROADMAP.md`，并在文档门户首页展示整体开发步骤和阶段进度。
- `2026-06-13`: 新增 `docs/DATA_MODEL.md`，记录 PostgreSQL 落盘口径、C++ 结构体参考源、标准字段映射和首批账表建议。
- `2026-06-13`: 新增 `docs/OPERATIONS.md` 和 `config/relay.example.yaml`，记录本地配置文件、凭据管理和 cron 后台任务口径。
- `2026-06-13`: 新增 `docs/PYTHON_SDK.md`，规划面向策略开发的 9092 API Python SDK。
- `2026-06-13`: 参考 Meridian SDK 发布方式，规划 relay SDK 通过 `http://relay-trader.quantstage.com/sdk/relay-sdk-<version>.tar.gz` 供内网 pip 安装。
- `2026-06-13`: 根据用户反馈，首页不再直接展开开发路线图，仅保留 `/docs/roadmap` 入口。
- `2026-06-13`: 推进 P1 工程化底座，新增 `internal/config`，实现 YAML 配置加载、`docs/api/worker` 模式校验和账户路由重复检查，并让文档门户可选读取 `RELAY_CONFIG_PATH`。
- `2026-06-13`: 继续推进 P1，新增结构化日志、HTTP request_id、统一 JSON envelope、API 服务骨架和 worker 常驻进程骨架；API 模式已提供健康检查、服务状态和配置态账户列表。
- `2026-06-13`: 进入 P2，新增 `internal/trading` 和 `docs/TRADING_API_SCHEMA.md`，定义第一版统一交易接口 schema、枚举、基础校验、状态机语义和 Redis Stream 映射；记录前置测试环境可用于后续联调。
- `2026-06-13`: 进入 P4 前置对接准备，新增 `cmd/relayctl redis-probe`、`internal/redisstream` 和 `docs/REDIS_STREAM_PROBE.md`，实现 Redis Stream 只读探测边界；当前环境未配置 Redis 凭据，尚未现场读取真实 stream。
- `2026-06-13`: 根据用户要求新增 `/api-console` 接口测试台骨架，采用 Apifox 风格三栏布局：接口集合、请求编辑和响应查看；交易写接口当前标记 `planned` 并禁用发送。
