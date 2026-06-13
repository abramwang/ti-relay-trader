# relay - TI Relay Trader

relay 是量化研究系统的基础数据项目，负责标准化实盘交易柜台接口对接，以及模拟柜台交易账表能力。

## 线程恢复卡片

- Agent 名称: relay
- 工作目录: `/home/ti-relay-trader`
- 对外端口: `9092`
- 最终服务口径: `http://relay-trader.quantstage.com`
- 当前状态: 已实现 9092 文档门户模式，并在首页展示整体开发路线图；尚未实现交易核心逻辑。
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
│   └── relay-docs/      # 9092 文档门户入口，不包含交易核心逻辑
├── config/              # 本地配置、示例配置、环境变量模板
├── data/
│   └── simdesk/         # 模拟柜台本地数据与账表样例
├── docs/                # 设计文档、接口文档、状态补充说明
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
| `http://relay-trader.quantstage.com/docs` | 文档列表 |
| `http://relay-trader.quantstage.com/docs/readme` | README |
| `http://relay-trader.quantstage.com/docs/architecture` | 架构草案 |
| `http://relay-trader.quantstage.com/docs/roadmap` | 开发路线图 |
| `http://relay-trader.quantstage.com/docs/data-model` | 数据模型与落盘设计 |
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
- [x] 在首页展示整体开发路线图和阶段进度。
- [x] 明确账户交易数据、订单数据、成交数据最终需要落盘，PostgreSQL 作为优先候选。
- [ ] 定义标准交易柜台接口 schema。
- [ ] 设计模拟柜台账表 schema。
- [ ] 实现正式交易服务模式下的 9092 健康检查接口。

## 待办事项

1. 定义账户、gateway、Redis stream prefix 的多账户路由配置。
2. 定义第一版统一柜台接口，包括账户、资金、持仓、下单、撤单、回报事件。
3. 设计 PostgreSQL 交易账表和模拟柜台账表 schema。
4. 增加 Python 盘后对账与账户盈亏统计任务骨架。
5. 将文档门户模式和正式交易服务模式拆分清楚，避免 9092 入口职责混淆。
6. 补充最小单元测试和集成测试。

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
- 9092 当前只是文档门户模式，尚未提供交易 API、Redis Stream 通信或账表写入能力。

## 工作日志

- `2026-06-13`: relay 初始化项目目录，创建 README，记录端口 `9092`、职责范围、目录结构、恢复方式和初始待办。
- `2026-06-13`: 添加 `.gitkeep` 占位文件，保证空目录在后续提交中保留。
- `2026-06-13`: 根据用户补充信息，形成 Go + Python 技术栈草案；明确 relay 聚焦多账户标准交易 API、盘后对账、历史数据和账户盈亏统计，前置层负责券商协议统一。
- `2026-06-13`: 按用户要求优先实现 9092 文档门户模式，支持 Web 查看 README、架构草案、前置对接手册、测试目录和项目结构；未触碰交易核心逻辑。
- `2026-06-13`: 确认域名映射完成，将 `http://relay-trader.quantstage.com` 记录为最终服务口径。
- `2026-06-13`: 新增 `docs/ROADMAP.md`，并在文档门户首页展示整体开发步骤和阶段进度。
- `2026-06-13`: 新增 `docs/DATA_MODEL.md`，记录 PostgreSQL 落盘口径、C++ 结构体参考源、标准字段映射和首批账表建议。
