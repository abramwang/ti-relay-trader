# CTP 账户只读终端与资金复测

日期：2026-11-17，Asia/Shanghai。

## 已发布

`/trade` 顶部新增“东吴期货测评 · 300052141 · 只读”入口，进入上期所行情与独立期货查询表格。也可从行情市场选择上期所/中金所；账户能力与行情市场不是同一概念，不能因中金所行情可用推断本账户有中金所交易权限。

- 账户不加入华鑫证券路由，不进入证券账户刷新、日终任务、绩效或股票下单服务。
- 四类表格：资金、持仓、委托、成交；支持列排序、每页20行、查询时间及来源明细。
- 期货多空、今昨、投保、投资单元分别展示，不将期货手数换成股票股数，不从持仓推导可平量。
- 已独立核验的09:24持仓12行，订单/成交分别完整空；标为历史采样，不宣称当前实时持仓。
- 10:33资金回复仍失败`QUERY_FAILED / missing_biz_type`，无资金数值。失败/未查询/未审核/分页不完整与完整0行分开显示。
- 心跳取当前路由最新条目，核对来源run_id/epoch，区分在线只读、未就绪、过期、已停止与不可用；停止心跳不会显示在线。
- 页面每5秒读取已保存的查询与Redis心跳，仅在期货视图可见时轮询。刷新按钮不是柜台查询命令，不会重发、报撤或确认结算。
- 期货提交仍双层禁用。GET接口显式返回`trading_enabled=false / cancel_enabled=false / business_published=false`；不新增写端点。

接口为 `GET /v1/futures/terminal`，TEST配置未启用时账户为空；生产环境不读取这套TEST观察存储。非GET返回405，响应禁止HTTP缓存。独立库故障只使期货观察不可用，不把证券链路或资金值置零。

## 数据与配置

本轮只读连接独立本机PG `relay_ctp_dongwu_20261117_test`，不是业务数据库。不执行迁移或改变原收件`pending`、owner/cursor，不注册过期批次或把旧窗口重新激活。

私有配置 `config/relay.local.yaml` 的 `futures_terminal`：

```yaml
futures_terminal:
  manifest_file: /home/ti-relay-trader/.runtime/ctp-terminal.test.json
  database:
    dsn: "host=... port=15434 user=... dbname=relay_ctp_dongwu_20261117_test sslmode=disable"
    expected_name: relay_ctp_dongwu_20261117_test
```

配置要求TEST、独立且明确命名的数据库。清单包含账户原生身份、当前gateway、可读历史gateway、来源/epoch、人工审核的请求上下文及独立证据文件SHA。不得从首条reply生成可信ticket/SDK ID。当前清单仅包含已核验早盘4条上下文；新资金失败作为已收原文诊断展示，尚无本机完整审计包，不自动升级审核标记。

读取时按账户/路由及已发布的原命令定位每类最新查询，核验命令和reply原文字节摘要及关联标识。审核上下文必须匹配持久发布目标，复用严格分页/字段解析作离线展示；这是原始查询观察，不是业务快照发布。最多256页/8MiB，不完整或矛盾结果不输出金融行。历史回放不调用原实时接收入口，不延长原请求超时。

私有清单和输出证据不进Git。当前审计PG仍在本机Unix socket/15434运行，不承诺其为已部署的常驻业务数据库；若停止，页面明确报观察存储不可用。新线程以README及本说明路径恢复，不重新初始化该库。

## 单笔资金复测结果

用户授权新隔离路由继续临时TEST功能验证，接受共享Redis淘汰风险，不豁免来源/epoch检查；不作为持久性或生产验收。

| 项目 | 实测值 |
| --- | --- |
| 路由 | test / ctp_4900 / ctp-dongwu-asset-retest-20261117 / 300052141 |
| 会话 | ctpproc-ba1756a547919493859985aa50dbb315 |
| 连接代次 / TradingDay | 1 / 20261117 |
| OC窗口 | 10:32:42 至 10:37:42 |
| 动作 | 唯一一笔 account.asset.query |
| origin_message_id | relay-dongwu-asset-retest-20261117-1794882789761829103 |
| Relay command_id / ticket / SDK request_id | 5 / 1 / 3 |
| 命令Stream ID | 1794882789788-0 |
| reply Stream ID | 1794882789945-0 |
| 回报时间 | 10:33:09.888+08:00 |
| 回报 | failed / QUERY_FAILED / missing_biz_type |
| 标准转换 | raw_completed=true、snapshot_applied=false、failure_stage=standard_projection |

