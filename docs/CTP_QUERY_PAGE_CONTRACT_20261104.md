# CTP 四类查询页字段契约候选

日期：2026-11-04，Asia/Shanghai。范围：资金、持仓、订单、成交查询的输出报文。本轮是F0的可校验字段附录，不是完整交易协议冻结、生产解析器或F1数据库上线。

## 1. 交付及兼容边界

- 机器契约：[query-page.schema.json](../testdata/ctp/query-page.schema.json)，JSON Schema 2020-12，文档身份 `urn:relay:ctp:query-page:draft:1`；它约束的payload仍是 `ctp.f0.draft.2`，不是发布新的交易wire版本。
- 完整订单/成交查询页：[query_pages.json](../testdata/ctp/query_pages.json)。资金、持仓和未发出超时继续复用原 `messages.json`，原六份共享JSON字节未变。
- Python校验覆盖形状、严格JSON类型和单页语义，Go测试复用现有envelope解码；均离线、合成、无服务依赖。OC无需接收或运行Relay程序，按文档/schema/样例自行实现并反馈差异即可。

新增必填和候选限制须OC复核，不能把本轮通过当成双方已冻结；未知或不支持的事实应保存原始报文并标记投影失败，不删除行凑成功页。A股解析器、API、SDK、SQL和账户配置均不变。

## 2. 公共查询结构

| 字段 | 必填/类型与语义 |
| --- | --- |
| protocol / message_type | relay.stream.v1 / reply |
| message_id / origin_message_id | 非空字符串；后者关联原查询命令，不使用SDK请求号替代 |
| routing | env、路由broker_id、gateway_id、逻辑account_id；必须另与可信Stream/账户绑定核对 |
| action → result_type | account.asset.query → asset_page；account.positions.query → position_page；order.list.query → order_page；fill.list.query → fill_page |
| status / code | partial或completed必须OK；failed必须非OK。报撤accepted不属于本schema |
| produced_at | 合法自然日期时间，RFC3339且+08:00；不代替交易发生时间 |
| payload | 必含product_type=futures、schema_version=ctp.f0.draft.2；成功页还需trade_date和items；可选account_id若有必须匹配routing |
| chunk | 成功页必有index非负JSON整数及is_last布尔；partial=false、completed=true；failed不得附chunk |
| adapter_context | 审计扩展区，可保留原始字段；必含query_evidence。其余标准对象拒绝未声明字段，不自动忽略拼写错误 |

查询失败可以保留已确认trade_date，但不得伪造items或末页；空资金结果应走失败码而不是0元资产。完整空持仓、空订单、空成交允许items=[]。本候选不要求输出无意义的空资金分页片段。

### 查询证据

query_evidence必含ticket、sdk_request_id、sdk_return_code、callback_error_id、connection_generation、counter_session_id、snapshot_applied；可选failure_stage。ticket和SDK请求号不是北向命令ID。

成功页必须有正整数SDK请求号、同步返回0、callback_error_id为0或null，snapshot_applied=true；null仅表示源未提供错误号，不伪称返回0。OC应先完成原始查询及类型化应用，再分页输出，所以partial仅表示北向还有后续页，不表示OC已经把部分原始快照当作完整。

failed的snapshot_applied=false。queue_expired表示未调用SDK，其三项SDK/回调证据必须为null；不能补零。失败阶段字符串仅作审计，不在本轮发明新业务错误枚举。

connection_generation为正整数，来源于真实连接；不能靠常数补齐。原CTP查询候选本就有此字段，不能与Meridian后来新增的导入完成证明混同为新的Redis升级要求。

## 3. 四类行字段矩阵

### 资金

行必填account_id、captured_at和futures，不使用华鑫payload.account或股票net_asset。

| futures字段组 | 类型与规则 |
| --- | --- |
| scope | 原生broker_id、account_id、currency_id、biz_type、trading_day字符串；settlement_id非负整数，全部必填 |
| balance / available | 有符号普通十进制字符串，分别保留，不能直接推出全产品净资产 |
| curr_margin / frozen_margin / frozen_cash / frozen_commission | 同上，不重复加为净资产 |
| commission / position_profit / close_profit | 同上；不把账户费用分摊成逐笔费用 |
| deposit / withdraw | 同上；不凭字段名确认为外部资金流 |
| scope_verified | 本候选固定false，直到另有经济范围证据与版本化约定 |

scope.trading_day必须等于页trade_date；不同币种/BizType/资金账户/结算编号分别保留。页内同scope重复拒绝，不覆盖末行或直接求和。可信配置中的原生范围授权需另验，schema只检查结构和内部一致性。

### 持仓

行必填account_id、captured_at、原生symbol/exchange及futures：

| futures字段组 | 类型与规则 |
| --- | --- |
| posi_direction / hedge_flag / position_date | 必填单字节原生字符，原值保留；具体SDK枚举合法性仍须OC转换层核对，不能把字符形状通过当枚举已验收 |
| invest_unit_id | 必填字符串，可明确为空；不允许缺失/null。候选限可打印ASCII，最多16字节 |
| position / today_position / yd_position / long_frozen / short_frozen | 必填非负JSON整数，单位手，不计算position=今日+昨日，不跨方向/今昨/投资单元合并 |
| position_cost / open_cost / use_margin | 必填有符号普通十进制字符串，不转换为股票平均成本或市值 |

页内业务键为symbol、exchange及上述四个维度；路由/交易日在页级另绑定。同合约多空、今昨桶分别存在是正常结果，不能视为重复行。

