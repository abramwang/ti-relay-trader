# relay - TI Relay Trader

relay 是量化研究系统的交易基础数据项目，负责标准化实盘柜台接口、多账户路由、Redis Stream 对接、交易账本、交易日任务、绩效计算和研究侧导出。

## 线程恢复卡片

| 项目 | 当前口径 |
| --- | --- |
| Agent | `relay` |
| 工作目录 | `/home/ti-relay-trader` |
| 对外服务 | `http://relay-trader.quantstage.com`，端口 `9092` |
| 业务时区 | `Asia/Shanghai`，所有交易日、任务和业务时间按东八区解释 |
| 当前环境 | `2026-11-18 15:18 Asia/Shanghai` 按用户指令切回TEST，`.runtime/active-config.yaml -> config/relay.local.yaml`；9092 API内嵌同步运行，生产独立worker停止。本机/域名均test/ok，当前PID以service status为准；后续切换仍需用户明确指令 |
| 安全状态 | TEST华鑫00030484沿用已启用交易配置，15:18心跳登录/快照ready，但不以盘后ready承诺可撮合，本轮未发报撤。生产配置仍6户保留/2户查询/0交易，387/388暂时停用、历史保留。今日CTP交易授权15:00已结束，不追加报撤、不重做结算或重启OC |
| 当前阶段 | P0-P4 完成，P5-P8/P10 持续生产化；N13 绩效重建保留未完成项；CTP TEST已完成CLI经Redis的真实单笔报撤及回报持久收件，普通API/终端报撤与完整业务快照仍未上线 |
| 最近确认 | `2026-11-18 15:16` 昨日20261117权威行情复算补跑成功，仅当前启用6830/5768；6830已是权威日线、核对无变化，5768仍有previous_economic_nav_gap及ETF范围/清算未核验、未发布净值。今日15:12收盘采集两户成功，今日结算/绩效仍未补跑；387/388保持停用 |
| 更新时间 | `2026-11-18` |

新线程按以下顺序恢复：

1. 阅读本节和“当前工作”。
2. 阅读 [开发路线图](/home/ti-relay-trader/docs/ROADMAP.md:1)，确认最新未完成项。
3. 执行 `git status --short`、`scripts/relay-runtime-service.sh status` 和 `curl -fsS http://127.0.0.1:9092/v1/status`。
4. 涉及生产任务时先查看 `/jobs`、`/operations` 及 `/var/log/relay/`，不得仅凭页面摘要修改账本。
5. 需要追溯旧决策时再阅读 [README 完整历史归档](/home/ti-relay-trader/docs/README_HISTORY_20260826.md:1)。

## 当前工作

