# CTP F0 draft.2：回签修正与 Meridian 标的对齐

日期：2026-11-03，Asia/Shanghai。版本：`ctp.f0.draft.2`，仍为离线候选，未冻结/部署。

接续 [draft.1 字段附录](CTP_F0_CONTRACT_DRAFT_20261103.md)。原版及 OC 复验的三份原字节保留在提交 `2e25e62`；当前 `testdata/ctp/` 为 draft.2。凭据向量不变，不能用 draft.1 的消息指纹验证 draft.2。

## 1. OC 回签处理结果

已审阅 OC `7a4bb66/c50226d` 交付说明，接受其对样例不一致的指正，不能把此前测试通过解读成完整 schema 已通过。本轮落实：

- fill 样例补齐标准 `fill_id=ctp-fill-v1-5ad40797043cdb61172b14339c29986a866ac1cc606199b02b942f12c0b87393`，测试按既有七段编码独立重算，与 OC 公布结果一致；缺失/null/错误值或错交易日均失败，不另起算法。
- accepted、queue_expired 均补 product_type/schema_version；所有 CTP payload 都检查类型，不为“空回执”创建股票默认分支。
- 批量父 payload 必须有类型/版本；子项只有在字段**缺省**时继承，显式相同值允许，null/不同值拒绝。每个子项仍须显式匹配账户和 business_type=F，不允许跨账户、混合产品。
- 成交样例保留不同于登录采集者的 `EXTERNAL_USER`，不得替换成登录 UserID 或仅因它不同而过滤同投资者外部成交；TradeField 不补 RequestID。
- native_ids.sdk_request_id 在订单上仅指 OrderField.RequestID，不能使用查询 nRequestID 覆盖；查询自身 SDK ID 在 query_evidence。若将来重命名消除歧义，需双方另行版本化，不在本次静默改字段名。
- snapshot_applied 明确只指 **OC 本地类型化应用**；Relay 必须独立验证完整分页并在其数据库原子应用。两者不是分布式原子事务。

OC 报告 C++ 已独立解密并逐字节重加密 Relay 公开凭据向量。Relay 可记录“OC 报告独立验证通过”，不声称已运行 OC 的 9 项 CTest、完整 wire 转换、持久恢复或在线柜台验收。原 PIE 异常继续保留。

## 2. 期货标的唯一标准：Meridian

本轮直接读取了 Meridian 在线机器契约 `/v1/contracts/futures/ctp-level1`，并核对本地正式文件及 fixture：

| 证据 | 本轮读到的定义 |
| --- | --- |
| contract_id | `meridian.ctp_futures_level1.v1`，contract_version=1 |
| 合约行 schema | `futures_instrument.v1` |
| 标准标识 | security_id，例如 IF2612.CFFEX |
| 原生代码 | source_instrument_id，例如 IF2612；Meridian 自带离线 SC 样例为 sc2610，不能强制大写 |
| 交易所/品种 | exchange_id / product_id |
| 乘数/最小价位 | volume_multiple / price_tick；不在 Relay 新建不同语义的标准字段 |
| 生命周期 | 当前目录的首末观察日不等于 Trader API 法定上市/到期日 |
| 当前 authoritative_products | IF/IH/IC/IM，未列 SHFE；不能据此声明 SHFE 实时/目录已支持 |

本地契约 SHA256：`35207c44c3fe98a9468670c7c2d33d0015a2866d27d6a7028e11b66fd1676e30`。在线读取确认上述版本和覆盖声明；这不是全网 SHFE 能力不存在的断言，需 Meridian 明确补充其最新范围。

### 对 Relay/OC 的具体约束

1. 面向策略、自动补全、行情订阅、图表、估值与跨系统关联，**统一使用 Meridian security_id**；真实合约的 source_instrument_id/exchange_id 来自同一条明确目录记录，记录发布身份/来源，不由 Relay 拼后缀或大小写转换产生。
2. CTP wire 当前的 symbol/exchange 仍是柜台原生代码别名，映射为 Meridian 的 source_instrument_id/exchange_id；保留这条兼容映射不是另建标的标准。后续 HTTP/SDK 期货输入应以 security_id 选择真实合约，由 Relay 显式解析原码，不能将标准代码直接填入 SDK InstrumentID。
3. 调用方同时提供标准 ID 与原生代码时必须逐项一致，不能“择一相信”。两条目录记录指向同一原生身份但标准 ID 不同也要隔离核实，不按第一条猜测。
4. 对未知标的的柜台订单、成交、持仓，先保存原码和审计事实，标准关联留空/待补；不能为满足外键伪造 security_id，也不能因 Meridian 缺目录而丢柜台事实。新单则等待可信映射、有效约束及其他门禁，不允许按原码绕过。
5. draft 样例 rb2612 仍是 OC 合成原生合约示例，不代表 Meridian 存在 RB2612.SHFE，也不是可执行授权。当前不向这些样例补一个臆造的标准代码。
6. 主连/连续系列如 IF.DOMINANT.CFFEX 不是可交易合约；即使有 underlying_security_id，也不能自动替策略换成实际合约发单。策略明确选择真实合约后再走映射及准入。
7. Meridian 的行情字段、交易日/时间、bars/SSE schema 原样沿用；OC TradingDay 与订单自然时间分别保存，不在 Relay 重定义行情时间标准。
8. Meridian 元数据用于跨系统标准及行情；OC 合约查询保留柜台原始约束。两者来源/日期不同或发生冲突须展示并限制相关新单，不以静态查询替代账户权限、动态涨跌停或行情时效证明。