### 订单与成交共同字段

account_id、gateway_order_id、counter_session_id、trade_date、symbol、exchange、business_type=F、trade_side=B/S、offset_type=O/C/CT/CY、hedge_type=speculation、native_ids必填。当前候选仅规范普通投机成交；不支持的原生业务仍保留审计并报投影缺口，不默认为普通交易。

client_order_id可省略，不捏造原客户端ID；行级product_type/schema_version可省略，若存在必须与页相同。captured_at在订单/成交行是可选采样时间，不替代源时间。行counter_session_id是该行关联的订单/成交会话，不强制等于当前查询进程，避免重启后错误改写历史身份。

| 对象 | 字段 | 类型/语义 |
| --- | --- | --- |
| 订单 | order_qty、cum_filled_qty、leaves_qty | 正委托量、非负累计/剩余整数；累计+剩余不得超量，活动单需闭合，全成需累计=委托且剩余0 |
| 订单 | limit_price | 普通非负十进制字符串；报告允许原始0，不把它当合法新报单价格。新单正价格/档位准入仍另验 |
| 订单 | gateway_status | accepted/working/filled/cancelled/rejected；未映射状态不可悄悄默认working |
| 订单 | submitted_at | 必填，已确认自然日期时间或null；未知不回填produced_at |
| 订单 | reject_code / reject_message | 可选字符串或null；仅保存已取得的标准拒因，不编造柜台文本 |
| 成交 | fill_id、qty、price | 确定性字符串ID、正整数手数、正十进制价格；只处理普通成交，不以0价制造成交记录 |
| 成交 | matched_at | 必填自然日期时间或null；TradingDay不能替代TradeDate，夜盘两者可不同 |
| 成交 | fee、fee_complete | fee必填十进制字符串或null，fee_complete必填布尔；true不能配null。非最终费用可有值但仍false，不代表最终交割 |

已撤订单的leaves_qty可以保留未成交数量，不代表仍能执行，不能一律改零；订单与成交关联/数量是否跨页闭合不是本轮单页校验所能证明。

### 原生编号

native_ids必含原生broker_id/investor_id。user_id、order_ref、order_sys_id、front_id/session_id/sdk_request_id按实际证据可选；后面三项为整数或null，不能用查询请求号覆盖订单的OrderField.RequestID。

成交还必须有非空trade_id、order_sys_id，禁止出现sdk_request_id（TradeField没有该字段）。外部UserID保留，不用采集登录用户替换。未关联成交暂存机制尚未完成，不能伪造gateway_order_id来满足本schema。

原生代码及ID候选限制为可打印ASCII；SDK字节上限沿用既有附录，前导零、大小写保留。三个填充编号须在保留原始审计后规范化；校验器不自行trim。其他编码如需支持必须明确版本与字节计数，不截断。

fill_id按既有七段原生身份重算；以external-ctp-v1-开头的外部gateway ID同样复核。普通本地gateway ID的真实命令映射、原生投资者授权和哈希冲突审计由后续持久化层核实，不能只靠ID非空通过。

## 4. Schema以外的强制检查

标准JSON Schema的integer可能接受数学整数1.0；本协议必须保留JSON词法类型，拒绝1.0、1e0、字符串及布尔。解码还须拒绝重复对象键和NaN/Infinity。日期format必须实际启用，不能仅匹配8位数字；金额不经float解析。

本地校验器额外检查路由/页/行账户与交易日、页内重复业务键、数量关系、fill/external ID和fee组合。提供可信expected上下文时，还检查原命令、action、ticket、连接代次、查询会话及SDK请求号；不传expected只证明自洽，不能作为真实准入结论。

价格按既有候选最多9位小数、64字节及未缩放uint64边界核验。金额只确认十进制字符串词法，不加价格九位限制、不舍入；OC serializer哨兵、极端值、负零、最大长度及binary64向量仍待核对。合法字符串不等于可信会计金额。

**尚未实现**跨页索引连续、请求完成时效、同消息重放/矛盾终态、页间重复、全快照原子替换、ACK/重启、未关联成交持久暂存。schema通过不意味着可替换账本，更不意味着能下单。

## 5. 本轮验证及下一步

Relay本地独立测试环境：`.runtime/ctp-contract-venv`，依赖固定为jsonschema 4.25.1，不改变运行服务、SDK或Meridian虚拟环境。[官方校验接口说明](https://python-jsonschema.readthedocs.io/en/stable/validate/)支持显式启用format和严格类型扩展；生产语言无须采用相同库。

```bash
python3 -m venv .runtime/ctp-contract-venv
.runtime/ctp-contract-venv/bin/python -m pip install -r tests/ctpcontract/requirements.txt
.runtime/ctp-contract-venv/bin/python -B -m unittest discover -s tests/ctpcontract -p 'test_query_contract.py' -v
go test ./tests/ctpcontract ./internal/redisstream ./internal/orderflow ./internal/credentials -count=1
```

17项Python测试方法（含多组反例）和四个Go包通过。未接入服务路由、原始消费、数据库或HTTP/SDK期货模型；本轮没有真实查询、下单、切环境或发布。

请OC只核对本字段附录、新schema及两份查询页报文，反馈字段缺口/类型差异；不是要求运行上述程序或重新复验原六份JSON。下一步Relay补跨页/乱序/重复终态的序列契约与测试，随后将已确认结构用于F1隔离模型；完整交易命令schema和金额共同向量仍未关闭。SHFE行情由Meridian独立推进，不成为这些离线工作的前置条件。