- `2026-11-18` **TEST常驻原文收件入口完成**：`relay-ctp-replies --capture --follow`支持reply/event独立许可、最长24小时、空闲继续和尾包保存；显式owner/cursor续读，来源/epoch/组及位点原文改变停收，取消/到期报告最终PG位点，不自动ACK/入账或放开交易。隔离Redis/PG回归及统一离线脚本已补；未部署/迁移在线库/发柜台命令，仍TEST。下一步持续业务分发、实际事实查询与价格准入、拒撤展示及人工新尝试预算；单次review/业务consumer仍限5分钟，不能与follow并行夺同流owner。见[协调清单顶部](docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md#relay常驻原文收件进展)，OC无需新协议回签。

- `2026-11-18` **事实调度/常驻增量适配完成**：OC f0f0477/测试62011e2材料10摘要匹配，原字节归档`testdata/oc_query_runtime_review_20261118/`；9事实/3心跳/6坏请求回归。TEST事实显式登记发布/review收件已接通，未执行阶段null保持失败，day只读查询可在交易窗口外继续；修复本地命令失败误投影订单拒单。全Go/vet及隔离PG race通过。未部署/迁移在线库/发柜台命令，仍TEST；000047拒撤迁移仍待部署。下一步常驻接收生命周期（当前单次consumer仍5分钟）、真实事实联测和价格准入替代、拒撤展示及人工重撤预算。OC三项实现已收，不再要求重复开发或材料回签，按[当前执行清单](docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md#事实调度与常驻增量复核)准备固定版本TEST部署；不宣称全天稳定/生产就绪。以下同日条目为阶段记录，以本条为准。

- `2026-11-18` **独立拒撤持久接线完成**：现有Stream分发/事件收件/消费循环支持order.cancel.rejected，核对原撤单和已确认发布的scope/session/代次/Redis来源，结果落独立表，不改订单/成交或撤单预算。未知发布继续待核，同文去重、冲突保留、事务失败不丢原收件；全Go/vet及专项和旧链路隔离PG race通过。新增迁移000047仅临时库已测，业务库/CTP独立库未迁移、未部署或发柜台命令，保持TEST。下一步事实查询在线编排、拒撤页面/API/SDK及人工新尝试预算；见[协调清单顶部](docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md#拒撤持久接线进展)。不宣称完整撤单尝试汇总或无人值守稳定版。

- `2026-11-18` **事实查询本地链路与部署评估**：新增TEST候选动作的命令登记/目标幂等、双SDK结果解析、原文收件与合约独立快照，复用现有PG表；缺末页失败、null保留、重启只读历史、同键换合约冲突。全Go/vet及隔离PG新旧查询/登记/发布race通过，在线发送仍关闭，CLI事实编排及拒撤尝试账本待接。16:20只读确认服务TEST/ok，未部署/切环境/发柜台命令。[同一协调清单顶部](docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md#独立部署阶段评估与下一步)建议进入固定版本TEST常驻集成测试准备；OC继续事实调度、常驻配置、局部拒绝隔离，不再做临时300秒测评；完整交易日及错误分支未验前不宣称无人值守/生产稳定。

- `2026-11-18` **OC职责收敛材料已接收并适配第一步**：`reference/responsibility_review_20261118/`7摘要匹配，原字节归档testdata同名目录；3心跳/2拒绝/3事实候选离线核验。新查询能力显式false不被旧推导覆盖，原生/标准快照scope分别校验；新增绑定原撤单命令的独立拒撤解析，保留订单状态/错误原文，不自动重试。全Go/专项race/vet及3组隔离PG race通过。拒撤持久分发/尝试预算及独立事实登记收件仍待，候选动作未开放，API未重启、未发柜台命令、保持TEST。见[同一执行清单](docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md#本轮增量接收与直接执行结论)，OC直接继续事实调度无需再回签，双方接通前保留逐单原生检查。

- `2026-11-18` **补充OC禁止模拟柜台边界**：用户要求OC只做柜台接入和报文梳理转发；现有材料证实的是离线模拟SDK/假SPI测试，尚无独立业务模拟柜台证据，不把两者混为一谈。[同一职责清单](docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md)新增明确排除模拟撮合/余额持仓/收益/结算，允许轻量测试替身但不进入业务启动或真实路由；历史撮合归回测引擎。请OC仅说明实际模块及部署入口，若有业务模拟功能则停止扩展并解除依赖，不再要求大材料包。本轮仅文档更新，未修改OC或运行权限。

- `2026-11-18 15:24` **推进OC职责精简及Relay本地准入**：[给OC执行清单](docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md)明确局部业务失败不退出、行情事实查询与连接就绪解耦、普通运行配置精简三项；Relay接事实/准入/账本，Meridian协调由Relay负责。已统一准备和原生发布前的精确十进制价格检查，发布登记层不再吞掉具体价格拒绝原因；新增20个边界用例及隔离PG“拒绝零发布/零预算占用，后续合法请求正常”回归。全Go、futures/CLI vet及临时PG race通过，交易CLI已构建；Redis在线测试未运行，未发送任何柜台命令、未重启9092/OC、未切环境。现有day模式仍依赖OC逐命令原生检查；待实际心跳/独立事实出口交付后协同移除，不单边取消保护或宣称OC已完成。

- `2026-11-18 15:18` **按用户指令切回TEST**：通过switch-relay-env.sh test完成目标库迁移检查、停止生产API/worker并启动TEST内嵌同步，域名test/ok、Redis/数据库/行情/事件桥正常；华鑫00030484心跳broker/order_snapshot/accepting_trade_commands均true。生产配置及387/388停用状态不变，无新柜台查询/报撤或OC重启。今日17:10生产canonical任务仍依赖生产API，留在TEST会无法选择生产账户；已提醒用户，不能据计划时间自行切生产。今日正式结算/绩效未补跑，昨日复算和今日柜台收盘采集证据保留。

- `2026-11-18 15:15:58–15:16:03` **补跑昨日权威行情复算**：昨日失败实际为TEST环境下`no enabled performance accounts selected`，并非Meridian水位未就绪。按用户要求以生产配置、目标20261117、当前启用6830/5768执行正常canonical脚本及互斥锁；任务`performance_canonical-20261117-watermark-poll`已从jobs接口核验succeeded，完成标记写入。6830原权威日线版本不变；5768成本calculated、价格来源meridian_1d_pre_close_and_close，但净值仍blocked且persisted=false，保留前序净值缺口/ETF权益范围/私有清算未核验，不宣称全账户业绩闭合。原失败报告备份`performance_canonical.failed-20261117.json`，补跑日志`/var/log/relay/performance_canonical-manual-20261118-for-20261117.log`。不重新启用387/388、不查询OC、不调整交易权限；今日17:10自动复算安排不变，今日正式结算未在本轮补跑。

- `2026-11-18 15:12:38–41` **生产盘后查询已补齐**：显式限定债享5号314000046830、富盈13号314000045768，通过正常post_close_capture及流水线互斥锁补跑，五类资金/持仓/订单/成交/费用查询终态和资金持仓新鲜度全部通过；两户broker_close资金快照写入，当前均零证券持仓。查询账本6830订单/成交0/0，5768订单/成交100/92；不是仅看命令受理。生产任务run_id=`post_close_capture-20261118-1794985958677590000`已从jobs接口确认succeeded。15:01原定时任务实际采集的是当时TEST00030484，原报告另存`/var/log/relay/reports/post_close_capture.test-20261118-1501.json`，新生产报告为`post_close_capture.json`，执行日志`/var/log/relay/post_close_capture-manual-20261118-1513.log`。本轮没有报撤、重新启用停用户或补跑结算/绩效；17:10权威复算安排不变。

- `2026-11-18 15:11` **暂时停用智算汇利混合307000051387、涌盈波动率307000051388**：按用户指令将私有生产配置两户enabled及trading_enabled置false，保留账户路由/别名/所有历史数据；盘前/收盘/默认绩效任务、主动查询和网关监控排除两户，worker仍可留存尾部事件。生产API/worker已重启，域名及路由接口验证6配置/2启用/0交易；另两户在线。未停OC进程、未删除Redis键/凭据或数据库账本，TEST配置不变。现有Stream错误仍保留待核，不宣称今日结算完成。

- `2026-11-18 15:02` **按用户指令结束TEST并恢复生产**：通过`scripts/switch-relay-env.sh production`完成目标库迁移检查及API/独立worker启动；本机与域名确认production、trading_enabled=0。387/388柜台未ready，另两户online，Redis/数据库/行情/事件桥正常。TEST历史、CTP独立PG和原始证据保留；今日唯一实际报单已撤，额外两笔未发，不延长15:00授权。今日生产收盘任务完成情况需单独核对，不以切换成功代替结算成功。

- `2026-11-18 14:57` **交易终端已显示真实CTP委托**：修正私有terminal manifest从旧PM路由到当前readonly-recovery路由；只读API新增经本地命令/发布记录/来源/摘要/会话校验的`local_orders`，与失败的全量订单查询分开，按订单归并实时状态，不改审计pending或冒充业务快照。桌面/手机Playwright均看到39429/rb2701/1手/已撤单，增加柜台委托号及中文状态，TEST API已发布重启，生产未改。全Go/vet、临时PG race和前端测试通过。用户要求追加两笔，脚本04/05已准备显式接管上次已退出event位点，但OC限价仍14:41:54过期，已通知刷新，尚未追加发单；15:00截止后不发。用户确认后续OC做减法，行情/价格准入、关联/账本归Relay，必要身份/幂等及未知不重发保留，见路线图。

- `2026-11-18 14:47` **OC报撤审计闭环核验通过**：新附件1711条/5,596,299字节，sequence/previous/SHA256/scope全部通过；报单SDK9、撤单SDK10的原生请求/受理回执/ACK/working及cancelled关联一致。两回执两事件与Redis及独立PG原文4/4逐字节一致；结算确认仅1次，后续6次为只读核验。原生行情确认为rb2701买一5218/卖一5428、限价5218–5653。归档`reference/oc_ctp_live_audit_20261118/readonly.audit.jsonl`，追加报告在`day-trade-03/oc-audit-review/`，详见[执行单顶部](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。不覆盖历史证据、不声称券商正式签认或完整独立callback链验证；本轮无交易/环境变更。

- `2026-11-18 14:43` **东吴TEST真实一报一撤已完成**：session `ctpproc-cac1855c2c51ccbad63ea1ffde3b5b92`，完成新查询command16/17后，14:41:54发布command18（message尾`03-1794984114779304590`），rb2701买开1手5218，14:41:55.366收到working；14:42:01发布command19撤单，14:42:01.818收到cancelled，OrderRef=`000000000008`/OrderSysID=`39429`、成交0。严格解析的两事件及两回执已进独立PG原始收件，两命令组pending/lag均0；不冒充已完成全部业务账本/柜台测评签认。前两单未调用SDK的失败回执均保存，不重发。定价新增仅东吴TEST/day/rb2701的一手下限价模式，使用实际OC限价且SDK前仍刷新，Meridian仅作证券映射，不伪造买一或把测评行情当实盘；原生产规则未变。全Go/vet/临时PG race通过，CLI已构建，9092未重启。证据`day-trade-03/`及最新receiver位点见[执行单顶部](docs/OC_CTP_JOINT_EXECUTION_20261118.md)；已请OC核对柜台记录，无需重复发单。仍保持TEST，不能自行切生产。

- `2026-11-18 14:27` **第二轮实际报单仍待OC核对，禁止重发**：旧command12已有失败回执`1794982934537-0`明确未调用ReqOrderInsert，pending清零且原文已入PG。新session `ctpproc-eb6393fdb05193a1e805b5495260cee2`两查询command13/14完成后，14:25:47发布command15（message尾`02-1794983147097334006`、entry`1794983147144-0`），rb2701买开1手2958。14:25:49 OC停止，原生限价5218–5653与Meridian买一2968不一致；第二笔pending=1、无reply/event，未发撤单。已请OC核对精确原生行情及执行责任，不凭心跳认定未报柜台。脚本已退出，`day-trade-02`不可盲目重跑；新reply owner/cursor、查询和报价证据见[执行单顶部](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。保持TEST，生产/结算授权未变。

- `2026-11-18 14:11` **CTP首笔报单已发布，未决，不可重报**：新session `ctpproc-bf780bba2df27c084c8864a4c33c83bb`完成当前两查询后，14:09:31发布command12，message_id=`relay-ctp-day-20261118-01-1794982171142780504`，Stream ID=`1794982171204-0`，rb2701买开1手2958。14:09:41 OC stopping；14:10:42命令pending=1、无交易回执/event，无撤单。已请OC核实是否进入ReqOrderInsert及退出原因，禁止自动重发/换ID补单，不按submit_attempts=0推定未执行。脚本已超时退出，`day-trade-01`目录留证、不可盲目再跑。day逐单实现及测试已完成，最新初始化/接收位点与现场证据见[执行单顶部](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。未重做结算、未改生产或重启9092。

- `2026-11-18 14:06` **CTP原生快照分离、日间逐单发布已实现**：当前session `ctpproc-d5c3f0eadf37b0c11da1aa873516341c`（1/20261118），新订单/持仓查询command8/9已发，标准订单失败保留但native ready、持仓成功；两回复显式续收，owner/cursor见`receive-20261118T060444.154586606Z.jsonl`。day approval/逐订单持久额度、具体父单撤单、旧模式混用拒绝接通，全Go/vet/临时PG race通过，CLI已构建；不再逐笔询问用户。当前只支持连续一手买开限价及对应撤单，非全部期货动作。**尚未实际发单**：OC合约缓存13:56:26采样已过期，14:05报instrument_expired并关报单；已请OC修复刷新死锁，不能绕过ready。现场封装`.runtime/ctp-day-trade-20261118.go`仅适用当前session且须新event receiver，已构建未执行；见[当前执行单](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。无结算重确认/生产变更/9092重启。

- `2026-11-18 13:48` **已执行CTP两初始化查询**：常驻session `ctpproc-0338e2357dca25664bef86f9797770a2`、代次1/20261118、登录/结算true。command6订单查询failed `order_association_pending`（raw_completed=true），command7持仓completed14行/完整末页；两回复原文已入独立PG、业务审核待OC原审计，无报撤。已把外部订单关联阻断本地准入反馈OC，要求拆分而非伪造快照。OC另定位rb2701查询附带期权行被误判冲突，正改精确匹配/末页后应用；不将其记为合约不支持。命令/回复/续读位点在`outputs/recovery/ctp_dongwu_scheduled_20261118/initialization-ctpproc-0338e2357dca25664bef86f9797770a2/`；不重发已发查询/结算，详见[最新执行记录](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。

- `2026-11-18 13:40` **CTP真实结算已确认，接通day profile初始化查询**：新session `ctpproc-1a883ba7568fa47b9dddb146b421f419`在13:38:23心跳登录/结算true，22deferred/0blocking，随后合约identity_mismatch退出（根因待OC）；两命令流0。OC报告匹配结算成功回调，不能重复确认。day profile实际至15:00已获用户授权，Relay查询发布已适配/CLI构建，futures及query CLI测试通过；交易发布仍拒绝day profile，多笔升级待办。4条新心跳续收入独立PG累计7条，owner/cursor见`heartbeat-capture-20261118T054003Z.jsonl`，接收已退出。等OC修复后新会话，不再要大材料；无新查询/交易或生产修改，见[当前执行单](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。

- `2026-11-18 13:35` **用户明确放开CTP TEST交易测试，不再逐笔请示**：实际新路由hb仅3条，最后13:15:22 stopping/14 blocking，session `ctpproc-dc96c0954ecd430394e5eb6eef073614`；两命令流0，未发报撤。13:36原始心跳已落独立PG，owner/cursor见`outputs/recovery/ctp_dongwu_scheduled_20261118/heartbeat-capture-20261118T053617Z.jsonl`，接收进程已退出。已请用户转OC直接恢复实际可用进程/新当日日间配置，不清状态、不重做未知结算；旧13:35已过期。Relay多笔发布仍未实现，day profile仍拒绝，不能宣称全部功能已开；多笔接线提升优先级，生产保持关闭。见[最新授权与阻塞](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。

- `2026-11-18 用户调整职责` **先测本地报撤，暂缓外部恢复迁移**：OC收敛为连接/授权结算/命令调用/标准转换与原文错误转发，无关外部坏回报不应退出或拖停本地链路；账本、外部关联、清算及策略准入归Relay。已覆盖执行单旧前置并更新路线图；Relay既有延期策略不要求旧32条关联/清零，新增查询及本地报撤回归。当前legacy_source专用字段仍按已交付协议解析，若OC精简由Relay同步接线，不新增恢复机制。未收到真实启动session，未发新命令、未切环境/延长13:35窗口。

- `2026-11-18 13:14` **OC scheduled实际准备通过，进入现场启动**：`reference/oc_ctp_scheduled_preparation_20261118T1305`六摘要/SDK一致，单笔无day profile，窗口13:30–13:35、进程上限3600秒。Relay13:10独立SCAN新恢复路由0键/run_id符合/noeviction；13:11独立PG保留5查询/0交易，新命令/receiver均0。已发给用户转交OC的复核后初始化及提前登录回执，结算和命令仅窗口内；等待实际session/代次/TradingDay及窗口内审计交付方式。程序/库已就绪但接收尚未运行，不伪造会话，终端仍旧PM观察绑定待新心跳再切。无新命令/生产变更，见[执行单顶部](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。

- `2026-11-18 12:58` **OC日间权限包复核与提前启动适配**：f79188a包9摘要、9离线场景审计链/journal/31输出原文、14查询审核草稿及104心跳通过；合成时间/来源不得作真实证据。已接scheduled生命周期及等待/到期发送门禁，day profile仅解码观察，查询/交易发布显式拒绝，PG单笔额度不变。先请OC使用本版scheduled不带day profile完成首轮1手报撤，无需再改首轮代码；多笔持久预算/逐单撤单仍Relay待办。全Go/专项race/vet和临时PG额度/未知/目标回归通过，五CLI已构建；详见[唯一协调单顶部](docs/OC_CTP_JOINT_EXECUTION_20261118.md)。未初始化、登录或发柜台命令，生产未动。

- `2026-11-18 12:39` **运维页纳入CTP账户**：`/operations`合并证券与TEST期货观察账户表/筛选/在线数，新增API `futures_accounts`，轻量复用严格来源/epoch心跳读取，不查询历史账本或向OC发命令。登录/查询/结算/报撤声明与终端只读权限分开，停止/过期标历史，未知PEL显示`--`。域名桌面1600及手机390检查通过，全Go/专项vet及3项JS测试通过，API重启PID713310仍TEST。当前300052141私有manifest仍指向旧PM路由，实际`epoch_missing_or_changed`，未自行切候选/重建键；当日联调核验后需更新观察绑定。华鑫权限、生产配置和CTP授权均未改；详见[运维说明](docs/OPERATIONS.md#ctp-期货账户状态)。

- `2026-11-18 12:26` **华鑫TEST手动报单入口恢复**：00030484原trading_enabled=true，无额外批量/撤单权限开关；OC心跳实际credential_status=missing。12:25:30使用已有加密CLI和用户此前TEST凭据补发v1，operation_id=`d7ea2addbdd409495d7aa2a6ce1bef2e`、key_id=hx-test-20260918，解密验证成功；无明文文件，OC自动loaded并broker_ready/快照/报撤true，无需重启OC。移除私有`config/relay.local.yaml`旧huaxin_7x24_test的order_entry_schedule（现为实时行情仿真），API重启PID705419，域名order_entry_ready=true、来源oc_heartbeat、无阻断理由。实际午间接受/撮合由柜台决定，不承诺成交。生产6户trading_enabled全false；未改CTP、运维DLQ写权限、幂等或参数校验，未发订单。私有配置不入Git，凭据缺失原因本轮未溯源，不归因于新淘汰。

- `2026-11-18 12:13` **当日CTP授权确认**：用户同意今日TEST300052141结算确认、两初始化查询及rb2701一手买开/对应撤单，同时允许扩大仿真多笔。安排先首笔闭环，再最多5笔/每笔1手串行（Relay执行上限、含首笔）；代码核实Relay及OC仍为单笔预算，多笔需联合小范围升级，不换assessment/清计数绕过，不延误首轮或自动反手平仓。已更新[授权与协调安排](docs/OC_CTP_JOINT_EXECUTION_20261118.md)；当前仅文档记录，未初始化、启动或发交易。

- `2026-11-18 12:11` **按用户指令切TEST账户**：`switch-relay-env.sh test --skip-migrations`成功，API PID698045（实时以status为准）、内嵌同步运行，生产worker停止；本机/域名test/ok，账户00030484启用且保留原交易配置。切换前未发现独立日常任务进程，生产API此前degraded未在本轮展开排查；不声称今日盘前已完成。未迁移DB、初始化CTP、确认结算、发查询/交易或自动补跑生产任务。今日生产15:01收盘前需用户明确切回，不自动定时切换；CTP沿已验准备材料另核实当日窗口。

- `2026-11-17 22:57` **按用户指令切回生产**：执行`switch-relay-env.sh production --skip-migrations`成功，API PID428463、独立worker PID428058（实时以status为准）；本机和域名均production/ok，Redis/DB正常，6配置/4启用/0可交易。保留TEST/CTP审计及配置，未迁移数据库、开交易、初始化候选、登录OC或发送命令；未手工补跑此前生产权威复算，不能将切换成功当任务完成。明日CTP需用户明确切回TEST并确认新当日窗口；以下“保持TEST”为切换前历史。

- `2026-11-17 部署准备收口` 接收`reference/oc_ctp_deployment_preparation_20261118`，4摘要、SDK指纹、旧三个源文件及manifest与已验材料一致；OC报告实际凭据配置预检/本地来源准备退出0，新state空、两处once不存在。结算关闭、截止刻意过期，不改成即刻可启动；Relay未读远程私有配置、未联网或运行柜台。下一步直接按[现场四步清单](docs/OC_CTP_JOINT_EXECUTION_20261118.md)核查新run_id/占用/接收器，用户确认新当日授权和窗口后最终预检、初始化、登录两查询及1报1撤；无需新增代码或原则回签。下方“实际配置预检待完成”为历史，现场最终预检仍待。

- `2026-11-17 22:46` **OC group修正离线验收收口**：收到`reference/oc_ctp_audit_group_fix_20261118`版本02af39b，19摘要及18次投递组/原文关联通过，9查询直接由新原审计生成草稿并通过Go验证；错误组留证/不ACK，部分查询不升级成功，旧50回调保留。68心跳/23输出及8场景同库race通过，全Go/vet/14项Python通过。报告`outputs/recovery/ctp-audit-group-fix-20261118/material-audit.json`；[给OC下一步](docs/OC_CTP_JOINT_EXECUTION_20261118.md)已改为实际私有配置预检、明日新窗口/即时审计，不再要求协议修改。以下“缺group待修”为历史；无OC启动、业务Redis/PG修改、服务重启或环境切换。

- `2026-11-17 22:40` **补齐查询与事件同库联跑**：新增7场景，9页查询按原命令及独立审计ticket/SDK身份绑定，完整查询/本地订单共存、working后登记撤单、重放不重复；失败/部分不产出快照，拒撤/意外成交保留原状态。专项临时PG race与既有解析/事件测试联合通过，14项Python、全Go及futures vet通过。仍未收到OC group修正版；内存假设修正验证9条下游草稿可通过但不出证据文件、不算正式接收通过。下一步只等定向新审计及实际预检，再安排新授权窗口；[同一协调单](docs/OC_CTP_JOINT_EXECUTION_20261118.md)已同步，TEST/业务库/Redis/服务未动。

- `2026-11-17 晚间增量` **OC次日测评实现已收，待一项审计修正**：`reference/oc_ctp_nextday_review_20261118`九摘要通过，原50回调/18审计/32pending逐条保留、7场景审计链/outbox核对一致；63心跳/9查询页/7事件及8反例通过解析。Relay新增严格assessment `legacy_source`、来源固定/原生身份/诊断及四场景PG终态与重放测试。**发现command_received缺group，已有草稿工具拒绝9条查询证据，未放宽校验**；请转[同一执行单顶部](docs/OC_CTP_JOINT_EXECUTION_20261118.md)让OC定向补实际消费组并提供新审计。接受唯一恢复候选走同一assessment主程序，无需独立只读进程切换；实际配置预检、占用/责任、新当日授权/会话/行情仍待。当前TEST、无业务Redis/PG修改/OC启动/报撤/9092重启；以下“等待最终assessment包”为历史。本次材料报告在`outputs/recovery/ctp-nextday-review-20261118/`，不将模拟值当明日实况。

- `2026-11-17 晚间` **次日联调准备推进**：接收`reference/oc_ctp_readonly_runtime_review_20261117/`，21个离线场景的58心跳/13回复及9反例通过；新`readonly_query_evidence.v1`严格解析/原生范围/固定来源/诊断接通，待关联不死锁查询、冲突及未知ACK不放行。全Go/vet、专项race、临时PG非空订单/v2成交关联及23组本机Redis/PG演练通过。真实旧50/18/32责任未改，材料运行来源3/2/1不是真实迁移；无OC/Redis业务操作、环境切换或服务重启。**请转OC [11月18日执行清单](docs/OC_CTP_JOINT_EXECUTION_20261118.md)**：今晚交最终assessment版本、跨日回放/状态承接回归及审计交付方式，明日真实窗口再做必要查询、1报1撤与落库。以下“等待只读实现”为历史；现等待测评路径最终交付与新窗口授权。

- `2026-11-17 17:55` 盘外准备补验收口：整个futures包临时PG race回归通过（378.658秒），含1万订单/1万成交关联及重启重放不重复；日志为下条第二目录`postgres-full.log`，现已并入统一脚本。共享Redis专项不在这次PG阶段连接，23组本机Redis演练另行通过；临时服务已清理，真实OC/服务/权限无变更。

- `2026-11-17 盘外准备` **不再盘中临时拼装基本链路**：新增`scripts/test-ctp-offline.sh`，私有Redis/临时PG的23组链路演练与4项故障回归通过，12项审计工具测试、111项Python契约、全Go/vet通过，五CLI构建并保存摘要。新增`build-ctp-query-review.py`从OC独立原始审计生成草稿，拒绝缺链/错会话/错请求/仅调用前记录，不联网登记；今日4查询原始材料均通过Go离线校验，失败资金不改成功。两轮证据在`outputs/recovery/ctp-offline-20261117T174333.9Azl00/`及`ctp-offline-20261117T174715.4NPSjF/`，原始草稿在`ctp_dongwu_joint_20261117/offline-builder-{0..3}/`。服务/生产权限未改、共享Redis未写，旧50回调责任保留；开窗前分工及用法见[唯一协调清单顶部](docs/OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)。下一步直接接OC实际只读JSON/恢复交付，未交付前不猜字段或初始化候选，新交易窗口另行确认。

- `2026-11-17 17:27` **独立Redis故障回归完成**：新增4项opt-in测试，race三轮12次通过；真实OOM下旧键不淘汰、查询发布保留PG attempted且恢复容量不重发、交易发布unknown、刷盘后受控重启保留原文/epoch/示意cursor/PEL而旧run_id拒绝发送。仅本机Unix socket Redis8.0.2（临时解包，无系统服务）及`relay_ctp_terminal_test_20261117`随机schema，退出已清理；不接共享8.2.2/OC，不更改运行代码。测试`internal/futures/redis_storage_local_test.go`、复验/边界见[回归记录](docs/OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)。共享配置/挂载/重启仍未验，OC候选不初始化。17:20发现权威复算`no enabled performance accounts selected`（当前TEST9092），用户已明确保持测试稍后切换；不自动切回、不补跑、不改cron。

- `2026-11-17 候选JSON补充` 用户已粘贴`oc.ctp.readonly-binding.candidate.v1`；路由/六流/epoch/只读开关与已确认方案一致，三个旧源文件SHA重新比对一致，18/32及22/10分类一致。仅核对粘贴内容及源摘要，不冒称拿到候选原文件字节；未查新路由占用、创建配置/状态或登录。继续等待OC五项只读实现及实际心跳/查询样例，无需再回签候选；详见[唯一候选与边界](docs/OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)。

- `2026-11-17 16:37` **OC新只读候选已确认，仍未具备启动条件**：接受`ctp-dongwu-readonly-recovery-20261117`、group同前缀加oc-、候选epoch `dongwu-readonly-0d304a7a29800b24d832803bed3007ab`；旧18 audit_only/32 pending保留原范围，source-manifest/legacy-links只追加关联。OC仍需完成只读原生通知持久留证、跨gateway关联/恢复、完整查询证明、心跳和工具允许列表；Relay收到实际只读JSON后适配，不能把assessment延期策略套用。代码核实独立只读目前无pending计数契约，早前“旧模式pending=0”仅指旧assessment。见[直接开发清单](docs/OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)。本轮只更新文档，未查占用/预留/初始化/登录/迁移/改环境；下条“等候选”为历史，现等待实现及JSON，不再原则回签。生产17:10任务仍需用户切回。

- `2026-11-17 16:29` **TEST Redis留存首测通过**：独立随机开发前缀下string/stream两键跨300秒31次检查，原值/entry/body及PTTL=-1保持，run_id不变、evicted_keys仍12326（增量0）、AOF写ok；结束清理本次两键成功。证据`outputs/recovery/ctp_redis_retention_20261117/5519565e16cbcec1e6bddbc615b06d0d.json`。非容量压测/重启验收，不证明历史键已恢复；16:28旧下午route仍0键。共享Redis没有改配置/重启/业务键写入，OOM拒写与持久化重启后续单独验。

- `2026-11-17 收盘后` **OC v4增量核验及Relay接线完成**：`reference/oc_ctp_postclose_review_20261117/`四摘要通过，原50回调/32延期逐条一致；17心跳、两页查询、2个本地订单事件及2个accepted关联通过离线核验。修复历史pending导致初始化查询死锁，仅显式TEST `local_assessment_only.v1`按严格计数允许延期；ACK未知、生产、过期授权仍阻断，诊断保留未完成责任。全Go、专项race/vet及111项Python通过，inspect/query/replies/trade CLI已构建；9092/OC未重启、无业务命令/迁移，环境仍TEST。复验脚本`scripts/audit-ctp-postclose-review.py`；详见[当前OC回执与下一步](docs/OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)。下一步等待OC具体新只读绑定/旧回调关联方案，不再重传材料或原则回签，不重建旧路由；旧13:35授权不复用。生产17:10权威复算仍须用户确认切回。下条“待材料”为历史状态。

- `2026-11-17 16:10` **OC收盘后增量回执已接收**：报告业务5ec0e31已补运行v2/测评回放隔离/缺文件锁持续拒绝及50回调离线运行回归；无真实进程、新session、迁移或结算，v4增量材料仍待本机接收，不冒称独立验收。Relay16:09:28核实TEST Redis2GiB/noeviction/AOF everysec、写状态ok、累计evicted12326/expired307，run_id不变，下午前缀仍0键（组缺失PEL/lag不可读，不填0）。独立PG仍5查询/0交易/0成交，旧原包摘要及审计链复验一致。证据`outputs/recovery/ctp_dongwu_assessment_pm_20261117/preflight-20261117T080928.414206400Z.json`。已请求只传v4 JSON/log/CTest及SHA、新心跳说明；下一步直接核验延期责任/实际输出/ACK未知保护，然后明确新只读绑定或显式迁移，不重做原则审阅、不重建旧路由、不复用13:35配置。见[收盘后回执与最小清单](docs/OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)。生产17:10任务仍需用户切回。

- `2026-11-17 15:16` **按用户新指令恢复TEST联调环境**：`switch-relay-env.sh test --skip-migrations`成功，域名API返回test，生产API/worker停止、生产权限未改。华鑫00030484新session=`hxproc-531c8a3d45079d77`，心跳新鲜但broker_ready=false/BROKER_DISCONNECTED。东吴下午route在15:16:31再次全量SCAN0键，run_id不变，未重建epoch/组或发命令；独立PG5查询/0交易/0成交不变。检查证据`outputs/recovery/ctp_dongwu_assessment_pm_20261117/preflight-20261117T071631.918433093Z.json`，已通过用户请OC回最新运行修复/当前进程/本地journal与once责任；下一步只读核验及修复联调，不能收盘后沿旧会话报撤。17:10生产权威复算仍需生产API，已提醒用户，未自动切回或修改cron。下条生产运行状态为切换前历史。

- `2026-11-17 15:01` **生产日任务已执行并核验**：按用户指令14:56执行`switch-relay-env.sh production --skip-migrations`，API/独立worker运行、生产下单关闭、两停用户不变，未应用CTP迁移。今日盘前09:01成功；cron收盘采集15:01:02至03、结算15:01:04至06成功，四户资金及2行持仓快照全部落库、16项对账输入、0差异/账户错误。每日绩效15:01:12执行成功但仅债享5号发布暂定净值；768为previous_economic_nav_gap并保留ETF范围/清算未核验，388/387另有归因残差4,959,900/4,999,900元、前序净值缺口及当日日线未就绪，不将差额猜成资金划转或利润。证据`/var/log/relay/reports/{pre_open_init,post_close_capture,post_close_settlement,performance_daily,performance_publish}.json`及`post_close_pipeline.log`，API任务记录一致。17:10起权威复算计划不变、尚待执行，不保证可消除资金/历史缺口；本轮不强行发布或修改资金事实。TEST联调暂停，本机无CTP CLI运行，原证据/PG保留，不自动切回。以下14:22及之前的TEST安排仅为历史。

- `2026-11-17 14:22` 用户明确OC只在交易时段内常驻，今天仍优先15:00前一手报单/对应撤单；原额度不增加、未知不重试、原13:35会话不复用。14:21:57只读检查下午路由仍3旧hb/末条stopping，两命令流0、pending/lag0，尚无新会话。已请OC回运行修复版本/预计就绪及新session/截止，下一步核验后受控执行，见[执行单顶部](docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md)。不改生产，不因常驻自行放宽命令时效或门禁。

- `2026-11-17 下午` **成交v2接收与OC材料核验完成**：九段编码已获OC回签；上传`reference/fill-v2-review-20261117.tar.gz`外层9/内层7摘要通过，50原始回调逐条核对，10成交保留10个v2键、4组v1一对多，24条身份观察（revision25）及8条审计链通过。受限证据在`outputs/recovery/ctp_fill_v2_review_20261117/`，只读复验脚本`audit-ctp-fill-v2-review.py`；不导入候选。Relay源码已接严格SHFE v2、完整查询/账本混合版本阻断、查询实时防重；全Go/PG专项race/vet、111项Python通过，原schema另存不改，新扩展`query-page-fill-v2.schema.json`。真实独立PG仍5旧查询/0交易/0成交观察。**未重启服务或更新运行二进制、未启用新窗口**；OC仍待运行切换、持久外部来源/回放释放、只读查询准入拆分及新心跳。见[唯一当前修正及回执](docs/OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)，无需再回签原则；收到可运行版本后构建接收工具、核验旧绑定/责任，再约窗口。13:35旧窗口已截止，不自动重启/确认/交易，生产不动。

- `2026-11-17 13:32 Asia/Shanghai` **下午测评启动即停止，尚未通过**：OC新session=`ctpproc-ea3476fac51da091a7bda6bd0dae79e4`，13:30:32两条broker_not_ready，13:30:33.778已stopping；代次1/20261117，recovery=false、pending_callback_count=32、order_ref_ready=true，报撤尝试0。Relay无新查询/交易，PG仍5旧查询/0交易，两命令流0/pending0/lag0。已通知OC查退出及自动回调积压，并核对结算once/SDK/回调，不自动重启重确认，13:35截止不延长。3条4443字节hb已落独立PG并与Redis原文/SHA核验一致，新owner=`7ab0d180bfd73394f1a2e7340525a4fe0ec8abaef887585af601bd89b1329766`、cursor=`1794893433793-0`，接收器已退出；后续须显式续读。见[执行单当前结果](docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md)。下条计划不代表本轮成功或仍在运行。

- `2026-11-17 13:01 Asia/Shanghai` **下午窗口已定**：用户转交OC部署`7d83ccd`、12:55:51前缀空/run_id一致/本地状态空；计划13:29复核初始化、13:30启动、**13:35硬截止不延长**。Relay仍等待实际session/代次/TradingDay/截止及新鲜心跳，收到后两初始化查询→一手报撤；不提前发占位查询、不自动登录/定时发单。未独立核验OC部署，不把安排当作已启动；见[执行单顶部](docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md)。下条新路由参数继续有效。

- `2026-11-17 12:53 Asia/Shanghai` **下午候选已确认**：OC回报旧5查询均已本地ACK、无待发/交易/结算once，属OC本地证据；Relay独立PG仍5查询/0交易、旧位点保留。新gateway=`ctp-dongwu-assessment-pm-20261117`于12:52:48全量SCAN空、run_id相同，新PG命令/receiver均0；assessment=`dongwu-assessment-20261117-pm-01`，epoch=`dongwu-assessment-pm-be85ee43d6c54faa617e394ca36b9e15`。已告知OC开窗前复查后可初始化新绑定，目标13:30起最多300秒；等待实际session/代次/TradingDay/截止，不提前发命令。工具已就绪，终端新观察绑定已更新并重启TEST API，旧查询保留、网页只读、生产未动。见[唯一执行单顶部](docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md)；替代下条“等待候选”，不取消旧路由丢失记录。

- `2026-11-17 12:45 Asia/Shanghai` **下午开窗前新阻断**：asset-retest前缀全量SCAN再次0键，epoch/命令流/reply/hb全部缺失，run_id未变；独立PG仍5查询/0交易发布。已通知OC核对10:33资金查询journal/outbox/ACK及settlement once，再提供新隔离报撤绑定候选；不能沿旧绑定启动、重建或重放。来源未能逐键确认，本轮开发回归仅写随机relay-dev键，不继续共享实例测试写入。终端`epoch_missing_or_changed`是真实连接状态，历史查询保留；下一步先确定新路由，再按13:30目标窗口执行，不能仅按下条“接线完成”就发单。详见[执行单顶部](docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md)。

- `2026-11-17 午间` OC c83731d单笔测评模式已适配：交付六项摘要、48心跳/4事件原文核验，初始化订单/持仓查询不被快照门禁反向阻断，assessment身份/原生限价时效/精确tick/独立撤单预算均受控；终端识别测评中但仍只读。全Go、定向race/vet及临时PG/随机隔离TEST Redis回归通过，更新TEST工具与9092展示，不切环境/不动OC。用户确认下午13:30窗口；**仍待OC实际assessment_id、新session/代次/TradingDay/截止**，不提前消耗单次结算授权、不使用10:37旧会话、不重复资金查询。随后两条新初始化查询→一手买开→真实working/SysID→撤单→真实cancelled。资金范围仍未核验、网页交易未开放，本轮尚无真实报撤。唯一协调文档仍为[单笔执行单](docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md#午间接线确认c83731d)；本条替代下条“等待心跳JSON”。

- `2026-11-17` 用户确定通过Relay与OC主程序共同链路完成东吴报撤，不走临时直连；批准仅本次300052141 TEST显式结算确认。已新增`relay-ctp-trade`受控单笔发布：精确原文短期授权、1手B/O、Meridian映射/5秒行情检查、当前交易心跳/来源/epoch原子门禁、先持久attempted、未知不重发、每批一次提交/撤单；撤单核对独立PG真实订单事件。`relay-ctp-replies --stream-role event`支持原文收件及显式续读，不伪造delivered。Go/PG/race/vet与随机隔离TEST Redis测试通过，两CLI已构建；仅独立东吴PG备份后应用46，原5笔查询/交易发布0，生产及9092未动。见[给OC的唯一当前执行单及运行参数](docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md)。首选RB2701.SHFE/rb2701，限价现场重取；还需OC确认合约、真实报撤心跳模式/部署及新窗口，当前未知模式仍拒绝。未发送东吴报撤，未宣称业务订单账本上线。以下旧条目的直连备选和“Relay只有查询发布工具”以本条为准。

- `2026-11-17 11:00 Asia/Shanghai` 用户明确今日交易时段优先完成东吴TEST登录及至少一笔有效报单/成功撤单。登录已验证，报撤尚未执行：OC最后交付仍只读，Relay当前发布CLI也仅四查询；已请求OC立即确认可部署报撤入口/窗口及必要结算确认，不再重复资金复测。Meridian SHFE实时行情10:59已核实可获取，不能据此推定东吴允许该合约。见[今日最小执行清单](docs/OC_CTP_DONGWU_TEST_READINESS_20261116.md#11月17日现场优先级更新)。若采用OC受控直连需明确入口及用户选择，只算券商测评；当前仍TEST，未发单/撤单/结算确认，生产权限未改。下条资金语义核实继续保留，但不再作为今日唯一推进方向。

- `2026-11-17` 用户批准接入CTP只读终端。`/trade`顶部“东吴期货测评 · 300052141”可进入独立资金/持仓/委托/成交表格、排序/分页及心跳，读取独立PG，未加入华鑫账户路由；旧09:24持仓12行、订单/成交完整空明确为历史采样。新会话`ctpproc-ba1756a547919493859985aa50dbb315`于10:33:09只发1笔资金查询（command_id5），仍missing_biz_type；用户转交OC审计请求31/响应00、末页真实，非请求漏填。原命令/1回复/592心跳Redis-PG逐条一致，两组pending/lag=0；10:37:42窗口停止，未重发/报撤/结算确认。详见[终端、复测、OC下一步及恢复位点](docs/CTP_TERMINAL_READONLY_20261117.md)。私有配置`.runtime/ctp-terminal.test.json`，独立PG仍Unix socket/15434；页面刷新不发柜台命令，不改变raw pending/cursor。Go全包/race、临时PG及桌面手机浏览器通过；当前仍TEST，生产未动。下一步核实柜台BizType语义/必要的显式未知范围契约，不默认00为31，不宣称四查询通过。

- `2026-11-17 10:23 Asia/Shanghai` 用户接受临时共享TEST存储风险继续单笔功能测试。新gateway=`ctp-dongwu-asset-retest-20261117`全量SCAN空、run_id相同；Relay新路由命令/receiver均0，已确认给OC。group同名加oc-前缀，候选epoch=`dongwu-asset-retest-c943bafc0e7a920857d5a4bf34d07cf7`；仍同来源/独立PG，新接收从0-0起，旧证据位点不动。OC声明旧4笔持久ACK已核对，本机未独立核验该journal，不影响已核实原始结果。下一步OC复查后初始化并开300秒只读窗口，回新session/代次/TradingDay/截止，再仅一笔新资金查询。详见[新路由执行确认](docs/OC_CTP_TEST_ROUTE_LOSS_20261117.md#1023用户授权的新路由功能复测)。本轮无Redis写/查询/启停，缺键仍立即停；功能验证不等于持久性或生产验收。

- `2026-11-17 09:51 Asia/Shanghai` 东吴TEST路由丢失，暂停CTP复测：全量SCAN真实路由/昨夜隔离路由均0键，run_id不变；本轮Relay未清理/设TTL/重建组epoch。发现共享Redis512MiB/allkeys-lru，累计evicted_keys11847、峰值535.53MiB，华鑫TEST托管凭据也已缺失；尚无逐键/历史监控证实具体删键来源，不直接归责。独立PG4条命令/4回复/327心跳完整且SHA通过。见[只读排查与待授权恢复方案](docs/OC_CTP_TEST_ROUTE_LOSS_20261117.md)：先独立noeviction交易存储或协调共享实例保护，再核对旧责任并批准新隔离路由/epoch；不回灌旧命令、不冒充首次空路由。未改Redis配置/清键/重启OC或切环境。

- `2026-11-17` 接受OC资金定向修正说明`96c55f7`：请求BizType显式字符1/hex31，并在SDK调用前持久审计五个原生字段；响应NUL仍失败，wire/Relay解析不变。OC自述33项测试及4项sanitizer通过，本机尚无biztype_review材料，不冒称独立复验或已部署。下一步用户确认修正版部署并开新只读窗口后，仅一笔新身份资金查询，核对query_request与同步返回/原始末页/标准回包；见[复测清单](docs/OC_CTP_DONGWU_FOUR_QUERY_REVIEW_20261117.md#接受oc定向修正说明)。不需再往返原则文档；本轮未连接业务Redis、未发查询或启停/切换环境。

- `2026-11-17` 东吴四查询独立材料已验：`reference/query_review_20261117`清单7项、359行完整摘要链/319行当前会话、4条命令及回复、295条心跳、12条持仓字段映射全部一致。资金原始BizType=00（NUL），不是Redis丢字段；OC报告请求未显式填BizType，但本包无原生请求抓包。请OC仅明确期货资金请求类型并补审计/离线回归，再安排一笔新资金采样；不默认修改响应。六种BizType负例回归已补，解析/wire不变；详见[复核结论及最小修正](docs/OC_CTP_DONGWU_FOUR_QUERY_REVIEW_20261117.md#资金问题与oc下一步)。原文已落PG，独立审计报告保留，业务快照未发布，逐命令durable ACK及非空报成交链路未验收；本轮无新查询/服务或环境变化。

- `2026-11-17 09:27 Asia/Shanghai` 东吴真实四查询已发送并收件：4笔均先PG登记再受控发布，各一次；持仓12条、订单/成交完整空列表，资金失败`QUERY_FAILED/missing_biz_type`（OC标记standard_projection、raw_completed=true）。4条命令/回复Redis与PG原文一致，OC两命令组pending/lag=0，无报撤。原始回复4条/8,695字节落独立PG，业务快照未发布，待OC独立请求审计；不得从reply自证ticket/SDK身份。会话09:25:14停止。详见[首轮四查询反馈及恢复位点](docs/OC_CTP_DONGWU_FOUR_QUERY_REVIEW_20261117.md)，请OC定位BizType原始值/映射，不猜默认值。心跳续读后累计327条，当前owner/cursor以新反馈为准；独立PG仍运行，9092华鑫TEST与生产均未改。下一步审计核对和资金修复后定向复测，不是四查询整体通过。

- `2026-11-17 09:20 Asia/Shanghai` 用户要求准备CTP测试：已核实东吴真实只读会话09:13:34登录ready、09:14:03停止；OC确认原生BrokerID=4900、UserID/InvestorID=300052141（Investor范围待查询验证）。三个CLI重新构建、定向测试通过，新独立本机PG库`relay_ctp_dongwu_20261117_test`准备33..41/44/45，仅Unix socket/15434、当前运行；32条实际心跳/39,620字节已持久化且SHA一致，登记/发送均0。详见[现场准备与续读位置](docs/CTP_TEST_QUERY_RUNNER_20261116.md#11月17日现场准备)。下一步请OC另开最长300秒只读窗口并及时给原始请求审计，核对新session后串行四查询；旧session已停不得使用。9092仍华鑫TEST，未改业务库/生产/权限、未发报撤/结算确认，不把登录心跳当四查询验收。

- `2026-11-17 09:07 Asia/Shanghai` 按OC反馈补发TEST账户00030484托管凭据：先核实TEST Redis DB0及current缺失，使用现有`relayctl credentials rotate`和此前用户提供的凭据，09:06:43发布v1并切current，认证解密通过，PostgreSQL started/succeeded审计成功，operation_id=`fada24e451961faf4e35577053cf4d4c`。明文仅经关闭回显的stdin输入，未新增明文文件。OC自动读取为loaded，09:07:14心跳仍broker_not_ready/BROKER_DISCONNECTED，broker_ready/order_snapshot_ready/order_entry_ready=false；后续请OC核对柜台连接/登录日志，不再归因于凭据缺失。未切环境、重启OC、发报撤单或更改权限/迁移；生产未动。CLI旧restart_required=true为保守提示，本次未执行重启且已证实自动加载。

- `2026-11-17 09:04 Asia/Shanghai` 用户确认早盘工作完成并要求切回测试：切换前核实生产`pre_open_init-20261117-1794877262047269000`为succeeded，09:01:02.047269开始、09:01:04.6867完成，盘前进程已结束。执行`switch-relay-env.sh test --skip-migrations`成功，本机/域名均TEST，生产独立worker停止；原权限及账本保留。测试OC有新鲜心跳但报`OC_CREDENTIAL_NOT_READY`，不是交易就绪，未自动修凭据或发送订单。未应用CTP迁移、未登录/重启OC；生产15:01收盘及17:10起权威复算配置未改，须用户明确指令再切回生产，不自动定时切换。

- `2026-11-17 07:35 Asia/Shanghai` 用户要求先切生产完成盘前采集：已执行`switch-relay-env.sh production --skip-migrations`，本机/域名production/ok、4户启用/0户可交易，独立worker恢复。依赖/交易日/cron已核对；OC四户心跳均停留昨日15:30附近，今日盘前任务记录为空，因此尚不能采集当日资金/持仓，未发无消费者的查询、未以昨日账本生成日初快照。原09:01 cron会按生产配置查询订单/成交/资金/持仓并持久盘前快照；下一步待OC正常上线及任务执行后核验四户结果。不是已完成今日采集，不自动切回测试或开启CTP；服务切换不改变原始账本、历史绩效及数据库迁移。

- `2026-11-16` 已接`relay-ctp-replies`独立TEST回复入口：raw持久收件、显式owner/cursor续读、run_id/epoch固定、独立OC请求审核原文/SHA与ticket/SDK ID绑定、四查询分页/快照及订单成交事实处理。隔离Redis/临时PG、CLI跨进程续读及错误/缺页保护通过；000045仅测试库验证，9092和业务库未改，没有向真实账户发查询。见[运行说明](docs/CTP_TEST_QUERY_RUNNER_20261116.md#独立回复入口已实现)。下一步真实白天窗口登录与四查询；审核上下文仍需由OC原始请求审计核实，不从回复自证，联调与交易权限仍未开放。

- `2026-11-16` `relay-ctp-query`已补受控TEST发送：登记后先持久记录attempted，再原子核对来源/run_id/epoch/新鲜只读会话后发布原文；未知结果、重复及8并发不自动重发。000044仅临时测试库应用，真实TEST Redis随机开发键验证后清理，未向东吴/华鑫账户发命令；9092仍华鑫TEST，业务库迁移未动。见[运行入口与下一步](docs/CTP_TEST_QUERY_RUNNER_20261116.md)。下一步接独立reply收件、OC ticket/SDK请求证据及四查询落库；发送成功不是柜台完成。原心跳17条审计及停止的独立PG不变，11-17 09:05仅候选窗口。

- `2026-11-16` 首轮OC实际Redis心跳联测已通过：17条/18,745字节与OC审计逐字节一致，独立capture进程5+12续读、末位点重启不重复、旧owner拒绝，以及本机PG重启落盘复核均通过。末位点`1794835752035-0`，来源/epoch不变；[收口及恢复位置](docs/OC_CTP_REDIS_JOINT_RUN_20261116.md#首轮结果实际心跳收件pg及续读通过)。独立本机审计库及dump保留、PG进程已停止；未动业务库/9092/OC或发送命令。17条raw pending为审计责任，不是OC命令PEL；OC两组PEL/lag=0。下一步独立可信命令入口与真实四查询，11-17 09:05仅候选；不能将今晚transport-only通过升级为登录/交易ready。

- `2026-11-16 21:27 Asia/Shanghai` OC确认准备但未启动。已接受今晚`ctp_joint_test/ctp-transport-20261116/990000000116`虚构账户心跳联测，独立路由全量SCAN空、run_id与双方一致，capture零连接预检通过。T0取OC实际首心跳，至少15秒，保留消息后Relay读取比对；未启动采集器/创建审计库，无真实登录。明早11-17 09:05仅真实只读候选，不设自动任务。见[首轮执行单最新确认](docs/OC_CTP_REDIS_JOINT_RUN_20261116.md#2127-确认今晚隔离心跳明早真实只读)，待OC回实际首尾entry/body，不再往返原则确认。

- `2026-11-16 21:24 Asia/Shanghai` 用户要求尽快Redis联测：两次TEST目标路由全量SCAN空、无hb/group/epoch，尚无OC输出可对接。已完成真实TEST Redis+临时PG单边预演（新模式25心跳/原文/续读/查询分页/失效停止，race无跳过），开发键已清理；不是双方通过。见[首轮执行单](docs/OC_CTP_REDIS_JOINT_RUN_20261116.md)。请OC回实际启动路由/session/epoch及15秒窗口；夜间不登录仅白天可测柜台。Relay四查询可信命令入口仍待接通，不把阻塞全归OC。9092/环境未改。

- `2026-11-16` OC真实只读入口`89fb095`增量已复核：原材料7项SHA、25条心跳与7条reply通过，Relay已适配`test_broker_read_only`解析、诊断、原文审计及采集代次/能力撤销保护。见[复核及下一步](docs/OC_CTP_DONGWU_READONLY_REVIEW_20261116.md)。A已实现未在线验收、B报撤/事件仍待；以下早前“A未实现”记录以本条为准。下一步接独立CTP业务接收与可信命令入口；无柜台登录/命令、迁移、环境切换或服务重启，9092仍华鑫TEST。

- `2026-11-16` 期货终端行情界面已扩至CFFEX/SHFE：左上市场选择、Meridian合约目录补全、价位/乘数、一档SSE、夜盘TradingDay隔离、OHLC分页及开平/手数表单。期货账户与报撤仍未接通，隐藏证券资产误用、保持交易关闭；无迁移/OC动作，仅重启当前TEST API。13项期货JS、11项绩效JS、全Go及桌面/手机浏览器通过，真实行情只读核对保留盘前陈旧/未就绪状态。见[实现与验证](docs/FUTURES_TERMINAL_MARKET_20261116.md)。下一步仍是独立CTP真实只读接线，不因行情已就绪自动开放交易。

- `2026-11-16 20:15 Asia/Shanghai` 用户明确要求当前切测试，已执行`switch-relay-env.sh test --skip-migrations`，9092及域名均test，API3859421/内嵌账本同步正常，生产API3343326/worker3025910停止；不是已部署并行测试或CTP。测试库只读核实1..40，不应用41..43。OC最新心跳已过期、报单准入false，历史心跳报凭据missing，未主动修凭据或发命令。仍沿用旧7x24测试时段，明日仿真需单独调整；生产cron未改，生产任务不能依赖目前的测试9092完成，切回须用户指令，不能自动切换。此前“不切9092”的并行规划不代表当前实际状态。

- `2026-11-16` OC东吴回签纠正当前状态：SDK及离线组件完成，真实查询/报撤/ready运行入口未完成，不能承诺明日直接部署测评。已整理[确认与推进](docs/OC_CTP_DONGWU_READINESS_RESPONSE_20261116.md)，接受`test/ctp_4900/ctp-dongwu-assessment-20261117/300052141`逻辑路由，native UserID/InvestorID仍单独核实；两个CLI零连接预检通过，未建组/库/登记。OC先真实只读再单笔报撤，Relay同步补独立业务接收/命令入口并协调Meridian；不等待原则回签、不启用旧commander绕过。生产及华鑫不变，早前“只待部署/参数”的描述以本条更正为准。

- `2026-11-16` 已整理可直接转OC的[11-17东吴测评准备确认](docs/OC_CTP_DONGWU_TEST_READINESS_20261116.md)，请OC一次反馈部署版本/测评模式false、身份路由、真实SDK四查询与单笔报撤入口、心跳/恢复状态及窗口。OC适配已完成不再重复开发；Relay公开期货交易入口尚未上线，两类验收分开。本文不含密码/authcode，不授权立即启动或交易；下一步据OC回复补接实际TEST运行链路，不改生产。

- `2026-11-16` 联调口径补充：明日华鑫也接仿真柜台，不是旧7x24测试环境；华鑫/东吴均仅各自实际交易时段可测。独立华鑫实例准备时须核对`order_entry_schedule`、去除旧四段准入的误用，并保留OC就绪检查；不改历史回报、不据此启动测试或改当前生产配置。清单及路线图已同步。

- `2026-11-16` 用户补充明日交易时间还做华鑫TEST实盘行情联调。已纳入[两条测试线安排](docs/CTP_OC_JOINT_INTEGRATION_PLAN_20261106.md#同期华鑫测试账户联调)：东吴CTP与华鑫TEST分别验收，9092生产不切换；独立测试API需不同端口/二进制/PID/健康地址及TEST配置，尚未部署、端口待确认。现有switch脚本会停生产，不能用于并行；测试客户端必须显式test base_url，行情实时不保证撮合同步。无启动/柜台动作/权限或计划变更。

- `2026-11-16` 用户计划11-17白天进行东吴CTP测试柜台联调，明确OC已完成适配；目标为SHFE登录及至少各一笔报单/撤单。已更新[入场与执行清单](docs/CTP_OC_JOINT_INTEGRATION_PLAN_20261106.md#11月17日东吴期货测评安排)，实际库须显式测评模式false；优先独立测试进程/路由，不切9092生产。接入资料在`.runtime/ctp-dongwu-test/connection-intake.json`，700目录/600文件且Git忽略，仅资料不是部署配置。尚缺具体钟点和实际运行路由；不复用11-08虚构身份，不把OC开发完成称为Relay端到端验收。离线CLI/契约三包通过，未联网东吴、登录、确认结算、报撤单或迁移。

- `2026-11-16` 盘后账户隔离已修复：原脚本对结算`account_error_count>0`全局退出；现在仅发布“已配置、成功捕获、完整结算”的账户交集，零持仓合法，部分持仓写入、错日期、重复账户、试算/错误快照源及全局错误均不放行。未完成账户仍保留原任务错误和延期日志，不算收益为0或改成成功。22项Python通过，13日真实四户报告只读复验通过，证据`outputs/recovery/post_close_account_isolation_20261116/`；脚本下次cron直接生效，不触发当前任务。见[隔离修复](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#11月16日盘后单账户隔离)。下一步仍待09:01盘前及收盘真实输入，不提前认定今日资金桥闭合。

- `2026-11-16` 自动接续准备已完成：生产cron绩效名单加入768，仍09:01盘前、15:01收盘捕获、17:10起权威复算；安装脚本改为原位替换任务块，保留其他项目环境顺序。实际只读成本预览承接13日24项非零持仓、成本21,098,521.797003元，无前日成本缺口；今日未有收盘快照，不能提前计算或发布今日NAV。16项Python/全Go及跨周末接续回归通过，原暂定/私有清算未核验标记保留。证据`outputs/recovery/fuying13_continuation_20261116/`，见[自动承接检查](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#11月16日自动承接检查)。下一步待今日实际盘前及收盘完成后验收资金桥和新增资金事实；13日失败的500万元出金不自动顺延登记，CSV不作日常条件。

- `2026-11-15` 成本接续与每日策略核对已上线：新增默认只读的`performance-daily-cost-publish`，备份/锁/当前事实复核后仅补存11-13的52行成本，拒绝重复覆盖；独立版本`fuying13-cycle-20261115-v5`追加25日股票、ETF、T0/隔夜、逆回购及残差，支持日期筛选、分页和展开原模型/补证细节。订单167132、成交228457、NAV版本1435及NAV全表内容不变；无新迁移/OC命令，仅API重启。回执在`outputs/recovery/fuying13_daily_attribution_20261115/`，浏览器验收在`outputs/recovery/cycle_review_ui_20261115_v5/`。见[发布记录](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第十一步成本补存与每日策略核对上线)。下一步检查后续正常交易日的成本/资金桥自动承接，未核验清算仍单列，不要求日常CSV。以下条目为历史阶段记录，成本未落库和网页v4等描述以本条更新为准。

- `2026-11-15` 日策略分解已覆盖25日：新增只读CLI `performance-fuying13-daily-review`核对11-11/12正式成本，13日52行仅重放未持久化；封存回款逐源日/委托关联，13日逆回购手续费113.17元由交割/成交证据核实。341笔委托/3211条成交匹配，11-10..13券商金额/费用/市值独立验算与已发布盈亏精确到分一致。`scripts/audit-fuying13-daily-attribution.py --with-continuation`结果`outputs/recovery/fuying13_daily_attribution_20261115/review-v6.json`（v7字节一致）；原14组周期、前21日及线上NAV不变，25日比较后最大差0.047621元，历史未命名资产仍不定性。180项Python、全Go及定向race/vet通过，无金融写入/部署/OC命令。见[完整覆盖](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第十步补齐至11月13日与独立券商验算)。下一步受控补存13日成本、把日策略核对接入独立版本展示；不要求重复CSV，不把本次历史证据设为常规依赖。

- `2026-11-15` 逐日时序核对已上线v4：从3616条封存现金腿重放，分列券商基准盈亏、已知/推定调整、已发布暂定盈亏及未核验资产开收分项，按交易日筛选并分页/展开流水。净额0但应收应付未清仍显示暴露，到账日保留期初；范围外/旧版本未发布不视为0异常。`profit-v13/v14.json`字节一致，14组收益和25日账户结果完全不变，仅追加独立版本，无NAV/账本写入。见[逐日对照与发布](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第八步逐日时序影响与账户盈亏对照)。下一步核对股票/ETF底仓、T0及隔夜成分的每日策略贡献与账户残差；不再重复现金补证，不把时序说明当作最终清算。

- `2026-11-15` 周期核对v3已补接遗漏证据：v2所称9条未分类划转，在11-14封存审计中已逐父单对应券商替代现金，共1151230.50元；不缺新CSV。新增严格身份/流水/金额/周期关联和页面到账明细，11项差额均有解释（9笔现金、2笔次日卖出），但保留原数量及最终性标记，不能把差额解释等同最终清算。`profit-v11/v12.json`重复字节一致，14组收益、费用和25日账户结果不变；完整备份后仅追加`fuying13-cycle-20261115-v3`，无新迁移或NAV/交易账本写入。回执`outputs/recovery/fuying13_cycle_review_20261115/publication-v3-apply.json.commit.json`，见[现金解释接续](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第七步已封存现金解释接续)。下一步为推定现金源日的每日影响和策略归因，不重复核验这9笔现金或索要CSV，不按159900建立通用规则。

- `2026-11-15` 版本化分层展示已上线：绩效页增加“申赎周期核对”，分开显示账户暂定净值、普通费用和每日归因，14组按赎回源日筛选，支持分页与待核验项展开。GET `performance/cycle-reviews`只读PG不可变版本，无CSV/OC/行情实时依赖。完整备份后仅执行43及追加一个核对版本，订单167132/成交228457/NAV版本1435均未变；全Go/race/vet、独立临时PG和正式域名桌面/手机回归通过。发布回执`outputs/recovery/fuying13_cycle_review_20261115/publication-apply.json.commit.json`，浏览器证据`outputs/recovery/cycle_review_ui_20261115/`。下一步处理成分关联/推定来源日的影响范围和每日归因，不重复费用审计，不把周期收益覆盖账户日盈亏或最终性。见[展示发布记录](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第五步版本化分层展示)。以下为历史阶段记录。

- `2026-11-15` 14组周期普通费用已全部接入：复用原分组/成分/日内池/隔夜归属，5,071条成交、2,626个委托均整单闭合；费用按委托只扣一次，连同两笔后续卖出合计38,986.88元。相对v4周期收益合计减少1.78元，10-15的510310组+559.02元，11-04仍+1,134.73元；25日账户层结果不变。全Go/专项race/vet及独立Decimal核验通过，证据`outputs/recovery/fuying13_cycle_review_20261115/profit-v7.json`，无DB写入/部署/柜台动作。下一步接版本化分层展示，并单独解释剩余成分关联/源日/资产范围标记；不得把费用核验升级为最终清算。见[费用接入](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第四步全部周期的普通费用接入)。

- `2026-11-15` 逐委托费用关联已完成：已有成交/交割8,205条费用全部可确定，379组重复键费用向量一致但不冒充原始行唯一；OC对照4,023/4,025笔精确匹配，另两笔0.008/0.006元成交金额差保留不覆盖。10-15余仓次日卖出费用9.65元已接回周期，510310组试算+568.74修正为+559.09元，其他13组不变。证据`outputs/recovery/fuying13_cycle_review_20261115/fees-invariant-v2/`及`profit-v4.json`；无数据库写入/部署/权限调整，线上暂定净值不变。下一步将已核对逐单费用经原有分配链传至其余篮子，再接版本化分层展示；不增加日常CSV依赖。见[本轮核对](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第三步逐委托费用与隔夜成分费用)。

- `2026-11-15` 周期收益第二步已实现：新增纯Go成本/卖出/现金公式与CLI `--cycle-profit`，14组试算均生成；13组保留OC费用/归属估算边界，11-03买入/04赎回159915按原始成本和实际现金复验整轮+1,134.73元。5,900份余仓和123,200份底仓不混入，15日两只成分16日卖出15,666元回接原组、费用仍待核对。父单费用从OC总费用剔除，避免与现金扣费重复。全Go/专项race/vet及独立Decimal核验通过；证据`outputs/recovery/fuying13_cycle_review_20261115/profit-v3.json`。无金融写入或部署，下一步用现有成交/交割替换可关联费用并接分层展示。见[周期试算](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#第二步交易成本与周期收益试算)。

- `2026-11-15` 首步现金周期计算已实现并验收：纯Go按来源日/ETF核对3,616条资金、14组、25日，与已发布暂定余额及盈亏一致；新CSV独立核对通过。9日无所观察时序/分项暴露，区间时序调整合计0，不能据此把私有清算或整个NAV标为最终。全Go/专项race/vet通过，两次报告一致，证据`outputs/recovery/fuying13_cycle_review_20261115/`；无金融写入、部署或权限变动。下一步结合成本/成分卖出完成周期收益及分层展示，见[首步核算](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#首步现金周期与逐日影响核算)。

- `2026-11-15` 用户确认券商无更多导出资料，要求优化统计计算方法；不再反复请求补CSV。下一步按账户净值/申赎周期/逐日策略归因分层，并行核验10-12..11-13的实际收支、待结去重释放及质量标记影响范围。现行暂定算法叠加待结及全区间统一未核验的局限已定位；仅更新计划，未改已发布净值、经济基数或最终性。详见[计算优化](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#11月15日证据边界与计算优化)。

- `2026-11-15` 新增768成交CSV核验完成：11,597行覆盖09-01..11-13，补齐委托/成交编号；8,205条普通买卖及逆回购与交割单多重集合一致，11-13的108个普通委托与OC逐单数量/金额一致。ETF父单与成分共用编号、零数量及重复行保留原义，不能按成交号简单去重；费用关联歧义和资产包含关系仍待核验。仅只读审计，无净值/账本写入或部署，详见[新成交证据](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md#新增成交文件核验)。

- `2026-11-15` 新券商导出修复至11-13：四文件账户全部768，资金/流水48日/47相邻桥通过、12,535条重叠金融事实不变、345条新增。11-10..12修正收盘尾差，13日按用户确认“转出失败”保留全部券商资产，不减500万元、不登记未来出金；日末37,580,098.22元、日盈亏-921,767.05元。25日版本追加、独立回读，旧版本保留；页面新增“交割单列示费用”，13日2,690.40元，不重复扣费或宣称逐单已匹配。全Go/race/vet、临时PG、Python/Node及正式域名桌面/手机验收通过；仅API发布，OC/worker/交易权限不变。详见[新证据与发布](docs/FUYING13_PROVISIONAL_PUBLICATION_20261115.md#新交割单更新至11月13日)。下个交易日若实际转出，再按成功事实登记；旧转出试算命令已禁用。未核实的资产包含及策略归因仍保留，CSV不作为日常依赖。以下为历史阶段记录。

- `2026-11-15` 富盈13号截至11-12连续暂定净值已恢复并上线：24条新版本替代6条旧当前值、补18条缺口，旧证据不删；原始券商总资产及未核验分项保留，10条现金差额候选按源日计入、记账日释放。最近申赎为11-04的159915赎回100万份，之后至11-13无新申赎。全Go/race/vet、临时PG、11项Node及正式域名桌面/手机通过，状态为“暂定/需要关注”，不是最终清算通过。常规rebuild禁止覆盖该审核版，后续正常引擎承接仍保留暂定标记；CSV仅本次纠错，未来不依赖更新CSV。证据、公式、限制见[本次发布记录](docs/FUYING13_PROVISIONAL_PUBLICATION_20261115.md)。下一步独立处理11-13划转与后续日，不再重复截至12日的审计循环。以下条目保留各阶段历史状态，以本条为当前结论。

- `2026-11-15` 非CSV成本恢复已落库：768的11-10末日41项从最近完整空仓起点独立重放92条逐日记录，再正常承接11/12的40/46项，共127条正式成本在完整备份后原子写入并独立回读。正常接口11-12由previous_cost_state_gap变为calculated；11/12数量差均0。计划/执行/提交回执在`outputs/recovery/fuying13_core_origin_20261115/`，CLI重复执行拒绝覆盖。全Go、race、临时PG及vet通过，资金/NAV未写、进程未重启。CSV仅一次性历史纠错，常规输入仍为OC持久账本、Meridian和审核入库记录；下一步解决历史经济资产/资金桥接续，累计净值仍blocked，不能声称已完整恢复至12日。见[独立恢复与证据](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#普通持仓成本的独立恢复)。

- `2026-11-15` 768的11-13划转只读试算完成：新增`performance-fuying13-transfer-preview-20261115`，生产只读repeatable-read核对11-12..15所有类别/状态现金登记0条，原引擎与仅注入账户层-500万元的场景对比，资产/估值不变；归因残差-5000010.80降为-10.80元，账户日损试算-922556.17045元，仍blocked。逆回购11317000元由ambiguous变为推断embedded，非券商独立证明，不再加本金。全Go测试通过，结果`outputs/recovery/fuying13_transfer_20261115/preview-final.json`；无金融写入、发布或进程/权限改动。下一步修正日期精度登记边界：当前零EffectiveAt会补写入时刻，次日overnight路径未识别date精度；不能直接将本次无精确时刻的内存条目落库。10月券商资产分项及历史NAV缺口仍待，详见[划转对照](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#已确认划转的只读引擎对照)。

- `2026-11-15` 划转确认更新：用户先答“大概在13日的中午”，后明确768“是在其中”。因此768的11-13划出500万元至对应期货账户有用户确认，不再以该源账户/金额缺证据阻塞核对。约中午不转换为精确时间或已到账证明；目标期货账号、实际到账及另两源账户仍未确认。下一步只读核对账户侧500万流出是否已登记及与资产变动衔接，产品层按内部划转/在途另行配对，不把余差10.80元猜作费用。正式流水/NAV尚未改；N15及[确认记录](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#富盈13号划转源账户确认)已同步。

- `2026-11-15` 绩效诊断展示已上线：独立“区间净值发布”显示缺口数量/日期，区分试算和暂定发布；“当日资产与资金桥”显示真实质量标记及`unattributed.pnl`，资金对账残差0不再表述为归因闭合。历史专用分支同样检查区间，长说明换行，绩效视图900px以下独立窄屏布局。10项Node回归、全Go及域名桌面/390px Playwright通过，证据`outputs/recovery/performance_quality_20261115/`。仅API发布PID2749324，worker2462706不变、无迁移/金融写入。768原文件SHA未变，仍需[最小券商补证](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md)；11-13的-5000010.80元残差可能涉及此前三笔500万划转，但用户尚未给映射/时间，不能按差额补资金。详见[展示验收](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#绩效诊断展示与发布缺口)。

- `2026-11-15` 旧发布口径核验收口：API六条历史记录的金融字段/完整pnl_components与冻结计划一致，64笔边界尾款全部已在10-28证据中，不能重复宣称新修复。旧算法导出内匹配归零不代表完整结清；10-13的交割处理日分配已与明确源日口径不同，后续需从最早受影响日复核，而非仅续20日。新增[可转券商的最小补证](docs/FUYING13_NAV_MINIMUM_BROKER_EVIDENCE_20261115.md)，只问分项公式/64笔权益包含/10笔源日/利息范围，不再要求重复CSV或OC历史查询。当前等待外部字段事实，不因继续指令默认切换经济基数；详见[源逻辑与剩余依赖](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#旧发布逻辑复核与外部依赖收口)。本轮仅只读核验及文档，无金融写入/代码发布。

- `2026-11-15` 账户层NAV接续：扩展现有`performance-fuying13-cash-timing-20261113 --published-boundary ... --published-boundary-sha256 ...`，离线重验768资金/流水和3606条已关联现金，保留券商总资产、负待付及10条未确认来源，不连DB/行情。64笔边界尾款分20日84925.52元、21日739646.82元；19日“券商总额+已观察现金时序”的条件对照比旧发布额高824572.34元，差额不直接入账。报告`outputs/recovery/fuying13_nav_bridge_20261115/account-bridge-final.json`，两次一致，全Go/race/vet通过。下一步复核10-15/16源日到19日期末的权益包含及旧历史版本，再接20日以后，不能以该差额新增外部资金或认为研究成本通过即已发布NAV。[接续结果与边界](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#账户级接续与旧发布边界核验)。

- `2026-11-14` 768线上资金桥断点已定位：`economic-nav`的10-20..11-13查询0条，`series`19日明确标记`official_performance_nav_unavailable`；10-19仍有旧版`broker_reconstructed_economic_nav.v1`，不是当日缺快照。20日只读preview为blocked，资产基准未应用、实际清算未接入、1270276.025元归因残差；不是缓存或缺Meridian行情。证据`outputs/recovery/fuying13_nav_block_20261114/`。下一步优先核对账户层正式发布候选的券商总资产/源日记账日/待结释放衔接，成本分桶另行验收；不直接rebuild或改确认标记，不突破用户等待分项说明的边界。[原因与修复顺序](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#线上资金桥阻断的实际发布断点)。

- `2026-11-14` 四日成分现金核验完成：离线`scripts/audit-fuying13-component-cash.py`校验封存输入/独立查询和开盘报告SHA，1612条划转核心字段指纹一致；9条159900记录逐父单、同日券商流水和金额匹配，6条另有成交/交割佐证，10-23三条在旧成交文件范围外如实保留。650项数量对平，真实300/500股余仓及次日售出未当现金；报告`outputs/recovery/fuying13_query_evidence_20261114/component-cash-final.json`。源码没有新增证券枚举或生产兼容分支，原cash_substitution=false及失败页不改，正式成本/NAV未发布；源观察27892行未变，全Go及159项Python通过。下一步绑定独立解释与逐项成本语义，详见[证据与边界](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#四日成分划转与替代现金核验)。

- `2026-11-14` 正式成本发布分项预检：`performance-research-source-check --publication-review`复用只读事务，核验22日/972行并列出941 CORE、23申赎成本释放、4余仓、4成分估值行。六表27892行未变，锚点/11条归属及正式表无覆盖冲突通过；8类语义问题仍单列，特别是4日未分类成分划转。报告`outputs/recovery/fuying13_source_preflight_20261114/publication-review-final.json`，不签发发布许可。下一步绑定已完成的重放/查询/现金/605项开盘承接证据，逐条说明成分划转对成本的影响，再开发受控发布及次日读取；原失败回包、券商资产范围和NAV门禁不改，不要求重复CSV。无部署/金融写入，详见[分项预检](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#正式成本分项发布预检)。

- `2026-11-14` 新增`performance-research-input-replay`：显式输入目录/SHA，仅使用封存记录和本地源码，无配置/DB/行情客户端。核验完整manifest、SHA/序号/次数、83份引擎依赖源码，固定时钟运行22日成本与归因，972行及隔夜预览输出一致；67次承接使用新计算成本，不能用旧响应掩盖差异。并发快照仅在连续批次内按完整参数匹配，主数据批量代码仅规范集合顺序，日期/分页/重复数量不放宽。证据`outputs/recovery/fuying13_engine_capture_20261114/offline-replay-1.json`，详见[离线验收](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#封存输入离线重放验收)。下一步逐日查询完成覆盖和preview-only正式成本语义审查；辅助原始SQL未独立重放，资产范围/NAV限制不解除。生产环境/进程/权限不变。

- `2026-11-14` 实际输入采集与成本复算完成：CLI新增`--capture-inputs`，只读repeatable-read、固定计算时钟、记录类型化数据库/行情请求响应及错误，拒绝金融写入。10-12..11-10的22日/972行与研究v1业务差异0，仅时间元数据不同；归属使用已落库审计，不再叠加旧fixture。证据`outputs/recovery/fuying13_engine_capture_20261114/run-1/`及`run-1-result.json`，manifest完整、1234条序号/字节/SHA核验通过；22次逆回购规则不存在保留原始错误，Meridian调用无错误。全Go/race/vet及冻结链测试通过，服务/权限不变。下一步实现封存输入的离线重放并逐日核对查询完成证据，不将本轮输入冒充旧版历史输入，不放开正式成本/NAV发布。[采集与结果](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#实际输入采集与成本复算)。

- `2026-11-14` 新增`performance-research-source-check`：显式账户/研究版，读库只读repeatable-read，按表/日保留完整行多重集合SHA与条数，基准绑定环境/数据库/正文指纹；新增、删除、同条数修订可定位最早复核日。768两轮六表27892行一致，期初/活动归属也未变；证据`outputs/recovery/fuying13_source_preflight_20261114/observation-{1,2}.json`。旧版只存计算/连续性报告指纹，缺当时完整订单/成交/持仓/行情输入清单，今日观察不能补造历史证明。全Go/race/vet/隔离PG通过，无服务或金融写入。下一步在现有数据上只读重放并归档完整输入及Meridian响应、核对查询完成证据，再比较新旧研究成本；不要求用户重复导出CSV，不跳过券商总资产分项门禁。[预检细节](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#研究成本源账表观察与发布缺项)。

- `2026-11-14` 独立[研究成本页](http://relay-trader.quantstage.com/trade#research)已发布：账户/版本必须显式选择，不默认latest；日期/证券/成本类别/状态筛选、排序、分页和当前筛选全量CSV，缺失不补零，审计变化单列。API与CLI复用只读repeatable-read读取，不调用正式计算或行情。全Go/race/vet、隔离PG及桌面/手机Playwright通过，生产验收证据`outputs/recovery/research_cost_web_20261114/production/`；仅API更新为PID1972284，worker仍PID2462706。新API可解码日期精度v2，但研究版未接正式任务，preview-only持久化保护保留。下一步补研究版本源事实新鲜度与正式成本发布预检；券商总资产基准不改，资产分项未说明前不发布768正式NAV。详见[页面与部署边界](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#研究成本终端与只读接口)。

- `2026-11-14` CLI `performance-research-cost-compare --account ... --version ...`已验：22日/972行的研究/正式成本对照、当前期初与活动归属校验，3次实读一致、175..224ms。缺失不按0计算差额，研究正文不覆盖；JSON/CSV在`outputs/recovery/fuying13_cost_compare_20261114/final.{json,csv}`，现已由上述独立页面复用。[明细与边界](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#逐日研究成本只读对照)。

- `2026-11-14` 受控研究导入已执行：`performance-fuying13-cost-import-20261114`默认只读，显式apply检查备份/数据库/固定正文与迁移SHA/完整锚点及11条归属payload。生产只新增000042及版本`fuying13-cost-research-20261114-v1`，22日/972行回读一致，重复apply0新增，1..32+42之外无登记；API/worker未重启，旧在线v1读取不变。备份`outputs/backups/relay_trader_20261114T092728+0800.dump`，证据`outputs/recovery/fuying13_cost_import_20261114/`；[执行记录](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#生产独立归档执行结果)。下一步接显式版本的只读成本对照与修订失效提示，不能自动喂给正式NAV；资产分项仍待券商说明。

- `2026-11-14` 研究成本版本：独立不可覆盖存储，保留期初锚点、逐日完整成本、归属审计、输入指纹和结构复核；显式账户/版本回读并重新核验，无自动latest或NAV发布接线。临时PG复验768冻结22日/972行、605次承接、25项转移与1项微元尾差；旧版本回读、并发冲突、事务回滚通过。下一步补受控导入/检查入口和独立迁移方案，再部署研究读取，不能用常规全量迁移顺带上线待验CTP表。详见[实现与验收](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#研究成本版本存储与回读验收)。

- `2026-11-13` 剩余归属审计落库：10-23三条库存证明沿用冻结报告中的真实审计时间；11-03隔夜100万份按11-11用户确认日期存`relay.t0_allocation.v2`，不造具体时刻。新核对出源日7300份底仓09:31:22售尽，10:57:15起另买100万份，次日赎回后123200份底仓独立。`performance-fuying13-remaining-audit-20261113`备份后事务追加4条、回读/重放通过；22日成本复核未落库link由4降0，历史金融表指纹不变。证据`outputs/recovery/fuying13_remaining_20261113/`，详见[执行记录](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#剩余四条归属审计落库与日期精度)。全Go/race/vet/独立PG通过，API/worker未重启，旧v1读取不消费v2。下一步是研究成本版本化持久化、完整读取链及受控上线，不再重复补这4条；正式NAV仍待券商资产分项说明。

- `2026-11-13` 新增只读`performance-fuying13-cost-continuity-20261113`：冻结v12成本报告与生产10-09成本起点独立核对，22日/972条、605次承接无断点；23项成本转出不是亏损，2项成分余仓15944元是估值基数而非实际买入成本。10-23的159949保留0.000001元六位小数差异，`exact_cost_identities=false`，不改账平差。数据库回读定位4条尚未持久化归属（10-23三条、11-03一条）；下一步先稳定确认时间/证据身份并复核落库，再处理研究成本的正式持久化验收。全Go/race/vet及实际重复核验通过，证据`outputs/recovery/fuying13_cost_continuity_20261113/`；详见[连续性核验](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#成本连续性独立核验与剩余持久化缺口)。本轮无DB写入/柜台动作/部署；正式NAV仍等券商分项说明。

- `2026-11-13` 富盈13号10-15补单归属已独立落库：20,200+979,800份对应100万份159869赎回，追加3条审计，不改原始订单/成交/金融账表。新增`performance-fuying13-split-audit-20261113`默认只读，显式写入需新备份，事务锁/冲突拒绝/幂等及历史表指纹校验；全Go、定向race/vet和独立PG原子回滚测试通过。备份`outputs/backups/relay_trader_20261113T184748+0800.dump`，执行/回读/重放证据`outputs/recovery/fuying13_split_20261113/`，见[落库记录](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#10月15日已确认补单归属独立修复)。下一步推进成分余仓/日内池/隔夜成本的独立持久化验收；券商总资产与正式NAV口径不切换，资产分项仍等券商说明，不因本次归属落库解除阻断。生产权限/进程不变。

- `2026-11-13` 用户选择“继续以券商总资产为基准，等待分项说明”：冻结此前剔除展示分项/独立现金资产方案，不因对平切换口径。新增`ReviewETFCashTiming`及离线`performance-fuying13-cash-timing-20261113`，3606笔/21日与Python原审计独立核对一致，10笔候选不参与；10-22明确待付652.34元不截成0。相关两Go包全量及定向race通过，未接正式计算/写库。证据`outputs/recovery/fuying13_cash_matrix_20261112/native_cash_timing_20261113.json`，见[用户选择与现金桥](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#用户选择券商基准与go现金桥)。下一步等待券商解释总资产分项；不再提替代基数为默认方案，不补造源日或清算完成。

- `2026-11-13` [N15产品级多账户绩效](docs/ROADMAP.md#n15-产品级多账户绩效)仅规划：产品归属有效期、股票/期货内部划转和在途、权益汇总及产品NAV；优先完成N13富盈13号，不做产品迁移。11-13各500万元的三笔划转尚缺账户映射/时点，未写正式流水。

- `2026-11-13` 768新增`component_basis_preview`，逐日验证券商总额=客户资金+市值+逆回购+展示项；用实际正展示项对照而非撤销负现金叠加，16/19日专属12688.20残差不再出现，剩余-4671.92/+7423.35来自邻接分项。全展示项排除仅敏感性、21日算术对平不确认源日/范围；不发布。报告`outputs/recovery/fuying13_cash_matrix_20261112/component_basis_20261113_v2.json`，详见[分层对照](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#11月13日报表分项与现金资产分层对照)。新增7项、148专项通过；下一步确认经济基数范围后接正式账表，不再重复确认16日业务关联。

- `2026-11-13` `audit-fuying13-confirmed-overlap.py --combined`合并利息桥、现金费用与10-16去重，报告`outputs/recovery/fuying13_cash_matrix_20261112/combined_inclusion_20261113_v2.json`。13日条件对平、8日残差集中五组资产分项；剥离分项变动后仍有16/19日正负12688.20，说明上轮“已含扣款=已按负应付净入资产”的解释不能直接发布。详见[统一复算](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#11月13日统一复算与符号检查)。下一步核实客户资金/正分项/总资产公式，不再重复问是否包含扣款、不重导普通流水；源日/其他分项/利息范围仍独立检查。新增8项，141专项通过，无数据库写入/柜台动作/部署，正式NAV不改。

- `2026-11-12` 用户答复CSV应覆盖10-16后，重新核对账户/四文件哈希及4060条余额链；交割第103..105行记16日，实际现金扣付19日，资金结余一致。报告`outputs/recovery/fuying13_cash_matrix_20261112/oct16_csv_recheck.json`，详见[重新核对](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#用户提示csv覆盖后的重新核对)。不缺10-16文件；需的是+12688.20等额外资产分项说明及源日关联，不要求重复导出普通流水。本次用户答复不代表批准删资产/确认源日。前轮[费用尾差](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-12-普通成交与现金费用尾差核对)及[逆回购桥](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-12-逆回购计提与到账衔接)证据保留，均未发布正式NAV；无金融写入/柜台命令/部署。

- `2026-11-11` 新增`performance-fuying13-cash-review-20261111`及`-broker-cash-preview`，只读核对冻结券商文件、108相邻现金日、7笔逆回购扣款/到期返本及159915隔夜现金链。实际整轮收益1134.73元（含源日佣金120.37、后续过户费13.04和现金差额3376.95），区别于IOPV模型-782.35元；123200份CORE未混入。`cost_chain_cash_v12.json`的22日数量/归因缺项为0，6日逆回购费用缺项在内存清除。下一步处理10月多轮已入现金去重、跨日清算及经济NAV，不能将当前局部闭合直接发布；数据库写入0、柜台命令0，页面旧结果未改。复现及证据见[现金核验](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-11-实际现金与逆回购费用核验)。

- `2026-11-10` 已完成16日日内成本池只读验证：7笔买单、4笔赎回与券商历史成交/交割单一致；期初期末零、逐时库存不负、全部同ETF的etfarb标签，无显式策略ID冲突。`-closed-day-pool-preview`只启用10-16，依赖两个既有非持久化预演开关；保留原父单、逐次IOPV和后续现金来源，不猜FIFO或逐轮盈亏。159915当日T0暂估贡献13,427.80元，不是整日或最终收益；成本链通过至22日，停在23日的159869补单/底仓混合，不自动推广成本池。下一步先解23日成本，再核对跨日待结与资金桥，不能凭局部通过发布净值。详见[16日解决记录](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-10-16日日内闭合成本池预演)。

- `2026-11-10` 富盈13号成分余仓只读预演完成：`-confirmed-split-preview -component-residual-preview`通过10-12..15的成本数量核验，10-15余仓估值15,944元从父篮子待结暂估中扣除，源日不重复计利；10-16两只股票全部卖出，增量损益暂估-287.650256元。全日仍被159915多轮买入/补单与分批赎回的归属阻断，不能以局部成功发布NAV。`159900`无收盘持仓的划转保留未分类，不造股票或退款。下一步核对10-16跨轮分配，再推进现金包含关系与连续净值；研究开关默认关闭、禁止持久化，现有文件足以继续。详见[成分余仓预演](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-10-成分股隔夜估值与跨日卖出预演)。

- `2026-11-10` 已完成387/388的11-04实际结算及11-04..10连续成本/NAV重建。单轮T0净贡献分别41,940.16/45,339.02元，不是账户整日收益；待结净权益按3,363.91 -> 3,376.95 -> 0释放。写前完整备份、只读预览、事务复核、PG/域名接口回读及重复执行拒绝均通过。证据在`outputs/recovery/actual_settlement_20261110/`，详见[实际结算发布记录](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-10-两户实际结算与连续净值发布)。下一步仅推进768的三轮赎回退款去重、5,900份余仓成本及跨日NAV，不再等待387/388补资料；券商导出仍为一次性修复证据。

- `2026-11-10` 用户授权按券商导出修复资产基数，已通过一次性`performance-broker-basis-20261110`写入29条资产基准和29条金标；写前完整备份`outputs/backups/relay_trader_20261110T190405+0800.dump`，SHA256 `829dea767ede72402b0b57b59bf93a4b2bca7fa8a199098c1ee516be0cf338e0`。证据、预检/写入准备/回读见`outputs/recovery/broker_refresh_20261110/asset_basis_*`。此阶段`broker_asset_base_authoritative=true`，`economic_nav_base_confirmed=false`，并非正式净值发布；不能填0待结权益或直接重用通用资金导入将全部其他资产当逆回购。下一步消费这些已确认基数，核对ETF跨日权益释放、完成T0余仓成本与连续NAV预览，再单独版本化发布。详见[执行记录](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-10-用户确认券商资产权威后的落库)。

- `2026-11-10` [新券商导出复核](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-10-新导出与更正后的复核)：先发现768文件实际属于830，用户更正后重新校验，当前768两文件身份正确；错误版与正确版分别冻结在`outputs/recovery/broker_refresh_20261110/source_evidence/`及`corrected_768/`，不能混用。387/388各70个相邻资金日、768共108个均对平。387/388的11-04 PCF/98股划转卖出/后续99笔流水闭合，暂未发布。768的10-13退款297笔正好覆盖三轮99只深市成分，可作账户级证据；总资产仍有未解释分项，详见[券商范围问题](docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md)。本轮没有修复正式净值，不因文件到账而解除余仓/同日现金preview-only门禁，也不把人工导出变为常规任务。

- `2026-11-09` 用户确认债享5号遗留订单为算法未撤、柜台收盘自动终结。已发布trade_quality.v8：生产历史查询对数量一致的部分成交余量作跨日日终失效投影；4笔非终态/4异常降为0，另列partial-expired=4，原始订单、成交、费用和实际撤单统计不改。TEST/当日/CTP不套用新增规则。全Go/race/vet及JS检查通过，仅更新9092 API，独立worker不变、生产下单仍关闭。详见[同日复核记录](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#用户确认后的历史日终失效处理)。

- `2026-11-09 23:16 Asia/Shanghai` 债享5号830“订单与成交账本”标红再次核实：默认10-09..11-09共16051笔订单/18040笔成交，4笔历史部分成交缺终态；各笔成交数量一致，OC盘后reply原文也非终态，未发现Relay丢终态。区间22日NAV均provisional而非整体计算失败。现有830券商文件仅到09-21、委托文件实际为成交，缺10-16/10-20/11-02终态证明；保留质量告警，待OC日志或真实历史委托核对，未改订单/净值/权限。定位与所需证据见[复核记录附录](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#附债享5号4笔状态证据)。

- `2026-11-09` [绩效复核与余仓开发](docs/PERFORMANCE_BLOCK_REVIEW_20261109.md)：四条T0归属审计已存，余仓成本/按日归因仍preview-only。新增券商同日赎回款包含关系审计和去重估值预览：六笔3802876.20元、现金余额链及按证券/金额/重复数量多重集合一致；逆回购实际利息1306.48、佣金380.46。券商日终基数试算10-12收益-4378.405864元、归因残差-325.217114元，非正式收益且不把容差当结清。证据及结果在`outputs/recovery/fuying13_same_day_receipts_20261109/`，一次性审计脚本及重跑说明见报告。下一步逐笔核对10-13退款、10-14现金差额与剩余待结权益，滚动闭合后再备份重建；未写金融账表/修改cron/权限/运行进程。

- `2026-11-08 22:07 Asia/Shanghai` 按用户指令执行`switch-relay-env.sh production --skip-migrations`切回生产，域名/API/独立worker正常；4查询账户、0交易账户，16条stream健康、lag和pending DLQ为0，4网关正常休市。未改账户权限、未执行迁移、未主动报撤单或重启OC；CTP联调仍未启动，原时点路由检查不代表已预留。

- `2026-11-08 12:48 Asia/Shanghai` 接受OC首轮隔离路由，只读完整SCAN空、前后run_id一致，inspect默认零连接参数预检通过；无Redis写入/PG采集/环境或服务变化。已整理[路由与首轮边界确认](docs/OC_CTP_JOINT_ROUTE_CONFIRMATION_20261108.md)：OC仅恢复/未登录心跳，新查询运行入口仍待；先约定SourceID/epoch/consumer/T0，窗口结束后读取保留原文并比对，不能把capture超时视为15秒连续采集。原始检查工具留在本地`.runtime/ctp-joint-preflight-20261108.go`，非运行服务组件。

- `2026-11-08 09:11 Asia/Shanghai` 按用户指令通过`switch-relay-env.sh test --skip-migrations`切回华鑫测试环境，生产API/worker停止，测试API与内嵌账本同步启动。域名确认test、00030484启用，Redis/DB正常、4条stream健康、lag及pending DLQ为0；OC心跳缺失、报单未就绪，API为degraded。未改账户权限、未发报撤单、未重启OC或执行迁移；测试保留000040，生产下单仍关闭。后续需OC新鲜心跳才能确认测试交易可用。

- `2026-11-07 21:01 Asia/Shanghai` 按用户指令切回生产，API/独立worker及域名验收正常；4个查询账户、0个交易账户，休市状态正常，lag和pending DLQ为0。切换使用显式`--skip-migrations`，确认现有A股运行链路不依赖CTP表，生产schema仍为000032；未部署CTP消费入口或执行000033-000041。新增3项隔离脚本测试，默认迁移行为不变，同时覆盖YAML列表首行的生产交易权限保护。后续环境切换仍须用户授权；CTP联测另行确认窗口。

- `2026-11-07` 为下一轮两端联测补`HeartbeatEvidence`及`relay-ctp-capture --audit`：PG只读repeatable-read分页、显式owner/cursor锚点、元数据/尾部/顺序/SHA复核、跨Stream游标拒绝、8MiB页预算；默认不输出正文，include-raw无损保存坏UTF-8及空body。新增5项PG，扩展实际CLI子进程复验，17项相关Go/race通过（19.226秒、无跳过），3项CLI/race、103项Python、七包/cmd/vet通过；日志`.runtime/ctp-evidence-regression.log`。未重跑上轮万笔容量，无迁移/部署/环境或权限变化。已向用户询问当前OC隔离gateway/account/采集窗口；收到后按[联合验收步骤](docs/CTP_OC_JOINT_INTEGRATION_PLAN_20261106.md#心跳证据比对)实施，不要求新的wire、源码或二进制。

- `2026-11-07` 独立`relay-ctp-capture`接入既有000040，transport-only hb可无native身份留原文；默认零连接，显式写TEST审计，目标数据库名/DSN/实际连接三方一致。新建不抢已有owner，重启须停旧读者并确认旧owner/cursor，行锁CAS防止并发同令牌接管；分页、退出、输出失败保留已提交证据。新增1模型/6PG（3项真实开发键，含实际CLI）/3CLI护栏；最终非万笔211项Go/race通过（81.305秒），万笔订单/成交恢复单项通过（299.39秒），合计212项（125项PG）分批覆盖；103项Python、七包/cmd/vet通过。日志`.runtime/ctp-capture-{final-regression,regression,cli-targeted}.log`，首轮CLI夹具DSN修正后已重验，不将首轮整文件称为通过。临时PG已销毁、开发键清理无报错；无新迁移/运行库写入/服务重启/环境或权限变化。下一步固定OC路由与窗口，做真实hb输出到PG的联合比对，无新wire或Meridian需求。见[用法与边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md#11-07-独立持久心跳采集入口)。

- `2026-11-07` 新增独立 `cmd/relay-ctp-inspect`，默认只检参数，`--run`才用专用环境变量连接TEST Redis读取指定hb单页。展示原文SHA、顺序、恢复状态与+08:00时间；原生身份/代次不猜填，业务授权固定false，不落库、不接管消费者、不发OC命令。39项相关Go/race（22.216秒）、5项CLI/race（1.052秒，含真实随机开发键）、103项Python、七包/cmd/vet通过；日志`.runtime/ctp-inspection-{regression,cli}.log`。未重跑上轮10,000容量全量，未部署/迁移/切环境/改权限。下一步规范隔离路由的持久采集运行入口及两端联测；[用法与边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md#11-07-独立只读心跳诊断入口)。

- `2026-11-07` 新增`TestHeartbeatWindow/RunHeartbeatWindow`及000041：显式审计边界、持久逐条观察、提交后更新guard；独立hb读取与时效取消接入业务消费，退出先回收协程再关客户端，完整查询历史保留。新增8项PG测试（2项含真实TEST开发键）；全量200项Go（119项PG）/race通过，398.731秒、无跳过，含10,000订单/成交恢复。最终退出顺序和+08:00审计调整另以24项定向race复验，18.388秒；103项Python、七包/cmd/vet通过。日志`.runtime/ctp-heartbeat-window-{regression,targeted}.log`，临时PG已销毁、开发键清理无报错。仅内部原型，ReviewID/会话/代次须独立核验，当前OC stage1不能用假日期运行业务guard；下一步独立只读入口与规范路由联测。未部署/应用运行库迁移/切环境/改权限，API仍原华鑫TEST进程802957。详见[边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)。

- `2026-11-07` OC新包`oc-ctp-runtime-stage1-20261107.tar.gz`与8项清单已核验，原文归档`testdata/runtime_review`。新增未登录恢复心跳适配，null不填假值、顶层镜像一致性校验、TEST范围及全false门禁；34条原心跳、原物理查询页与临时PG恢复通过。新增3项Go模型/2项PG、6项Python；28项相关Go/race（12.531秒、无跳过）、103项Python和七包/cmd/vet通过，日志`.runtime/ctp-runtime-stage1-review.log`。见[给OC的复核与分工](docs/OC_CTP_RUNTIME_STAGE1_REVIEW_20261107.md)：不等于真实SDK/四查询就绪，不重跑OC程序，不要求重复原则回签。下一步持久观察、新窗口及规范隔离路由联合验证。未部署/切环境/修改权限，原用户CSV与旧交付未改；上轮容量全量187项结果保留，本轮未重跑。

- `2026-11-07` 复用现有StreamStore增加hb原文/位点原子持久收件，新增`HeartbeatAudit`分页保留断开、坏报文、隔离及重复历史，不更新guard或标记delivered。新增5项临时PG测试（2项真实TEST Redis开发hb键）；全量187项Go（109项PG）/race、97项Python、七包/cmd及vet通过，385.1秒、无跳过。日志`.runtime/ctp-heartbeat-audit-regression.log`，开发键清理无报错、临时PG已销毁。下一步连接持久观察与新消费窗口，再接独立身份/连接代次证据，不能把恢复原文当恢复在线权限。无新wire/迁移/部署/OC命令，原API进程802957及TEST配置不变。详见[实现边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)。

- `2026-11-07` 新增`DecodeHeartbeat/TestSessionGuard/RunGuarded`：固定已知会话/日/来源，重复心跳不续期，过期/断开/变更取消当前消费，完成历史保留；仅内部TEST接线，无真实hb读取/审计、服务发布或权限变化。共享hb没有connection_generation/native登录身份，不从其推断重连代次；下一步依据OC运行稿补持久入口及独立身份门禁。[边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)已更新。新增9项模型/2项PG+Redis测试，全量182项Go（104项PG）/race通过，375.2秒、无跳过；97项Python、七包/cmd及vet通过。日志`.runtime/ctp-heartbeat-guard-regression.log`，开发键清理无报错、临时PG销毁；API仍原TEST进程802957，未重启或发OC命令。

- `2026-11-07` [OC Redis交付复核](docs/OC_CTP_REDIS_DELIVERY_REVIEW_20261106.md)：用户补齐`testdata/redis_review`；指纹/三组37场景记录及原分页解码/PG复验完成，材料缺口解除，不等于重跑OC/在线SDK。补齐清单所需的同SHA正文路径，收到的文件未改。安全/别名轨迹粒度和物理路由边界已记录；ACK不确定仍阻断。新增2项Go/5项Python测试，无业务代码/环境/权限/服务/Redis操作。下一步真实心跳、会话与规范隔离路由的两端联合验证，不需要再索要同一材料或Meridian改动。

- `2026-11-06` 在既有分发/联合质量检查上补`TestStreamSource/TestConsumer`：固定来源配置与专用客户端，单次最长5分钟读取/分发/应用/超时循环及空闲退避；错误停止、不重报命令。新增`QueryStore.Retire`，停止仅中断未完查询，完成历史保留但退出当前快照。9项新增测试（7项PG，其中4项含真实TEST Redis开发键）已通过。[实现边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)明确这是TEST内部原型，配置指纹不是服务器认证、窗口不是OC心跳租约；下一步真实心跳/会话撤销和独立只读入口。无新迁移/服务重启/环境或权限变化，用户CSV与OC材料未改；无需OC重复回签。

本轮169项Go（101项临时PG）/race通过，含10,000订单/成交恢复，391.0秒、无跳过；92项Python、七包/cmd回归及vet通过。日志`.runtime/ctp-test-consumer-regression.log`，测试Redis开发键清理无报错，临时PG已销毁。运行服务复核仍为原TEST进程802957，没有重启或发送OC命令。

- `2026-11-06` 按用户授权直接使用TEST Redis8.2.2，新增8项Redis+临时PG及2项模型定向race通过：位点/接管、二进制原文、重复字段、未读裁剪/删除、客户端重建、提交失败及查询两页交接/中断。修复普通XRead将重复body折叠成map的风险，改用专用RESP2数组严格校验；无新wire/OC/Meridian整改要求。[实现边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)及[复现命令](tests/README.md)已补。测试仅随机`relay:test:v1:ctp:relay-dev-*`键，结束精确清理并有TTL，不操作账户业务流/共享Redis配置；PG仍临时实例。环境/服务/交易权限未动，CTP常驻消费者未上线。下一步可信客户端/会话/请求分发、统一质量门禁及保留配额。

前轮传输验证：151项Go（85项临时PG）/race、92项Python通过，包含真实TEST Redis和10,000订单/成交恢复；七包/cmd回归及vet通过。开发键清理无报错，临时PG已销毁，不代表OC在线交易已验收。

- `2026-11-06 21:12 Asia/Shanghai` 用户授权切到TEST：已核对测试/生产数据库名称和Redis端点不同，标准脚本重启9092成功；测试API/内嵌worker健康，4条业务Stream lag=0、待处理DLQ=0，但华鑫OC缺心跳，不能据此宣布柜台可用。现有TEST交易配置未改，未主动查询柜台或报撤单。测试库迁移32-40不等于CTP运行接入。[OC联合联调计划](docs/CTP_OC_JOINT_INTEGRATION_PLAN_20261106.md)明确先隔离恢复，再获授权只读，最后受控交易；下一步Relay补真实隔离Redis验证和可信接线，OC继续dispatch/outbox/ACK。生产实时消费暂停，cron保持原配置，恢复生产必须等用户新指令。

- `2026-11-06` 新增`StreamStore/ReadOnce`及000040：原body/待处理责任/位点原子落库，显式接管隔离旧writer，按已登记目标原子移交query/event；未知身份不造单，坏报文保留，旧查询半页不复活。新增3项模型+9项临时PG/race通过，详见 [恢复与运行边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)。当前Redis只用客户端替身，无实际Redis服务端测试或常驻进程；接收原文预算是每Stream累计50,000条/64MiB，不每日清空，保留/统一质量门禁未完。下一步真实隔离Redis恢复与可信分发/扫描调度，不上线生产。OC上一轮27条原body已验收，见 [回执](docs/OC_CTP_TRADE_DELIVERY_REVIEW_20261106.md)，继续其dispatch/outbox/命令ACK；Relay沿用XREAD，不创建消费组/XACK。无部署、生产迁移、柜台/业务Redis调用或环境/权限变化，用户CSV/附件未改。

- `2026-11-05` 新增`submit_command.go/event_store.go`和迁移000036：单笔/批量命令原字节及子单身份原子登记，不发送命令、不造实际订单；完整事件先持久收件再事务应用，核对原命令及经济字段，未知本地身份等待，错误/冲突留证阻断，游标扫描支持重启补处理。新增4项模型+10项真实PG测试，累计84项Go含38项集成/race、七包回归/vet与83项Python通过，详见 [实现边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)。下一步查询请求登记、查询/事件统一身份门禁，然后消费位点/ACK；当前没有Redis消费者/发布outbox/交易准入，也不把调用方信任替代真实来源认证。Meridian标的定义不变，无生产迁移/发布/环境/权限变更，用户CSV未动。

- `2026-11-05` 新增`internal/futures/query_store.go`与迁移000033：资金/持仓原文、批次状态、完整候选和发布指针在同一事务写入；晚到旧查询只留历史，冲突撤销可用性，旧接收进程退休后不能再发布，失败刷新不清空旧快照。12项真实PostgreSQL集成测试（含提交前终止数据库连接、16路重投、环境/账户隔离及up/down）和26项原Go测试在本机临时库以race通过，83项Python通过。复现入口`scripts/test-futures-postgres.sh`，详见 [持久化边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)。生产库/服务/Redis/权限未动，用户暂存CSV保留；下一步订单/成交Go模型与持久身份/待关联，再接可信请求登记、消费者位点/ACK，不重复原则回签。

- `2026-11-05` OC与Relay并行开发，不再新增原则回签。Relay在`internal/futures/query_batch.go`实现资金/持仓完整reply的严格校验、有界乱序收页、原字节重投、冲突及超时门禁和完整快照候选；14项新增测试覆盖三页六种排列、失败不归零、跨页重复、会话隔离、容量及独立副本，详见 [实现边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)。候选不代表落库/ACK/readiness，当前只在内存，下一步实现独立持久批次及原子发布、崩溃恢复。未接服务、迁移/部署/切环境/修改权限，原共享JSON和用户暂存CSV未改。

- `2026-11-05` 已核对 [OC逐ticket查询候选交付](docs/OC_CTP_QUERY_CANDIDATE_DELIVERY_20261105.md)：答复与四JSON指纹一致，OC报告原始应用按ticket固定及资金/持仓标准候选实现；材料缺口解除，不冒称Relay运行OC测试或在线验收。按用户提高协作效率要求，无新阻塞就直接开发，不另出原则回签文档。Relay实际新增`internal/futures`资金/持仓Go类型与完整items解码，详见 [当前实现边界](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md)：12项测试/race/vet/五包回归通过，83项Python通过。未接业务消费/API/SDK/DB，原协议JSON、环境、交易权限及用户暂存CSV未改。下一步OC继续完整命令/意图编码/有限分页，Relay推进批次/持久层隔离；只反馈实质差异和必要验收证据。

- `2026-11-05` 完成 [OC r1回签答复](docs/OC_CTP_R1_SIGNOFF_REVIEW_20261105.md)，[原文存档](docs/OC_CTP_R1_SIGNOFF_20261105.md)保留OC证据边界；r1 SHA与OC一致且不改原文。独立转录/核对28个金额输入、59项字段/舍入预期，另2048有限值往返通过；不冒称验证OC运行时fenv或厂商哨兵。查询schema材料版2明确未入队ticket/generation可null、失败不填假日期；补原完整输出按旧上下文历史恢复/新请求拒收旧半页、四类账户级当前日查询、原生持仓枚举。总83项Python/Go四包通过。下一步按OC顺序推进逐ticket固定结果、标准转换、完整意图/待关联/持久输出，真实代码证据与容量尚待，不关闭F0；无部署/切环境/DB/权限变更，原六JSON与用户CSV不动。

- `2026-11-05` 已整理 [给OC的统一对接稿](docs/OC_CTP_UNIFIED_REVIEW_20261105.md)，替代分批转交内部草稿；用户尚未转交，下一步接收C1-C6一次性回签并按差异修订，不自动宣布F0冻结。新增三类交易命令字段与保守订单状态合并校验，16项新测试+53项既有测试共69项通过，Go四包通过。首期未关联传输选择保持wire必填gateway：OC自身未知则持久暂存/查询明确不完整，Relay本地未见订单则独立pending不造单；此责任需OC确认。原JSON/schema不变，未改运行服务、账户权限、环境或数据库，用户暂存CSV保留。

- `2026-11-05` 完成 [成交关联与隔离账本内部设计](docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md) 及18项离线关联测试，总53项Python/Go四包通过。区分执行事实、关联、费用证据与原始审计；query schema不变，缺gateway仅为内部pending测试，不是已升级wire。按用户要求继续暂缓OC文档转交，统一稿需明确未关联传输、完整命令、状态合并及金额/恢复待确认项。未迁移/发布/切环境/改账，四份用户暂存CSV不纳入提交。

- `2026-11-04` 已补 [查询序列内部验证](docs/CTP_QUERY_SEQUENCE_REVIEW_20261104.md)，18项序列+17项单页测试及Go四包通过；原六份共享JSON和查询schema不变。ready仅是完整候选，不是原子落库/ACK；未关联成交、真实恢复和HTTP/SDK/DB矩阵仍待实现。按用户要求，前轮及本轮新增文档暂不转给OC，先收敛完整对接稿再通知；不宣布F0冻结。未切环境/部署/改权限/改账，用户已暂存的reference资金CSV不纳入本提交。

- `2026-11-04` Relay已补 [CTP四类查询页契约](docs/CTP_QUERY_PAGE_CONTRACT_20261104.md)：严格JSON整数/金额字符串、账户与交易日、原生ID/数量、空结果/失败回包和页内重复检查；新增两份完整页，17项Python测试及四Go包通过。依赖仅在`.runtime/ctp-contract-venv`，运行服务/SDK不改。schema是待OC复核候选，不是完整分页/落库验收；继续补序列和HTTP/SDK/DB映射，金额哨兵共同向量仍待补，未登录/发布/迁移或改历史绩效。

- `2026-11-04` 用户已交Meridian推进SHFE行情，明确CFFEX实时链路长期运行；Relay撤销原先以Trader完整样本阻塞行情的依赖，见 [边界修正](docs/MERIDIAN_SHFE_MARKET_BOUNDARY_UPDATE_20261104.md)。新增目录代理反例验证权威行情来源可无柜台量边界/Trader证据透传，未自行发布目录或开放交易。本轮先处理此新边界，未关联成交/重复终态共同样例仍为下一项，未标完成。

- `2026-11-04` 已确认Meridian收口文档，M1/M2/V1/V2离线accepted、M3 pending_authorized_sample，无新增技术整改；“账户级下单量”仅按柜台合约静态量边界解释，不代表账户权限。按用户要求修正 [OC确认文档](docs/OC_CTP_DRAFT2_ACCEPTANCE_REVIEW_20261104.md)：只转协议/样例/证据，不需要二进制或源码包，旧包仅历史备用。下一步仍为金额协议材料、完整wire/恢复及获授权SHFE样本；未登录/采集/发布/改账。

- `2026-11-04` Meridian整改已独立复验，V1/V2离线问题关闭，详见 [整改记录](docs/MERIDIAN_CTP_V2_RECTIFICATION_ACCEPTANCE_20261104.md)。快照完成证明新增connection_generation，Relay适配时保留真实代次，不改OC交易wire或要求其扫描全目录。已在用户转交前同步 [OC确认文档](docs/OC_CTP_DRAFT2_ACCEPTANCE_REVIEW_20261104.md) 和固定提交包；旧51a4d6b包作历史。未切环境/运行采集/发布，SHFE及金融数据历史缺口保留。

- `2026-11-04` 已确认OC `1aed721/f15a0f7`选定约束/共享样例回签，六份JSON原字节不变；接受未证明写前顺序时判unknown的安全补充，Relay四包回归通过。补齐现有字段附录和Go runner的固定提交交付，详见 [回签确认](docs/OC_CTP_DRAFT2_ACCEPTANCE_REVIEW_20261104.md)。下一步核对OC已有金额说明/向量，继续完整schema与实际恢复；不重复开发OC已交付部分，未切环境/部署/放权，Meridian及历史绩效缺口保留。

- `2026-11-04` 初轮Meridian M1/M2复核发现5个类型反例及非末页请求隔离风险，历史证据见 [初轮记录](docs/MERIDIAN_CTP_V2_REVIEW_20261104.md)；同日整改已独立复验通过，以上不再是当前阻塞。完整SHFE源仍优先用Meridian既有采集器，OC单合约仅诊断，不拼造全目录；继续等待授权与独立行情验收。

- `2026-11-04` 按用户要求明确 Relay 为 OC/Meridian 唯一协调方；分别整理 [Meridian 待反馈项](docs/MERIDIAN_CTP_NEXT_ACTIONS_20261104.md) 和 [OC 问题答复及异常样例](docs/OC_CTP_NEXT_ACTIONS_20261104.md)。Relay 负责接收格式、原码映射、适配及联合验收，不要求 OC 实现 Meridian 导入格式。新增拒撤/未知结果/批量部分失败4例及持久恢复9个参考窗口，四包测试通过；仅离线样例，无真实崩溃注入。下一步取得两方回签，补金额serializer、完整schema及真实恢复证据；未切环境/部署/改账，SHFE仍未就绪，历史绩效缺口保留。

- `2026-11-04` 已复核 Meridian 合约目录回执：在线 `/v1/futures/instruments` 映射、同版本分页、变更页大小409和SHFE503均符合声明；现有观察expire_date不是法定到期日。新增 Relay `/v1/meridian/futures/instruments` 和 SDK `get_meridian_futures_instruments` 源码，保留上游标的/游标及错误详情，不升级外部数据框依赖。Go四包及SDK68项测试通过，当前未部署/发布wheel、未改环境/权限；SHFE需获授权的真实查询源/快照再交Meridian，详见 [回执复核](docs/MERIDIAN_CTP_CATALOG_RECEIPT_20261104.md)。历史绩效问题与F0其余恢复验收仍保留。

- `2026-11-03` 期货处于 F0：OC 报告7a4bb66/c50226d离线codec/凭据实现；Relay交付 [draft.2](docs/CTP_F0_DRAFT2_REVIEW_20261103.md)，修正fill_id、回执类型标记、批量继承及SDK约束，新增Meridian精确映射反例。标准代码必须来自Meridian security_id，原码按source_instrument_id/exchange_id保留，不自动拼接；在线契约仍仅列IF/IH/IC/IM，SHFE协作清单已整理。下一步OC复验draft.2、确认行情覆盖、补金额serializer与完整恢复fixture；运行时不接这些样例，未切环境/改账/开放权限，768历史资产问题保留。

- `2026-10-28` 768现金范围追查：新增唯一余额链审计，4060条非零交割全部使用、5日逐证券/金额/笔数完全匹配，证明现金记录无缺口或重复。资金报告其他资产仍缺业务定义，不能凭同额删除或加回；已向用户询问并整理 `docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md` 可转券商。完整证据见 `docs/FUYING13_BALANCE_CHAIN_REVIEW_20261028.md`，确认范围后再连续重算。

- `2026-10-28` 768跨日权益审计：新增离线增量流水工具，126项Python测试通过。10-15应收37,606.88到16日期末仍存续，前轮简化计算多扣一次；原510310现金差额交割日还混同经济来源日，资金表其他资产有4,671.92/12,688.20/7,423.35同额线索待核。新条件试算不发布，先核资产范围和来源日再连续重算；未改NAV或原账，详见 `docs/FUYING13_ETF_CASH_BRIDGE_REVIEW_20261028.md`。

- `2026-10-28` 768新文件复核及局部修复：36,860条流水与旧证据无金额/日期冲突，54个现金桥通过；9-09/10 close总资产修正为39,930,428.165330和39,861,602.151225。仅资产分项写入，柜台污染成本/盈亏和NAV均未重算；原始账本不变。8-26三只ETF漏计市值204,813.60，10-27新增45,222.24现金差额归属10-23；其余18个旧close及ETF连续权益仍待核。恢复证据、备份和下一步见 `docs/FUYING13_UPDATED_FUNDS_REVIEW_20261028.md`。

- `2026-10-28` 历史close现金时刻修复完成：重扫四户214日，3笔重复本金通过现有结算接口重建，资产分别修正为830/8-13的5456573.767352、388/10-21的49077771.348110、768/10-26的38900829.793475；原OC输入及1266个NAV历史版本完全不变，272条持仓全部财务字段不变。备份/回执/域名验收见 `docs/CLOSE_CASH_TIME_RECOVERY_20261028.md`。20个768早期漏列候选及其ETF权益另行核验；未改生产权限或进程。

- `2026-10-27` 已完成387/388实际清算与连续绩效恢复：159915的98个实物成分全部核对，另2行PCF明确零暴露；每户13.06过户费和652.34现金差额按源日应付、实际扣款日释放，T0净贡献分别-21386.47/-17430.61。备份后写入2条终值、198条清算现金、8个独立reconcile和10账户日成本/NAV，两户各61日、0 blocked，网页默认到10-27；仍为账户provisional，388部分repo费用提示保留。历史close未改、768未改，下步先重建两笔已确认重复本金的close，再核768权益。详细备份/验收及代码提交见 `docs/PERFORMANCE_ACTUAL_SETTLEMENT_RECOVERY_20261027.md`。

- `2026-10-27` 绩效修复代码完成：自动T0分组按原委托量/PCF整数倍排除底仓，387/388从10-21到27共10账户日内存连续预览通过，未落库。逆回购根因追到broker_close读取用整批captured_at覆盖15:01:01原始资金时刻，已改为读取raw源时间、缺时刻拒绝派生写入。新增 `performance-rebuild -preview-chain` 只读事务；测试及证据见 `docs/PERFORMANCE_REPAIR_PREVIEW_20261027.md`。历史close/NAV尚未修复，新券商清算仍须加入独立计划后备份发布。

- `2026-10-27` 新上传三户券商资金证据通过只读校验：资金表均到10-26，387/388各55日现金桥闭合；768新流水只有10-08起但可接旧冻结包，新增明确归属10-16的清算净入账786965.46元。已具备启动资金桥和尾段修复的主要证据，尚未改账；先修T0分组及逆回购重复计入风险，不把券商记账日盈亏直接替代源日收益。源指纹、剩余具体缺口见 `docs/BROKER_FUNDS_REPAIR_EVIDENCE_20261027.md`。用户上传的reference变更保留、不纳入文档提交。

- `2026-10-27` 四户只读绩效复核：830已发布至当日，387/388停在10-20，首因是10-21的159915百万份T0与两笔底仓被候选分组混用；后续为成本/NAV连续性阻断。另查明388的10-21、768的10-26收盘逆回购与绩效embedded口径不一致，有重复计入风险；768未在每日绩效cron名单，旧尾段仍待ETF清算权益复核。详见 `docs/PERFORMANCE_CLOSURE_REVIEW_20261027.md`。未改账、任务配置、环境或权限，先修Relay再区分外部证据缺口。

- `2026-10-23` 资金持仓收盘显示已修正：新增 `/asset?trade_date` 直接读历史close，保护逆回购和零证券市值资产；生产收盘收益无匹配NAV时显示待核算/待复核/待核对，不再用柜台污染成本拼账户盈利。四户资产及桌面/手机、历史CSV、repo-only浏览器fixture已核对；原金融事实未写入，生产权限和worker不变，见 `docs/ASSET_CLOSE_DISPLAY_20261023.md`。三户历史绩效/ETF清算仍未完成，不能将本次页面修正视为NAV已恢复。

- `2026-10-21` 新增只读 `performance-etf-transfer-review`，无需Meridian/OC即可全量分页定位未关联成分及原始fill身份。768的15/16日发现5条159900现金替代分类问题，15日另有600026/601872真实隔夜及myasset卖出未归篮；两次只读结果一致，未写NAV或改原始数据。可转OC文档为 `docs/OC_ETF_CASH_SUBSTITUTION_CLASSIFICATION_20261021.md`，完整审计见 `docs/FUYING13_ETF_TRANSFER_AUDIT_20261021.md`；数量匹配不代表经济资产/清算已核验。

- `2026-10-20` 富盈13号10月16日待结权益优先复核：页面-81万元不能视为可信实际亏损；159869两条159900/数量1/价格116964及233928的记录与现金替代款对应，但OC的cash_substitution仍为false。已暂停旧恢复CLI写入并补回归，原NAV/原始账表未改，不按人工约亏4万元凑平。事实、条件试算及可转OC的分类要求见 `docs/FUYING13_ETF_TAIL_REVIEW_20261020.md`；此前“375点、0 blocked”只描述发布状态，不代表尾段经济权益完整。

- `2026-10-20` 按用户要求增加可选“出金前资本参考（非正式）”曲线，默认未勾选。用户确认参考分母正常计入入金、加回原计权出金资本，避免历史大额入金日的日初小分母失真；逐日连乘及参考回撤仅作分析，区间基期1、缺证据后停止。正式NAV/基准统计不改，切换不查询、不丢缩放，API/CSV同步；详见绩效设计“可选出金前资本参考曲线”。

- `2026-10-20` 经用户认可新增双口径资金影响：正式净值/回撤不改，资金悬浮及“资金影响”明细并列日初资本参考收益、正式分母和固定股票/ETF市值前后仓位，CSV同步；日期/证据缺失不补造实时仓位。富盈13号7月2日正式-4.52%、参考-1.16%，市值固定试算14.50%→56.57%；公式见 `docs/PERFORMANCE_ANALYSIS_DESIGN.md`“资本参考与资金影响”。

- `2026-10-20` 绩效净值图已增加资金流入/流出金额标记，悬浮显示东八区实际时刻、当日盈亏及净流量；策略资本独立，日期级旧记录不补造时刻。只读投影既有NAV证据，不重算净值、不改权限；浏览器验证富盈13号375点及同日双向资金样例，设计见 `docs/PERFORMANCE_ANALYSIS_DESIGN.md`“资金变动标记”。

### 已验证运行态

- `2026-10-04 21:10 Asia/Shanghai` 按用户明确指令从 TEST 切回生产：`.runtime/active-config.yaml -> config/relay.prod.yaml`，目标库 migration 成功，独立 API/worker、Redis、PostgreSQL、Meridian 和事件流均正常。6 个配置账户保持 4 个启用查询、0 个开放交易，`501000114077` 与 `307000051389` 继续停用且历史账本保留；当天为非交易日，4 个启用账户均正确显示 `off_hours`，16 条生产 Stream 总 lag 为 0、pending DLQ 为 0。
- `2026-09-30 20:34 Asia/Shanghai` 按用户明确指令从生产切换到 TEST：`.runtime/active-config.yaml -> config/relay.local.yaml`，目标库 migration 成功，API 内嵌 worker、Redis、PostgreSQL、Meridian 和事件流均正常。测试账户 `00030484` 为 `trading_enabled=true`，但 OC 最后 heartbeat 停在 `2026-09-19 08:21:40 Asia/Shanghai`，当前 `HEARTBEAT_STALE / order_entry_ready=false`，Relay 正确失败关闭；reply/event/hb/dlq 四条 Stream 总 lag 为 0、pending DLQ 为 0。生产配置和生产账本未修改，切回生产必须取得用户新的明确指令。
- `2026-09-30 Asia/Shanghai` 接入 Meridian CTP 股指期货正式契约：Relay 新增 `/v1/meridian/futures/{status,snapshots,bars}` 与两条期货 SSE 薄代理，Python SDK `0.1.41` 同步查询和快照订阅。交易终端以 Meridian 活跃快照生成 `IF/IH/IC/IM.CFFEX` 自动补全，按资产类型选择期货实时快照/分钟线并持续刷新；由于 OC 当前不支持期货下单，CFFEX 在风险提示和提交函数均失败关闭。未改生产环境、账户配置或下单权限。
- `2026-09-24 09:23 Asia/Shanghai` 定位并修复交易终端盘前总资产短暂等于可用资金。OC 当前资金页只含现金，09:01 的当前持仓也不带行情市值；盘前初始化虽已正确落库上一交易日收盘估值的 `open` 快照，但交易终端的全量持仓实时汇总会在 Meridian 首条 Level1 到达前把市值按 0 覆盖。当前实时持仓估值按 Meridian 标准字段逐级使用 `last -> pre_close -> 上一交易日 adjustment=none 日线 close`，仅补缺失证券；浏览器发现任一正持仓仍无有效估值时回退资产接口，不生成现金-only 总资产。新增两组 API 回归覆盖“有昨收、无最新价”和“Level1 整体尚未出现”，全量 Go 测试通过；生产下单继续关闭。
- `2026-09-23 10:45 Asia/Shanghai` 发布 OC 部分成交状态兼容：仅当 `gateway_status=partially_filled` 且数量满足有效非终态部分成交时，标准账本映射为 `status=partially_filled + gateway_status=working`；实时事件和订单查询页共用规则，raw 永久保留原值，标准账本记录审计字段，同步报告记录规范化计数。生产三户当日 `481/299/195` 条原始事件已全部重放为同量订单事件，0 解析/数据库错误；两笔仍在部分成交的订单已更新为 `12,500/175,000` 和 `23,000/291,200`，与 2/4 条成交合计一致，当日孤立成交为 0。旧 checkpoint 错误已转存审计，API、16 条 Stream 和全部核心依赖恢复 `ok`，总 lag=0、pending DLQ=0；OC 后续可随正常版本修正，无需紧急升级。详见 [兼容与验收记录](/home/ti-relay-trader/docs/OC_PARTIAL_FILL_GATEWAY_STATUS_20260923.md:1)。
- `2026-09-23 10:00 Asia/Shanghai` 发布绩效默认权威区间：新增只读 `performance/default-range`，仅接受成功 `performance_canonical` 对应的 Meridian 权威日线 NAV、人工 finalized NAV 或 `broker_statement_nav.v1`；默认结束日为账户最近权威绩效日，起始日向前一个自然月并夹紧月末。当天尚无权威结果时不调用当天 economic NAV preview；完全无权威历史的账户显示正常空态。生产三户可用账户均解析至 `2026-08-22..2026-09-22`，涌盈波动率 Playwright 实测曲线 22 条、焦点日已发布、无控制台/HTTP 错误；富盈13号因尚无可信起算锚点显示“暂无权威绩效数据”。交易权限保持 0。
- `2026-09-22 20:50 Asia/Shanghai` 完成涌盈波动率 `307000051388` 的资产快照与资金桥恢复。最新券商资金文件 42/42 行满足 `收盘资产=前收+入金-出金+当日盈亏`，9 月 10 日 `33,000,000 CNY` 和 9 月 11 日 `170,000 CNY` 转出均以 0.00 元残差闭合；成交资金明细与 OC open 将两笔流量分别夹定在 09:01 后至首笔成交前、09:01 前。9 月 9 日至 21 日 9 条独立 `reconcile`、2 条 confirmed external flow 和金标已落库，顺序重建至 9 月 22 日后 11 个交易日均为 provisional、0 blocked。券商权威资产已闭合但证券归因未闭合的日期保留账户 NAV，并明确标记 `strategy_attribution_pending`，不再丢弃整日净值；OC open/close、订单和成交均未覆盖。写前备份 SHA-256 为 `6fb2097d22e3f0e0283909dcae57eb0a70a66aace288a7a9886a132d678a855f`，详见 [恢复记录](/home/ti-relay-trader/docs/BROKER_FUNDS_BRIDGE_RECOVERY_307000051388_20260922.md:1)。
- `2026-09-22 20:05 Asia/Shanghai` 追清当日 `performance_canonical` 失败：Meridian 三类未复权日线水位实际于 16:33:14 全部到达 `20260922`，原失败由 cron 显式名单仍包含已停用 `307000051389` 引起；补跑后又准确识别 `307000051388` 的既有 `previous_economic_nav_gap` 是账户资金连续性问题，不是行情未就绪。当前显式任务名单也会强制过滤 `enabled=false`，盘后绩效只处理本轮 broker close 成功账户；canonical 价格完成但单账户账务 blocked 时，任务成功并保留账户级告警。三个纳入绩效的启用账户 canonical 比较均为 0 差异，任务终态 `succeeded`。跟随 Meridian 16:40/16:50 校准与 17:05 状态机截止，Relay 当前改为 17:10 首查、每 10 分钟重试至 19:10。
- `2026-09-22 19:40 Asia/Shanghai` 完成债享5号 `314000046830` 开户以来券商账表重建：`2025-12-29` 为零资产锚点，178 个资金日资产恒等式、51,948 条唯一资金流水现金桥、51,604 笔成交与交割单、51,667 个持仓数量事件均 0 差异；178 条 current gold 和 `2025-12-30..2026-09-21` 共 177 条 `broker_statement_nav.v1 finalized` 已落库，9 月 22 日 v2.7 顺序重建为 version 3。`委托.csv` 与 `成交.csv` 完全相同，因此未成交/撤单历史仍不能据此证明；账户净值、成交、实际费用和持仓成本不受影响。一次性导入不进入 cron、不覆盖 OC 原始账表，详见 [完整对账记录](/home/ti-relay-trader/docs/DEBT5_FULL_HISTORY_RECONCILIATION_20260922.md:1)。写前备份 SHA-256 为 `0241a98cbcd70b385333b90a4e124a8494c33e3c6d754e3abc10dbcdb3131b53`。
- `2026-09-22 18:55 Asia/Shanghai` 使用四户最新券商资金文件完成 9 月 21 日盘前资金事故的一次性审计。四户相邻交易日资产恒等式均以 `0.00 CNY` 闭合；三户缺盘前资金账户只写独立 `reconcile` 证据，不补造 open、不覆盖 OC 快照。同步修复经济净值缺 open 时把前收总资产误作现金并再次叠加持仓的错误，并增加上一交易日 NAV 连续性门禁，禁止累计曲线静默跨过阻断日。智算汇利混合 9 月 21 日 open `49,678,696.28`、close `49,766,465.34`、PnL `87,769.06`、残差 0，已顺序重建至 9 月 22 日；涌盈波动率保留 `2,703.08 CNY` 未分类资金事项阻断，9 月 22 日旧 current NAV 已追加连续性审计并标记 blocked；富盈13号继续等待绩效起算锚点；债享5号保持真实盘前证据优先。写前备份 SHA-256 为 `e750cbe3c39a0a25d38f6ef843f3f4921f98caad477e752fbee29358db0e2765`，详见 [恢复记录](/home/ti-relay-trader/docs/BROKER_PREOPEN_RECOVERY_20260921.md:1)。运维监控窗口已收紧为 `08:55 <= t < 15:30`，OC 计划关停后统一显示 `off_hours`，不再标红。
- `2026-09-21 14:43 Asia/Shanghai` 查询中断自动恢复聚合已部署并在线验收：仅当同一 `origin_message_id` 的查询先收到 `QUERY_INTERRUPTED`、随后收到 action/result type 匹配的 `completed/is_last=true` 末页时，命令状态才返回 `completed/success/recovered`；对应未审核 DLQ 显示“已自动恢复”且不计入当前 pending。债享5号现存 5 条 OC 重启竞态记录全部准确识别，生产 `/v1/status` 从 `degraded` 恢复为 `ok`，pending DLQ=0、Stream lag=0；其他错误码、非法末页及反向顺序仍保持矛盾告警，原始 reply/DLQ 永久保留，生产下单保持关闭。
- `2026-09-21 13:32 Asia/Shanghai` 补救当日盘前初始化失败：四户 09:01 持仓查询均为唯一成功终态，证券集合和数量与 9 月 18 日收盘逐项一致，仅可卖数量按 T+1 正常恢复；从 raw archive 恢复 270 条 `position_snapshots(open)`。债享5号另有三次一致的 08:55 资金回报，完整恢复日初现金 `3,278,865.20`、市值 `2,032,161.00`、总资产 `5,311,026.20`；其余三户没有可信盘前资金，未用盘中资金倒填，继续由绩效层以前收盘经济资产回退并保留质量阻断。原失败任务状态和错误证据保留，报告新增 `partial_recovered` 审计，`/jobs` 复核页可区分完整恢复与仅持仓恢复。生产库写入前备份为 `outputs/backups/relay_trader_20260921T132658+0800.dump`。任务侧对 `BROKER_NOT_READY` 改为在 180 秒窗口内每 5 秒仅重试失败查询，其他错误仍立即阻断；cron 已更新，生产下单仍关闭。
- `2026-09-21 11:06 Asia/Shanghai` 完成生产重复 OC 隔离与复验：安全组已阻断旧 OC 出口 `117.186.20.114`，经用户授权按 client ID 清除 6 条剩余 Redis 连接，立即及后续复查均无重连。四个账户唯一健康会话、托管凭据和柜台快照正常；资金/持仓/订单/成交 16/16 查询取得唯一成功终态，16 条 Stream 健康且 `lag=0`，当日订单累计成交量与成交明细逐户闭合、活动订单为 0。08:55 OC 重启恢复产生的 5 条 `QUERY_INTERRUPTED` 后续均取得同原命令成功终态，并已在 14:43 自动识别为 recovered。生产交易继续关闭，后续轮换 Redis 凭据，完整证据见 [OC 生产重复会话事故](/home/ti-relay-trader/docs/OC_PROD_DUPLICATE_SESSION_INCIDENT_20260921.md:1)。
- `2026-09-19 10:22 Asia/Shanghai` 按用户要求停用生产账户 `307000051389`：配置改为 `enabled=false/trading_enabled=false`，OC 端可保持关闭；账户路由、数据库别名及历史订单/成交/持仓/快照全部保留并可读。生产 API/worker 重启健康，6 个配置账户中现有 4 个启用查询、0 个开放交易，运行监控由 20 条 Stream 收敛为四户 16 条且总 `lag=0`、pending DLQ=0。今天是非交易日，系统正确标记 `non_trading_day`；盘前、盘后、绩效及运行监控默认账户集合均按 `enabled=true` 选择，不会因两个停用账户无 OC 响应而整体失败。
- `2026-09-18 16:25 Asia/Shanghai` 切换前再次确认 TEST 新版 OC、凭据 `v1`、柜台、订单快照、Stream 和日志全部正常；随后按用户授权切回生产。生产目标库 migration 成功，独立 API/worker 和全部依赖为 `ok`，6 个账户保留、5 个启用查询、0 个开放交易；当前已过生产监控窗口，五户按预期显示 `off_hours`，最后一轮旧 OC 状态的柜台和订单快照均 ready，报单继续由 `RELAY_TRADING_DISABLED` 阻断。20 条受监控 Stream 健康、总 `lag=0`、pending DLQ=0；四户预置生产凭据 `v2` 未修改。
- `2026-09-18 16:23 Asia/Shanghai` 按用户明确指令从生产切到 TEST：目标库 migration 成功，API 内嵌 worker、Redis、PostgreSQL、行情和事件桥均为 `ok`。OC `counter_session_id` 已从首次验收会话变更为新进程会话，但仍成功加载账户 `00030484` 的凭据 `v1`，托管账户、Key ID、柜台登录、订单快照及报单/撤单准入全部匹配。资金、持仓、订单、成交四类查询在新会话下均取得唯一成功终态并落账，consumer group `pending=0,lag=0`、无 DLQ，完成 TEST 跨进程凭据恢复验收。生产配置、旧版 OC 和预置的四户凭据 `v2` 均未修改。
- `2026-09-18 14:07 Asia/Shanghai` 已为计划升级新版 OC 的四个生产账户预置 `oc.secret.v1` 凭据 `v2`：富盈13号、智算汇利混合、涌盈波动率和债享5号。首次 `v1` 的动态密码为空，用户指出后立即以与 TEST 相同的动态密码完整轮换为 `v2`；`current` 均已指向 `v2`，独立认证解密确认登录账户匹配、交易密码非空、动态密码非空且符合预期，四户 PostgreSQL `started/succeeded` 审计完整。旧 `v1` 密文只保留审计且不再生效；`307000051389` 与停用账户 `501000114077` 未写凭据。当前旧版 OC 未重启，生产仍为 5 个网关在线、0 个开放交易、总 `lag=0`。
- `2026-09-18 14:02 Asia/Shanghai` 按用户明确指令从 TEST 切回生产：目标库 migration 成功，独立 API/worker 均健康；6 个账户保留、5 个启用查询、0 个开放交易，`501000114077` 继续停用且历史账本不删除。当前生产仍为旧版 OC，五个启用账户 heartbeat 新鲜，Redis、柜台登录和订单快照全部 ready；心跳不含可选的托管凭据字段时 Relay 保持向后兼容，报单统一由 `RELAY_TRADING_DISABLED` 阻断。20 条受监控 Stream 健康、总 `lag=0`、pending DLQ=0，未向生产发布查询或交易命令；后续等待用户协调维护窗口替换新版 OC。
- `2026-09-18 13:57 Asia/Shanghai` 完成新版 TEST OC 加密凭据下的最小写链路验收。以 Meridian `600000.SH` 实时盘口为参考提交 100 股买单，Relay 返回 HTTP 202 和唯一 accepted 回执，订单最终 `filled 100/100, leaves=0`，唯一成交价格 `9.08`、`matched_at=13:56:05+08:00`，三类订单 ID、原命令关联和 OC 会话 ID 全程稳定；相同幂等键和请求重放返回 HTTP 200、`replayed=true`，`cmd.trade` 未新增消息。第二笔 100 股低价买单进入 working 后撤单，撤单回执 accepted，最终 `cancelled, filled=0, leaves=0`。随后资金、持仓、订单、成交四类 OC 查询全部唯一成功终态并与实时账本一致：2 笔订单分别为 filled/cancelled、1 笔唯一成交、0 活动订单；服务保持 TEST `ok`，consumer group `pending=0,lag=0`，无 DLQ 或错误日志。测试柜台订单状态时间仍使用模拟时钟，成交权威时间继续读取 `Fill.matched_at`。
- `2026-09-18 13:43 Asia/Shanghai` 新版 TEST OC 已加载账户 `00030484` 的加密凭据 `v1`：heartbeat 的 `credential_status=loaded`、版本、Key ID、来源及托管账户均与 Relay 一致，Redis、柜台登录、订单快照、报单和撤单准入全部 ready。Relay 已按预定维护步骤从兼容旧 OC 的 `relay:prod:*` 迁移到标准 `relay:test:*`，重启后整体状态为 `ok`。资金、持仓、订单、成交四类只读查询均取得唯一成功终态并落账，持仓 38 条、当日订单和成交为空，Stream `pending=0,lag=0`、无 DLQ。`user_product_info` 由 OC 内部填写，不属于 `oc.secret.v1`，Relay 无需传递或扩展协议；生产运行态未切换。
- `2026-09-18 13:29 Asia/Shanghai` TEST 账户 `00030484` 已按 `oc.secret.v1` 写入首个凭据版本：Redis `current=v1`、AES-GCM 认证解密、信封身份及 PostgreSQL `started/succeeded` 审计全部通过；临时明文文件已删除，仓库与 API 日志泄漏扫描为 0。用户随后确认 `user_product_info` 由 OC 内部填写，不属于 Relay 托管凭据。
- `2026-09-18 13:27 Asia/Shanghai` 已把用户提供的 TEST/PROD `Key ID + AES-256` 主密钥部署到 Git 忽略且权限为 `0600` 的 `config/relay.credentials.env`，并生成独立 Web 管理令牌；文件不重复保存 Redis 密码。当前 TEST 实例重启后，凭据管理接口无令牌返回 `403`、授权查询返回 `200`，账户 `00030484` 明确为 `configured=false`，因此尚无版本信封或 `current` 指针。生产运行态未切换，下一步需取得该账户券商登录用户/密码后再通过 `/operations` 首次轮换，并与 OC 联合验收。
- `2026-09-18 13:12 Asia/Shanghai` 已完成 OC `oc.secret.v1` 的 Relay 侧实现：每账户 AES-256-GCM 版本信封、先写版本再切换 current、PostgreSQL 轮换/停用审计、`relayctl credentials`、`/operations` 内网管理模块和托管心跳失败关闭；TEST/生产数据库均升级到 schema 30，SDK 发布为 `0.1.40`。真实凭据尚未写入；等待与 OC 通过安全渠道对齐两套 Key ID/Key 后先做 TEST 单账户验收。当前 TEST 业务 Stream 继续使用旧 `relay:prod:*` 命名，未做单边切换。
- `2026-09-18 12:40 Asia/Shanghai` 按用户明确指令从生产切到测试环境：`.runtime/active-config.yaml -> config/relay.local.yaml`，TEST migration 完成，API 内嵌账本同步 worker 及 Redis、PostgreSQL、行情和事件桥均为 `ok`；账户 `00030484` 启用查询和交易配置。OC 最新 heartbeat 为 `2026-09-17 23:04:06 Asia/Shanghai`，当前 `HEARTBEAT_STALE`、`order_entry_ready=false`，因此 Relay 正确失败关闭；`cmd.trade/cmd.query pending=0,lag=0`，无 DLQ。生产配置和数据未修改，切回生产仍需用户新的明确指令。
- `2026-09-17 19:38 Asia/Shanghai` 按用户明确指令从测试切回生产：`.runtime/active-config.yaml -> config/relay.prod.yaml`，数据库 migration 完成，独立 API/worker 健康；6 个账户保留、5 个启用查询、0 个开放交易，`501000114077` 继续停用且历史账本不删除。生产 API、PostgreSQL、Redis、行情、事件桥和订单服务均为 `ok`；20 条受监控 Stream 健康、总 `lag=0`、待处理 DLQ 为 0，盘后 5 个启用账户显示 `off_hours` 属正常状态。
- `2026-09-17 19:19 Asia/Shanghai` 接受 OC `42150cb` 对华鑫 TEST 柜台的最小边界：精确全局状态拒绝在当前 OC 进程内锁存，先发布 `DEGRADED / counter_order_entry_not_ready / accepting_trade_commands=false` 心跳，再发布拒单事件；查询继续，满足快照条件时撤单继续，恢复只通过人工确认后的 OC 重启和新 `counter_session_id`。Relay 已移除订单账本反推、五分钟冷却及专属 API 字段，只按新鲜 OC heartbeat 返回通用 `OC_TRADE_COMMANDS_PAUSED`；SDK `0.1.39` 同步该语义，生产行为不变。当前 OC 最后 heartbeat 为 `17:54:17 Asia/Shanghai`，19:19 已过期，Relay 正确以 `HEARTBEAT_STALE` 失败关闭，在线锁存验收等待 OC 新版本进程启动。
- `2026-09-17 15:20 Asia/Shanghai` 按用户明确指令从生产切到测试环境：TEST schema 29、API 内嵌 worker 及全部依赖为 `ok`；账户 `00030484` 的 Redis、券商登录、订单快照和报单准入均 ready，当前 `counter_session_id=hxproc-0eb87c8f23897e76`，下一次柜台时段切换为 `15:30`。Stream `pending=0,lag=0`，空 DLQ Stream 未创建属于正常状态；切换时旧行情连接产生的一条 `context canceled` 不属于当前运行故障。
- `2026-09-17 14:42 Asia/Shanghai` 按用户明确指令从测试切回生产：独立 API/worker、数据库、Redis、行情和事件桥正常，6 个账户保留、5 个启用查询、0 个开放交易。五个启用账户的 OC heartbeat、券商登录和订单快照均 ready，并已提供不同的 `hxproc-*` 进程会话 ID；Stream `pending=0,lag=0`、无待处理 DLQ。总体暂显示 `degraded` 仅因为无事件账户 `307000051389` 尚未创建 `event` Stream，其他 Stream 与 heartbeat 正常，未作为协议或数据故障处理。
- `2026-09-17 10:00 Asia/Shanghai` 按用户明确指令从生产切到测试环境：TEST schema 29 和 API 内嵌 worker 正常，数据库、Redis、行情、事件桥和订单服务均为 `ok`，Stream `pending=0,lag=0`。测试 OC 最后 heartbeat 为 `2026-09-16 22:22:05 Asia/Shanghai`，当前 `HEARTBEAT_STALE`、`order_entry_ready=false`，因此总体显示 `degraded`；这是 OC 尚未启动造成的失败关闭，不是 Relay 切换或协议错误。
- `2026-09-16 22:22 Asia/Shanghai` 按用户明确指令从测试切回生产：独立 API/worker 和全部依赖健康，6 个账户保留、5 个启用查询、0 个开放交易；业务 Stream consumer group `pending=0,lag=0`，无新 WARN/ERROR。`307000051389:event` 仍未创建但其他 Stream 存在，沿用“尚未产生事件”的监控结论。
- `2026-09-16 15:18 Asia/Shanghai` 按用户明确指令从生产重启到测试环境：TEST schema 29 已就绪，API 内嵌账本同步 worker 健康；账户 `00030484` 的 Redis、券商登录、订单快照和报单准入均 ready，当前 `counter_session_id=hxproc-8c52fcb4c88e9250`。六条 Stream 中仅不存在正常的空 DLQ Stream，consumer group `pending=0,lag=0`；生产配置和数据未修改。
- `2026-09-16 14:40 Asia/Shanghai` 按用户明确指令从测试切回生产：生产库已升级到 schema 29，独立 API/worker 健康，6 个账户保留、5 个启用查询、0 个开放交易。五个启用账户的旧版 OC 均为 `UP`，Redis、券商登录和订单快照 ready；资产、当日订单、当日成交只读接口全部通过。旧版 heartbeat 暂无可选 `counter_session_id`，Relay 兼容为空且不影响查询和落账；业务 Stream consumer group `pending=0,lag=0`、无 DLQ 或解析错误，因此未增加吞错规则。`307000051389:event` 尚未创建但 heartbeat 正常，当前按“未产生事件”监控，不标记协议故障。
- `2026-09-16 13:58 Asia/Shanghai` Meridian 实时 Level1 与测试柜台撮合盘口并非完全同步，仅作为报价参考；7 笔 TEST 主动成交矩阵最终为 3 笔成交、4 笔撤单、0 笔活动残留。3 笔成交均满足 `cum_filled_qty=order_qty=100`、`leaves_qty=0`、`is_terminal=true`，订单与普通成交数量逐笔闭合。schema 29 已在 TEST 应用并把 3 笔既有成交全部回填为 `strategy_id=active-fill-matrix`；后续写入也按同账户、同交易日、同 `gateway_order_id` 继承订单策略归属。四条业务 Stream `lag=0`，DLQ 不存在。
- `2026-09-16 13:44 Asia/Shanghai` TEST 连续人工重启的会话 ID 从 `hxproc-a5463f8735b83449` 换为 `hxproc-8c52fcb4c88e9250`；第二个会话的最小测试单多次完成 `created -> working -> cancelled`，订单事件、撤单尝试和 heartbeat 会话 ID 一致。OC 新增的 `order.cancel.accepted` 已按同一 `origin_message_id` 幂等合并，柜台 event 正确把 reply 的暂态 `reconciliation_required=true` 更新为 `false`，不再新增 unsupported 错误；四条 Stream `lag=0`、无 DLQ。
- `2026-09-16 13:18 Asia/Shanghai` OC 新版在线验收：`counter_session_id=hxcs-0da546ea12434a71` 在 `disconnected -> ready` 期间保持稳定；对上一会话遗留订单撤单返回 `ORDER_NOT_FOUND`，完整携带 `retry_safe=false`、`order_state_changed=false`、`reconciliation_required=true`、东八区 `occurred_at` 和当前会话 ID，原订单仍为 working。TEST 会话现收敛为“一次 OC 进程生命周期一个 ID”，进程内短线重连不变、人工重启换新，OC 不实现四时段判断。13:18 的 100 股最小测试单到达柜台后被以“当前状态禁止此项操作”终态拒绝，订单身份和会话字段正确，但实际下单/撤单仍需目标交易区间人工重启 OC 后复验。
- `2026-09-16` 已把 OC 后续任务合并到 [测试柜台进程会话与订单可靠性契约](/home/ti-relay-trader/docs/OC_COUNTER_SESSION_ID_REQUIREMENT_20260916.md:1)：`counter_session_id` 采用一次 OC 进程生命周期一个 ID 的最小实现，并约束撤单结果六个结构化语义字段及 `filled` 数量/残量/终态原子一致；历史字段允许留空，新版本订单必须满足。
- `2026-09-16 10:52 Asia/Shanghai` 已发布撤单尝试分页账本和 `relay-sdk 0.1.37`：`iter_cancel_attempts(page_size=7)` 在线完整读取 20 条唯一 attempt/Gateway ID，均为 `ORDER_NOT_FOUND` 且 `reconciliation_required=true`；对应前一日 20 笔订单仍为 `working/is_terminal=false`。Relay 已兼容 readiness/订单/撤单尝试的可选 `counter_session_id`，当前 OC heartbeat 未提供该字段，因此 TEST 遗留订单人工 resolution 保持失败关闭，生产更不会据此自动终态化。
- `2026-09-16 10:26 Asia/Shanghai` 测试 OC 恢复并完成无交易写入的主动验收：账户 `00030484` 的资金、持仓、订单、成交、费用查询均取得唯一 completed 终态；资金更新时间为 `10:26:21`，持仓返回 15 条，今日订单/成交/费用均为空结果；4 条 Stream 全部健康、总 lag 为 0、pending DLQ 为 0，`order_entry_ready=true`。测试配置沿用 OC 约定的 `relay:prod:v1:huaxin:00030484` 键名前缀，但 Redis 与 PostgreSQL 均为测试环境独立实例。
- `2026-09-16 09:58 Asia/Shanghai` 按用户明确指令从生产切到测试环境：测试库 migration 成功，API 内嵌账本同步 worker 启动；Redis、数据库、行情和事件流均正常，但测试 OC 柜台会话为 `disconnected`、订单快照未 ready，账户 `00030484` 当前 `order_entry_ready=false`，Relay 以 `BROKER_DISCONNECTED` 失败关闭并拒绝交易命令。生产配置未修改。
- `2026-09-15 22:39 Asia/Shanghai` 按用户明确指令从测试切回生产环境：生产库 migration 成功，独立 API/worker 启动健康；6 个账户保留、5 个启用查询、0 个开放交易，`501000114077` 继续停用且历史账本不删除。
- `2026-09-15 15:18 Asia/Shanghai` 按用户明确指令从生产切到测试环境：测试库 migration 成功，API 内嵌账本同步 worker 启动健康；账户 `00030484` 的 Redis、柜台会话和订单快照均 ready，`order_entry_ready=true`、`trading_enabled=1`。生产配置未修改，后续切回生产必须等待用户新的明确指令。
- `2026-09-15 15:16 Asia/Shanghai` 修复 `/jobs` 账户复核的日初资产口径：旧页面误用盘前 OC 原始资金摘要，现始终回读已落库的 `open` 资产快照；该快照按上一交易日 Meridian 未复权收盘价汇总持仓市值。在线确认富盈13号日初资金 `34,644,380.14`、市值 `5,008,603.10`、总资产 `39,652,983.24`，智算汇利混合日初资金 `46,326,170.32`、市值 `3,216,148.50`、总资产 `49,542,318.82`。后续盘前/盘后任务报告也直接携带最终估值资产，不再混用 OC 原始摘要。
- `2026-09-15 14:31 Asia/Shanghai` 按用户明确指令从测试环境切回生产：目标库 migration 成功，独立 API/worker 启动健康；6 个账户保留、5 个启用查询、0 个开放交易，`501000114077` 继续停用且历史账本不删除。
- `2026-09-15 13:41 Asia/Shanghai` 已按华鑫 7x24 测试柜台的集合竞价、休市和交易分段增加账户级 `order_entry_ready`；只在测试配置生效，生产仍使用正常 A 股时段。`GET /v1/accounts/00030484/readiness?force=true` 实测为 ready、下一次切换 `15:30`；Python SDK `0.1.36` 增加类型化读取和失败关闭 `verify_ready()`，详见 [Chronos 测试柜台准入说明](/home/ti-relay-trader/docs/CHRONOS_TEST_ORDER_ENTRY_READINESS_20260915.md:1)。
- `2026-09-15 09:37 Asia/Shanghai` 按用户明确指令切到测试环境：`.runtime/active-config.yaml -> config/relay.local.yaml`，API 内嵌账本同步 worker；测试 OC 的 Redis、柜台和订单快照均 ready，账户 `00030484` 可接收交易及撤单命令，4 条 Stream `lag=0`、pending DLQ=0。生产配置未修改，切回生产仍需用户明确指令。
- `2026-09-14 21:01 Asia/Shanghai` 按用户明确指令切回生产环境：`.runtime/active-config.yaml -> config/relay.prod.yaml`，独立 API/worker 均健康；6 个账户配置中 5 个启用查询、0 个开放交易，20 条生产 Stream `lag=0`、pending DLQ=0。盘后 `off_hours` 为正常监控状态。
- `2026-09-14 19:18 Asia/Shanghai` 测试链路完成只读复测：账户 `00030484` 的 OC `redis_ready/broker_ready/order_snapshot_ready` 均为 true，资金及 10 条持仓查询取得 completed 终态并成功落库，四条 Stream `lag=0` 且无 DLQ。`off_hours` 仅表示盘后监控阶段；测试库已补齐 migration 25-28，环境切换脚本现在会先迁移目标数据库，成功后才停止旧服务。生产配置未修改，后续切回生产仍必须等待用户明确指令。
- `2026-09-14` Chronos R2c 测试环境联调已完成。13:23 曾在未取得本轮明确授权时恢复生产，用户于 13:28 要求切回测试；14:29 收到用户明确指令后才切回生产只读。Relay 必须等待新的明确指令，不得把历史讨论、README 提醒或计划任务时间视为切换授权。
- `2026-09-08` 生产部署基线为 `.runtime/active-config.yaml -> config/relay.prod.yaml`，独立 API/worker；六个生产账户及数据库别名保留，其中 `501000114077` 为 `enabled=false/trading_enabled=false`，其余五户启用查询但均关闭交易。每日任务默认账户集合已验证不包含停用户，历史账本读取不受影响。
- `2026-09-09` 09:01 盘前初始化在 OC 登录前发布查询，五个启用账户均因 180 秒无 reply 而阻断，未写 open 快照；OC 就绪后 09:07 手工重跑仅耗时 7.2 秒，20 类账户查询均取得唯一 completed 终态，写入 5 个日初资产快照和 227 条日初持仓，0 账户错误。原失败任务保留为历史记录，最新任务状态为 succeeded。
- `2026-09-06` 测试批量单已验证 `HTTP 202 -> accepted reply -> order.event -> PostgreSQL -> Web`。OC 后续状态事件曾把同一订单交易日从 `20260906` 改为 `20450624` 并丢失命令关联，Relay 已按订单时间防止异常日期拆单、保留原值审计，并让批量页按 `message_id` 自动刷新回报；OC 侧反馈见 [测试批量订单回报反馈](/home/ti-relay-trader/docs/OC_TEST_BATCH_ORDER_FEEDBACK_20260906.md:1)。
- `2026-09-06` OC `f4a683a/5eaea72` 已完成在线复测：两个批量子单均收到 accepted/working/cancelled 多阶段事件，所有事件保持 `trade_date=20260906`、原批量 `origin_message_id` 及稳定的订单 ID；`20450624` 只出现在 adapter 审计字段，主动订单查询后身份不变，command groups 为 `pending=0,lag=0`。详见 [OC 测试订单回报验收](/home/ti-relay-trader/docs/OC_TEST_ORDER_REPORT_VALIDATION_20260906.md:1)。
- 交易终端日期按环境分流：测试环境订单/成交监控默认东八区自然日，允许周末和节假日查看测试柜台订单；生产环境仍默认 Meridian 最近交易日。K 线、绩效和资金持仓在两个环境中均继续使用最近交易日口径。
- 生产 API、worker、PostgreSQL、Redis、事件桥、Meridian 行情代理和订单服务均已接通；API 监听 `0.0.0.0:9092`，worker 健康端口只监听 `127.0.0.1:19092`。
- 生产配置保留账户 `501000114077`、`314000046830`、`314000045768`、`307000051388`、`307000051389`、`307000051387`；其中 `501000114077` 自 `2026-09-08`、`307000051389` 自 `2026-09-19` 起停用，`307000051387`、`307000051388` 自 `2026-11-18` 起暂时停用。仅债享5号、富盈13号启用查询，全部生产交易关闭；停用账户历史信息均不删除。别名由 PostgreSQL 管理，账户 ID 始终作为路由和账本主键。
- 每个资金账户都带必填 `broker_id` 所属券商标签；当前六户均为 `huaxin`。该标签与账户别名、Gateway 和环境分离，后续新增券商沿用同一账户路由模型。
- `2026-08-26` 已验证 `archive_incomplete -> Level1 provisional -> canonical daily` 全链路：3 个活跃账户 ready，1 个空账户 not_applicable，0 blocked；权威日线复算与 provisional NAV 差异为 0。
- Meridian 权威日线父任务 16:30 启动，随后执行 16:40/16:50 本地校准和质量复验，状态机 17:05 截止；Relay 17:10 首查并每 10 分钟重试至 19:10。窗口内显示等待，19:10 仍未就绪才标记 Meridian 上游阻塞；同一交易日所有轮询复用一个 `run_id`。
- TEST 与生产 schema 均为 `30 oc_credential_audit`，Python SDK 当前版本为 `relay-sdk==0.1.43`。
- 公网绩效写入口和生产下单权限保持关闭；本机任务可按质量门禁写入版本化绩效结果。

### 当前进展与阻塞

- Chronos 跨日订单 R1/R2/R4 已完成；R3 TEST-only 人工遗留订单处置待实现。TEST 定义为一次 OC 进程生命周期一个 `counter_session_id`，进程内短线重连不变；OC 不识别 7x24 四时段，Relay 时间表只控制准入且不推导会话变化。生产正常 A 股逻辑严格隔离。详见 [需求响应](/home/ti-relay-trader/docs/CHRONOS_CROSS_DAY_ORDER_REQUIREMENT_RESPONSE_20260916.md:1)。
- Chronos 本轮验收把测试订单的 `accepted_at/last_updated_at=09:46:44` 与逐笔成交时间混淆；权威成交字段为 `Fill.matched_at=13:53:00+08:00`。Chronos 底层投影已读取该字段，仍需修正验收取证、页面或持久化消费口径；测试柜台状态时钟差异不外推到生产，详见 [Chronos 成交时间字段语义纠正](/home/ti-relay-trader/docs/CHRONOS_FILL_TIME_SEMANTICS_20260915.md:1)。
- 首页已移除主栏 `200/126/280px` 固定 Grid 行约束：5 个快捷入口自动排布，账户路由表按实际账户行数撑开，左右两栏共同决定 dashboard 高度。Playwright 已在 `1600x900` 与 `1366x768` 验证入口、6 行账户路由和右侧运行边界无裁切、无重叠。
- `/jobs` 已移除任务计划区的固定高度约束：任务卡按内容自适应，账户复核、历史记录和报告区随内容顺序布局，超出视口时由页面主区域统一滚动；任务报告工作区限制为随视口变化的 `360-520px`，完整 JSON 在模块内部滚动。Playwright 已验证 5 张任务卡无裁切、无区域重叠，78KB 长报告不会撑高外层页面。
- Chronos 在 `2026-09-14 19:11` 遇到的测试资产 HTTP 500 已定位为测试 PostgreSQL 缺 migration 28，并非 OC 未 ready。测试库已升级、目标库迁移已纳入环境切换前置门禁；`GET /asset?enrich=false` 和默认资产均恢复 200。资产错误现区分 `404 NOT_FOUND`、`503 ASSET_NOT_READY` 和 `500 INTERNAL`，复测证据见 [Chronos 测试资产基线验收](/home/ti-relay-trader/docs/CHRONOS_TEST_ASSET_BASELINE_ACCEPTANCE_20260914.md:1)。
- Chronos 消费端已独立关闭上述阻断并完成 Direct 两子单闭环；Relay 复核本轮两个 100 股子单均已撤成、零成交、零剩余，账户活动订单、命令 pending、Stream lag 和 DLQ 均为 0。该结论不外推到 TWAP、R3 故障注入或生产权限。
- 已修正盘前/盘后资产快照口径：OC 华鑫 `asset_page` 当前只返回可见资金，Relay 的 `open` 现在按上一交易日 Meridian 未复权收盘价估值盘前持仓，`close` 汇总当日收盘持仓，统一形成现金加证券市值的总资产；原始 OC 值继续保留审计，缺行情时阻断而不写现金-only 总资产。
- 标准日终资产另行列示 `reverse_repo_receivable`：按去重后的 `204001.SH` 当日成交计算本金应收，且只补成交时间不晚于资金快照捕获时间的本金，避免把晚于旧快照的成交重复叠加；应收加入总资产但不加入证券市值，预估利息继续延后到实际资金事件确认。
- `2026-09-14` 已从各账户确认的 Relay 可信起点重算历史 close 资产：`307000051387/1388` 从 `2026-07-27`、`307000051389` 从 `2026-07-28`、`314000046830/501000114077` 从 `2026-07-22` 起算。四个非空账户共修正 146 个账户日，其中 110 日从不可变 `broker_close` 结合 Meridian 日线重估，36 日使用已验收券商交割单 close 持仓汇总；93 个逆回购账户日按快照时点判为 84 日本金应收、9 日本金仍在旧现金。6,511 条持仓及 180 条资产恒等式零失败，133 条正式 NAV 只读复算数值零差异，因此不新增绩效版本。富盈13号仍等待起算日和期初持仓锚点确认，未纳入本轮历史重算。详见 [历史资产重估报告](/home/ti-relay-trader/docs/HISTORICAL_ASSET_REVALUATION_20260914.md:1)。
- 债享5号券商资金表覆盖 `2026-06-01..08-26` 共 62 个交易日，逐日资产恒等式全部闭合；正式绩效仍从有 OC 空仓干净锚点的 `2026-07-22` 起算。该日起 26 个交易日的日初、日末资产及每日盈亏均与券商记录精确到分，原 7 个 legacy 阻断日已全部恢复为 v2.7 provisional。
- 添利1号从 `2026-07-22` 干净起点开始核对。14 个已取得真实清算证据的 `159915.SZ` 赎回日已从 IOPV+15bp 暂估升级为版本化终值，公式使用实际 ETF 买入、OC 成分卖出、真实现金差额/现金替代、实际费用及 Meridian PCF；`2026-07-22..08-24` 共 24 个 NAV 日连续可算、归因残差为 0。`2026-08-25` 的 `512700.SH` 仍缺最终 cash component，保持 pending，8 月 25/26 日不发布伪终值。
- 债享5号资金流水一次性确认 14 笔逆回购实际净息、6 笔分红、3 笔红利税和 1 笔 `3,100 CNY` 银证转出。分红事实保留为 operational 审计记录，绩效由 Meridian 公司行为调整后的前收盘口径体现，避免重复计收益；逆回购净息和红利税进入 `income_expense`。
- 两份开户以来的券商历史资金各形成 23 个版本化资产基数金标；`2026-08-06` 通过可信 `reconcile` 快照承接 8 月 5 日 ETF 待结算资产后，两户残差降至 `-1,554.76 / -1,621.70 CNY`，状态由 blocked 改为 provisional。OC 原始 `open/close/broker_close` 快照未覆盖。
- 资金流水和历史资金只作本次事故的外部权威证据，不建设日常导入任务。未来仍依赖对应券商 OC 当日数据补充柜台范围、内部划转和资金明细；华鑫极速柜台可见性限制不得外推到其他券商。
- 两户均于 `2026-07-27` 盘前入金并开始交易；原起算日误用了 Relay 首次取得 OC 快照的日期（分别为 7 月 29 日和 28 日）。券商资金与交割单已一次性恢复两户 7 月 27 日及 `307000051387` 的 7 月 28 日账本，逐证券数量桥和 Meridian 收盘市值均闭合。
- 两户 `2026-07-27..2026-08-26` 各 23 个交易日、共 46 个账户日已按 `performance_economic_nav.v2.7` 顺序重建，0 blocked。结果仍为 provisional，因为部分历史费用、ETF 清算资产和归因使用明确标记的估算口径。
- `performance_position_cost.v3.2` 已实现 `meridian_pre_close_mark_to_market`：仅在人工确认起算日按盘前数量和 Meridian 未复权前收盘建立 CORE 初始成本，缺行情即阻断且不回退柜台污染成本。富盈13号仍待确认具体起算日和盘前持仓锚点，生产账户配置未修改。
- Chronos 已独立确认 `relay-sdk 0.1.33` 的 P0 数据与事件能力、P1 接口契约、全量分页审计和 SSE 对账页证据均通过。报告中的 4 条旧终态拒单正残量已追到 OC 原始归档：终态和零成交可信，但无标准拒绝文本；`trade_quality.v7` 不再把内部迁移审计 `reason` 误作柜台原因，原始账本保持不变且残量不可执行。不支持的北交所留作未来升级。
- `relay-sdk 0.1.34` 已按 Chronos R2c 要求补齐单笔、批量和撤单成功回执的稳定 `action`；真实测试覆盖单笔、批量、working 订单首次撤单和三类幂等重放，所有重放均为 0 次额外 Redis 发布。SDK 对缺失/错误动作按结果未知失败关闭，用户已确认 Chronos 完成当天联调，详见 [Chronos R2c 写回执验收](/home/ti-relay-trader/docs/CHRONOS_R2C_WRITE_RECEIPT_ACCEPTANCE_20260914.md:1)。
- `2026-09-02` 的 14 条 `meridian_watermark_poll` 是 `16:40..18:50` 对同一权威绩效复算的重复水位检查，并非 14 个独立任务；根因是 Meridian 当日父任务受 3 只新股行业映射缺口阻断。现已补写单一终态记录 `performance_canonical-20260902-watermark-poll`，旧记录保留审计但在页面折叠。

### 下一步

Chronos 完整历史 close 持仓基线 P0 已发布：GET-only 完成证明、内容寻址 ID、跨页重建 409、全量数量摘要及 SDK 0.1.43。四户历史 13/1/7/1 行与真实空账户只读验收通过，10,001 行及超限/部分状态测试通过；API Console 已有表单入口。下一步可交给 Chronos 独立验收，当前时点原子基线尚未实现。详见 [P0 回执](/home/ti-relay-trader/docs/CHRONOS_POSITION_BASELINE_RESPONSE_20261013.md:1)。生产环境和零下单权限保持不变，独立 worker 未重启，未发送 OC 查询或重建真实账表。

Chronos 费率预算已完成用户确认、订单级计算及四户版本化发布，可通知其使用 SDK 0.1.42 做只读消费端验收。有效日从 `2026-10-11` 开始，股票最低收费/券商舍入仍未核验，不混同实际费用或开放生产交易。详见 `docs/CHRONOS_FEE_RULE_PUBLICATION_RESPONSE_20261011.md`。

10月16日两户连续绩效已恢复：387的真实正退款/负现金差额、实际费用和券商reconcile基数已按版本写入，顺序重建9..16日；388的3300万外部入金按09:55:53登记，逆回购本金及加权收益验证通过。截至16日两户各54条current NAV、0 blocked；12/13日约±1817元归因差异仍保留。写前备份和实际结果见 `docs/PERFORMANCE_CONTINUITY_RECOVERY_20261016.md`；该日权威行情后续由既有任务处理，此处不冒称已完成复核，文件不进入日常任务。

下一优先项是取得OC实际发布包并安排安全只读窗口：P0代码和工具已由OC报告交付，在线资金/清算验收未完成。见 `docs/OC_CASH_P0_DELIVERY_REVIEW_20261019.md`。先离线核验，再由用户安排无冲突窗口；不可绕锁、抢占生产或自行切换环境。wire暂不变，不用余额与行情补造清算，不要求每日人工导出。768已有9月9日至10月9日限定基线和17日绩效；更早及10月12日后申赎、4日归因告警继续待核，见 `docs/FUYING13_PERFORMANCE_RECOVERY_20261019.md`。

768账户已发布375点历史，原计划与回执在 `outputs/recovery/fuying13_continuous_20261020_verified/`。10月16日复核发现：98819条已导出ETF现金匹配不证明尚未导出的待结权益完整，当前尾段收益不能作为已核验结论。原版本、607条成本状态、9/9成本起算及OC事实保留，旧 `performance-fuying13-continuous-recovery -persist` 已暂停；离线输出仅供诊断。优先处理159869现金分类及跨日权益承接，见 `docs/FUYING13_ETF_TAIL_REVIEW_20261020.md`，不拿人工约亏4万元直接改账。

4月起初始现金、整笔56.93元结息及债券剔除政策已由用户确认。原65点的资产、532.28元盈亏及7/7单位净值已在全段统一公式中保留，7/3管理交接边界不变；原始预览包和历史发布记录不改写。此前82点缺口报告仅记录中间状态，不再代表当前查询结果，见 `docs/FUYING13_HISTORY_CONTINUITY_GAPS_20261020.md` 的后续状态说明。

1. 在保留 `117.186.20.114` 安全组黑名单的前提下，安排生产 Redis 凭据轮换并只更新 Relay 与当前生产 OC，防止不可登录旧主机更换出口后重新连接；未经用户明确指令不得开放生产下单。
2. 与 OC 协调当前交易日的多柜台资金范围、按需柜台划转事件和资金明细字段，让今后同类日直接依赖 OC，不要求 OC 提供历史查询。
3. 从后续自然交易日持续验收 OC 当日资金、逆回购净息、公司行为和外部资金流，确保历史券商文件只停留在一次性事故修复边界。
4. 等待添利1号 `2026-08-25` 赎回的真实清算资金证据；到账后以同一版本化终值口径完成 8 月 25/26 日，不使用 PCF 预计现金提前确认。
5. 富盈13号账户净值已连续至10月19日；继续核查历史证券成本、策略归因、5日旧OC数量差异及券商未拆分资产科目，不回退受申赎污染的柜台成本，不因归因未完成隐藏账户净值。
6. 次优先项为内部 Webhook 告警实配、数据库异机备份及长区间交易质量查询性能优化。
7. 当前处于生产环境且下单关闭；任何环境切换、Stream namespace 调整或生产交易权限变更都必须收到用户新的明确指令。

## 系统边界

- **Go**：9092 在线 API、交易终端、API Console、多账户路由、订单状态机、Redis Stream 消费、PostgreSQL 账本和健康监控。
- **Python**：盘前初始化、收盘捕获、盘后结算、绩效计算、历史修复和验收脚本。
- **OC 前置**：统一券商协议和字段，负责交易日当天的命令、查询、订单、成交、费用和 ETF 划转事实；不提供历史查询能力。
- **PostgreSQL**：测试库 `relay_trader_test`、生产库 `relay_trader`，保存标准账本、快照、任务、对账、成本和绩效版本。
- **Redis Stream**：Relay 与 OC 的实时通信协议，遵循 `relay.stream.v1`；worker 独占生产 output stream 消费并持久化 checkpoint。
- **Meridian**：证券主数据、交易日、Level1、bars、adjust factors、ETF PCF/IOPV 的唯一行情标准来源。Relay 只做薄代理和业务计算，不另造行情字段。
- **模拟撮合**：不属于 Relay；实盘接口调试使用券商测试环境，历史撮合归回测引擎。

详细设计见 [架构文档](/home/ti-relay-trader/docs/ARCHITECTURE.md:1)、[交易接口 Schema](/home/ti-relay-trader/docs/TRADING_API_SCHEMA.md:1) 和 [数据模型](/home/ti-relay-trader/docs/DATA_MODEL.md:1)。

## 核心契约

- 环境选择完全在 Relay 服务端完成，SDK 只连接 `base_url`；测试和生产使用独立 Redis 配置与 PostgreSQL 数据库。环境切换属于受保护操作，只能依据当前用户明确给出的目标环境执行，时间提醒、历史指令和任务计划均不构成授权。
- 真实凭据只允许存在于未跟踪配置，例如 `config/relay.prod.yaml`，不得写入 README、日志、提交或前端响应。
- 生产切换默认只读。只有账户 `enabled=true && trading_enabled=true` 才允许发交易命令，生产启动脚本还要求显式人工确认。
- 订单业务唯一键为 `account_id + trade_date + gateway_order_id`；`req_id` 是客户端请求 ID，`order_id` 是柜台 ID，`order_stream_id` 是交易所/柜台委托流 ID，均保留用于关联和审计。
- 本地订单首次提交的 `origin_message_id`、`request_id` 和 `idempotency_key` 是不可变命令身份，后续查询回报和状态事件不得覆盖；批量子单通过同一 `origin_message_id` 完整回查。
- 成交必须关联订单并按账户、交易日、订单作用域幂等；ETF 赎回 0 价成分划转使用 `transfer.event`，不得伪装成普通成交。
- 逐笔成交时间唯一消费口径为 `fill.matched_at`；订单 `accepted_at/last_updated_at/terminal_at` 只描述订单生命周期，不能用于成交展示、成交回调、TCA 或逐笔成交事实。
- 相同幂等键和相同 payload 返回原回执并标记 replay；相同键不同 payload 返回 `IDEMPOTENCY_CONFLICT`，终态订单不得被重复提交回退。
- 测试环境订单和成交默认查询东八区自然日，生产环境默认查询 Meridian 最近交易日；历史订单、成交和持仓使用独立历史接口。表格查询使用服务端 cursor 分页。
- 价格展示位数和可报步长分别读取 Meridian `price_decimals`、`price_tick`；当前沪深股票为 2 位、ETF/可转债为 3 位。北交所未纳入当前实盘能力。
- 绩效正式净值使用资金、Meridian 重估持仓、确认资金流和可审计调整，不依赖 ETF 申赎后受污染的柜台平均成本。
- 原始 Redis 消息、DLQ、券商回包和修复证据永久保留；修复采用版本化或审计记录，不覆盖原始事实。

Redis Stream 细节见 [前置对接手册](/home/ti-relay-trader/docs/THIRD_PARTY_INTEGRATION_GUIDE.md:1) 和 [账本同步设计](/home/ti-relay-trader/docs/REDIS_LEDGER_SYNC.md:1)。

## 交易日流程

| 时间/触发 | 任务 | 关键行为 |
| --- | --- | --- |
| 09:01 | `pre_open_init` | 判断交易日，刷新账户数据，固化 `open` 资金和持仓 |
| 15:01 | `post_close_capture` | 不依赖 Meridian，查询 OC 最终资金、持仓、订单、成交和费用，固化不可变 `broker_close` |
| 捕获成功后 | `post_close_settlement` | 从 `broker_close` 结合 Meridian 生成正式 `close`、对账输入和差异 |
| 结算成功后 | `performance_daily` | 计算移动成本、经济 NAV 和质量状态，阻断账户不影响其他账户 |
| 17:10-19:10 每 10 分钟 | `performance_canonical` | 等待 Meridian 16:40/16:50 校准及 17:05 状态机收口；同一交易日轮询复用一条任务记录，19:10 仍未就绪才明确标记上游阻塞 |

非交易日通过 Meridian 交易日接口跳过。账户级查询失败单独标注；只有系统依赖失败、全部账户阻断或写库失败才使整项任务失败。完整流程见 [交易日工作流](/home/ti-relay-trader/docs/TRADING_DAY_WORKFLOW.md:1)。

## 运维速查

```bash
# 运行状态、启动、重启和日志
scripts/relay-runtime-service.sh status
scripts/relay-runtime-service.sh start
scripts/relay-runtime-service.sh restart
scripts/relay-runtime-service.sh logs-api
scripts/relay-runtime-service.sh logs-worker

# 测试/生产环境切换；生产默认拒绝带下单权限的配置
scripts/switch-relay-env.sh test
scripts/switch-relay-env.sh production

# 安装容器重启自恢复与交易日任务 cron
scripts/relay-runtime-service.sh install-cron
scripts/trading-jobs-cron.sh install

# 基础验证
curl -fsS http://127.0.0.1:9092/healthz
curl -fsS http://127.0.0.1:9092/v1/status
go test ./...
PYTHONPATH=src:sdk/python .venv/bin/python -m unittest discover -s tests/unit -p 'test_*.py'
PYTHONPATH=sdk/python .venv/bin/python -m unittest discover -s sdk/python/tests -p 'test_*.py'
```

生产日志位于 `/var/log/relay/`；任务报告位于 `/var/log/relay/reports/`；canonical 完成标记位于 `/var/log/relay/state/`。完整说明见 [运维手册](/home/ti-relay-trader/docs/OPERATIONS.md:1) 和 [运行进程说明](/home/ti-relay-trader/docs/RUNTIME_PROCESSES.md:1)。

## Web 入口

| 路径 | 用途 |
| --- | --- |
| `/` | 项目文档首页和环境摘要 |
| `/trade` | 交易终端、资金持仓、订单监控、绩效分析与设置 |
| `/api-console` | Apifox 风格接口测试台 |
| `/jobs` | 盘前、盘后和绩效任务状态 |
| `/operations` | Gateway、Redis lag、checkpoint 和 DLQ 运维 |
| `/sdk/` | Python SDK 下载与文档 |
| `/docs` | 全部设计与运行文档 |
| `/docs/readme-history-20260826` | README 精简前的完整历史归档 |
| `/tests` | 测试目录与页面冒烟入口 |

## 核心里程碑

| 阶段 | 状态 | 已交付结果 |
| --- | --- | --- |
| P0-P1 | 完成 | 9092 文档门户、Go/Python 工程骨架、配置、日志、测试和线程恢复机制 |
| P2-P4 | 完成 | 标准交易 schema、多账户路由、Redis Stream 命令/事件/恢复/checkpoint/DLQ |
| P5-P6 | 持续生产化 | PostgreSQL 标准账本、交易 API、SSE、Python SDK、API Console 和交易终端 |
| P7 | 持续生产化 | 盘前、收盘捕获、盘后结算、任务页、对账和账户级故障隔离 |
| P8/N8 | 完成主能力 | 历史查询、绩效页面、CSV、收益/回撤/贡献/成本/质量展示 |
| N9-N12 | 完成 | Stream 运维、环境隔离、人工复核、回归测试、API/worker 常驻和发布流程 |
| N13 | 进行中 | 可信成本、公司行为、ETF T0/CORE 分账、实际费用、经济 NAV 和 canonical 复算 |
| P9 | 暂缓 | 模拟撮合归回测引擎，不进入 Relay 实盘边界 |

详细完成项和验收证据以 [开发路线图](/home/ti-relay-trader/docs/ROADMAP.md:1) 为准。精简前逐日工作日志保存在 [历史归档](/home/ti-relay-trader/docs/README_HISTORY_20260826.md:1)。

## 关键文档

- [开发路线图](/home/ti-relay-trader/docs/ROADMAP.md:1)
- [架构与当前实现](/home/ti-relay-trader/docs/ARCHITECTURE.md:1)
- [交易接口 Schema](/home/ti-relay-trader/docs/TRADING_API_SCHEMA.md:1)
- [交易终端](/home/ti-relay-trader/docs/TRADING_TERMINAL.md:1)
- [Python SDK](/home/ti-relay-trader/docs/PYTHON_SDK.md:1)
- [绩效分析设计](/home/ti-relay-trader/docs/PERFORMANCE_ANALYSIS_DESIGN.md:1)
- [绩效净值金标](/home/ti-relay-trader/docs/PERFORMANCE_NAV_GOLD.md:1)
- [两户券商资金流水一次性审计](/home/ti-relay-trader/docs/BROKER_CASH_FLOW_AUDIT_20260827.md:1)
- [添利1号 ETF T0 最终清算审计](/home/ti-relay-trader/docs/TIANLI1_ETF_SETTLEMENT_RECONCILIATION_20260828.md:1)
- [Meridian 证券价位契约验收](/home/ti-relay-trader/docs/MERIDIAN_INSTRUMENT_PRICE_TICK_REQUIREMENTS_20260902.md:1)
- [Meridian 盘后水位协调](/home/ti-relay-trader/docs/MERIDIAN_POSTCLOSE_READINESS_COORDINATION_20260826.md:1)
- [OC 生产重复会话事故](/home/ti-relay-trader/docs/OC_PROD_DUPLICATE_SESSION_INCIDENT_20260921.md:1)
- [2026-08-06 延后结算记录](/home/ti-relay-trader/docs/SETTLEMENT_HOLD_20260806.md:1)
- [数据库迁移](/home/ti-relay-trader/docs/MIGRATIONS.md:1)
- [备份与恢复](/home/ti-relay-trader/docs/DATABASE_BACKUP_RESTORE.md:1)

## 状态维护规则

- 重要变更只更新本 README 的恢复卡片、当前阻塞、下一步和核心里程碑；详细实现清单更新到 `docs/ROADMAP.md`，专项结论写入对应文档。
- 不再在 README 追加逐日工作日志。需要长期保留的故障、验收和决策应写独立文档并在路线图中链接。
- 每次项目更新后自动执行 Git 提交。
- 禁止把密码、Token、DSN、生产柜台地址或其他敏感信息写入仓库。