`meridian_mapping.json` 只选取 Meridian 自带 fixture 的 IF/SC 行中已有字段，保留 schema、来源与 fixture_only 标记。它**不是 Relay 新建生产目录**，匹配成功也不构成生产可交易证明。新增 8 项精确映射用例，覆盖原码大小写、错误交易所、未覆盖 SHFE 和连续代码；不连接或订阅真实行情。

## 3. SDK 约束及金额回复

接受 OC 按指定 6.7.13 头文件报告的有效字节上限，不按 Unicode 字符数判断，不截断。以下是柜台适配约束，不是对 Meridian security_id 的长度限制：

| 字段 | 有效字节上限 |
| --- | ---: |
| BrokerID | 10 |
| InvestorID / AccountID | 12 |
| UserID | 15 |
| Password | 40 |
| AppID | 32 |
| AuthCode | 16 |
| InstrumentID | 80 |
| ExchangeID | 8 |
| OrderRef | 12 |
| OrderSysID / TradeID | 20 |
| CurrencyID | 3 |
| InvestUnitID | 16 |
| Date / Time | 8 |

数量候选检查已收紧为 JSON 整数 `1..2147483647`；真实合约限价手数范围还需单独验证。价格候选词法检查补入最多 64 字节、无小数点的未缩放整数不超 uint64、最多 9 位小数，不通过浮点容差取整。OC 需继续核对高边界值与尾零规范化的先后顺序，未通过共同样例前不能扩大线上能力。

接受 O/C/CT/CY 对应 SDK 0/1/3/4 的**头文件映射**；这不证明所有 SHFE 合约都允许各 flag。B/S 和 speculation 按实际头文件映射，未支持的组合旗标必须拒绝而不是截去剩余部分。

同意金额以 SDK binary64 值可往返的十进制文本表达，不承诺恢复“券商原始十进制账务精度”。classic locale、max_digits10、展开指数、不做分厘舍入是 serializer 的候选规则；原始值/位模式保留审计。非有限/疑似无效哨兵不得变零，必需项无效使本次投影失败并保留旧快照，可选费用未知仍为 null。具体哨兵、长度和 serializer 共同向量仍待交付，不把合约行情的 price_tick 或价格 9 位限制套到资金金额。

另需 OC 收紧外层路由 token：Relay 当前 broker_id 是 1-32 位小写字母/数字/下划线/连字符，key_id 是 1-64 位字母/数字/点/下划线/连字符，logical account 是 6-20 位数字。OC 报告的通用 128 位 token 本地上限不能替代共同契约中的更严格路由规则；外层全部身份须由可信配置绑定，不能由 envelope 选账户。

## 4. Runner 与交付方式

源 runner：`tests/ctpcontract/contract_test.go` 和 `draft2_test.go`；依赖根目录 go.mod/go.sum 与被引用的 internal 包，不能只复制一个 Go 文件到 OC 目录执行。

```bash
go test ./tests/ctpcontract -count=1 -v
go test ./internal/credentials ./internal/redisstream ./internal/orderflow -count=1
```

提交后从固定提交打包 `.runtime/ctp-f0-draft2-source.tar.gz`，包含根 Go 模块、internal 源码、两个测试文件、全部 CTP 样例及附录，不包含 config、reference 券商文件或运行凭据。解压后在模块根运行第一条命令即可；Go 至少 1.24，第三方依赖需已有模块缓存或允许按 go.sum 下载，**未附 vendor，不宣称无缓存离线安装可用**。归档是构建产物，不加入 Git；提交号与归档 SHA256 在交付时报告。

凭据向量保持原 SHA256 `debc867b6604561845a7e9921dccd0ece626f4771ec41e855e8f044293e06cc3`。当前消息和查询文件版本已变，重新核对指纹，不覆盖 OC 原始留证文件。

## 5. 状态与下一步

本轮实际修改仅离线样例、测试和文档；四个包的测试通过，未改业务运行时、数据库、凭据、权限，也未部署/重启或切环境。新测试验证候选数据规则，不是完整生产 schema、OC 实现或数据库落库验收。

请 OC 先替换/并存保存 draft.2 样例并更新已知差异断言，按同一 fill_id 算法及批量继承规则复验；Meridian 标的契约是后续映射前提。对 Meridian 的具体覆盖确认项见 [协作清单](MERIDIAN_CTP_INSTRUMENT_ALIGNMENT_20261103.md)，本轮未修改 Meridian 工程。

完整金额 serializer、拒撤/批量部分失败/未关联成交持久恢复、重复同消息与矛盾终态、SDK/输出/ACK 崩溃矩阵仍在后续共同回归；F0 整体仍不打完成勾。