新会话前的10:25:27窗口已结束时Relay未发命令，没有补发旧会话。新命令先登记PG再受控XADD，单次发送，没有重试；`cmd.query`只有1条、`cmd.trade`0条，两组pending=0/lag=0（10:40及10:47检查）。

证据目录：`outputs/recovery/ctp_dongwu_asset_retest_20261117/query-20261117T023309Z/`，含原命令、目标、新鲜心跳、发布结果及reply收件。后续`verification-20261117T024755Z.json`逐条核验Redis与PG：reply 1条/1,159字节，hb 592条/737,438字节，全部原文/摘要匹配。592条包括两轮会话和停止心跳，不是592条当前在线心跳。所有raw仍pending，未伪造delivered或业务发布。

恢复位点：

- reply owner=`83050fa8a13c5fdc04292462294d32856b87b3050decb0ec318bc9002d249be2`，cursor=`1794882789945-0`。
- hb owner=`d853a1c31f68f6f1eff203646cc6383847c14d483222c9d732d0408d94d6c2fc`，cursor=`1794883062615-0`。
- 来源/run_id不变；epoch=`dongwu-asset-retest-c943bafc0e7a920857d5a4bf34d07cf7`。旧gateway收件保持不动，不混用owner。

复测后独立PG备份：`outputs/recovery/ctp_dongwu_asset_retest_20261117/relay-audit-after-retest.dump`，SHA256=`b33a430264dd062769ea3244279215cf1d9ef98516633098a4d8e9f50fe06560`。未对业务库备份/恢复，也未回填Redis。

## OC 审计节选与下一步

用户转交两条OC审计记录，而非本机完整连续审计文件：

1. seq342 `query_request`，10:33:09.825，`ReqQryTradingAccount / before_sdk_call`，请求成员原值：BizType=`31`、BrokerID=`3439303000000000000000`、InvestorID=`33303030353231343100000000`；AccountID/CurrencyID保留空定长字节。记录摘要`b306761689f50a70998fff50391ba4e03f8e780a84ebbb49920653e17d66088e`。
2. seq344 `query_result`，相同message/session/ticket/SDK ID，submit_return_code=0，callback_error=null，last_seen=true，单行于10:33:09.840完成。响应BizType=`00`；BrokerID=4900、AccountID=300052141、CurrencyID=CNY、TradingDay=20261117。记录摘要`fa659718f211c9a6bee9658a118805e2f52b30087216e6e2406ce430ffc7774b`。

这支持“显式请求31，真实响应仍NUL”的结论，不再归因于请求漏填或Relay丢字段。未收到seq343/完整前缀，不宣称已独立重验本轮完整审计链。`before_sdk_call`单条不代表已调用；这里同时有同步返回、真实末页和PG失败回复佐证。

请OC下一步核实东吴测评柜台对该响应字段的约定。现有协议要求非空BizType，当前失败正确，**不需要重复同样的资金查询，不将00改成31，不放宽生产资金范围**。如柜台确认该测评接口合法不提供BizType，可共同评审“明确未知范围且不可用于交易/权益汇总”的最小表达，先形成字段契约和负例再编码；本轮未批准或实施该变更。

持仓/订单/成交原验收不作废，无需重复采样；资金仍不通过四查询整体准入。报撤、结算确认和生产部署继续关闭。终端只读开发不依赖先伪造一个成功资金值。

## 验证

- Go全包、futures/api/config定向race及13项原期货JS回归通过。
- 新模型测试覆盖独立审核、scope/关联不符、缺页、矛盾终态、失败不补零、完整空结果。
- 新临时PG测试重复读取不改变pending/cursor，不生成业务快照或审核登记；原文摘要篡改拒绝。使用单独`relay_ctp_terminal_test_20261117`，不改真实审计库。
- Playwright桌面1600×1000、手机390×844验证真实只读账户/12持仓/资金失败/完整空、排序、45行浏览器合成分页和部分结果清表；所有非GET拦截且没有写请求，无JS异常、页面水平溢出。
- 原期货行情600根跨夜OHLC、SSE与canvas非空回归通过。入口快速点击期间无代码时不请求空K线，避免初始化提示。

浏览器证据：`outputs/recovery/futures_terminal_account_20261117/`。这里只更新9092 TEST API/UI，未重启OC、切换环境、修改生产权限或业务库迁移。
