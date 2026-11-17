# 东吴 CTP 首轮真实四查询反馈

日期：2026-11-17，Asia/Shanghai。实际发送窗口09:24:25至09:24:30。

## 结论

真实TEST命令登记、Redis发送、OC回复、Relay原始收件已跑通：4条查询各发送一次、各收到一条回复，4条命令的本地文件/PG/Redis原文一致，4条回复的Redis/PG原文和SHA256一致。

**资金标准转换失败；四查询整体验收尚未通过。** OC独立请求审计已收到并复核：持仓12条的原始字段与标准输出一致，订单/成交各有真实末页且为空；ticket/SDK ID已与原始审计关联。Relay已持久化原文，但尚未发布业务快照；历史审计核实不升级为当前交易准入。

| 动作 | OC结果 | 数据/说明 | ticket / SDK request_id（已独立核对） |
| --- | --- | --- | --- |
| account.asset.query | failed / QUERY_FAILED | missing_biz_type；standard_projection失败 | 1 / 3 |
| account.positions.query | completed / OK | 12条，index=0、is_last=true | 2 / 4 |
| order.list.query | completed / OK | items=[]，index=0、is_last=true | 3 / 5 |
| fill.list.query | completed / OK | items=[]，index=0、is_last=true | 4 / 6 |

持仓按多空/昨仓等原始维度保留，不合并同代码多空，不把行结构通过升级为账户经济范围已核验。订单/成交空列表是本次回包事实，不证明非空交易链路已验收。

## 本轮身份与消息

- 路由：`test / ctp_4900 / ctp-dongwu-assessment-20261117 / 300052141`。
- 原生BrokerID=4900、UserID=300052141经OC确认认证/登录回包一致；12条原始持仓的BrokerID/InvestorID/TradingDay与4900/300052141/20261117一致。资金AccountID=300052141，缺BizType仍不能证明完整资金作用域。
- session：`ctpproc-1040f2e889ecd8f7199e3ebf95914a38`；连接代次1，TradingDay=20261117。
- Redis run_id：`94f801dc68e3699cc681feb6dbb48f19e46aef9e`；epoch：`dongwu-prepared-164bb9020478c4a8c83e9cf25d9f8e34`。
- 命令组：`oc-ctp-dongwu-assessment-20261117`；09:26:33复核cmd.query/cmd.trade均pending=0、lag=0，cmd.trade长度0。PEL清空不代替OC本地durable ACK审计。
- 最新心跳09:25:14.999为stopping，本轮已结束，不再依据历史ready发送。

| 动作 | 原命令message_id | cmd.query entry_id | reply entry_id |
| --- | --- | --- | --- |
| 资金 | relay-dongwu-20261117-1794878665496969687-0 | 1794878665524-0 | 1794878665710-0 |
| 持仓 | relay-dongwu-20261117-1794878666728333872-1 | 1794878666760-0 | 1794878666984-0 |
| 订单 | relay-dongwu-20261117-1794878668170356306-2 | 1794878668198-0 | 1794878668428-0 |
| 成交 | relay-dongwu-20261117-1794878669604872438-3 | 1794878669629-0 | 1794878669835-0 |

## 资金问题与OC下一步

资金回复时间09:24:25.687，关键字段原值：

```json
{
  "status": "failed",
  "code": "QUERY_FAILED",
  "message": "missing_biz_type",
  "adapter_context": {
    "query_evidence": {
      "ticket": "1",
      "sdk_request_id": 3,
      "sdk_return_code": 0,
      "callback_error_id": null,
      "failure_stage": "standard_projection",
      "snapshot_applied": false
    },
    "standard_validation": {"raw_completed": true}
  }
}
```

以上是标准回复；现已对照独立原始审计确认`raw.rows[0].fields_hex.BizType="00"`，即单字节NUL，不是字符`'0'`（30）或`'1'`（31）。该行50个SDK命名成员齐备，BrokerID=4900、AccountID=300052141、CurrencyID=CNY、TradingDay=20261117，SettlementID原始hex为01000000（小端整数1）。原始结果completed、末页true、同步返回0、callback_error=null；不得把null补成柜台返回0。

OC报告拒绝点为`ctp_query_snapshot.h/sourceScope`的空BizType检查；另据其源码核对，资金查询请求零初始化后仅填写BrokerID和InvestorID，未显式设置BizType。**本包没有完整原生请求结构体抓包**，因此请求BizType=0是OC源码核对结论，不是Relay独立捕获的请求事实；也不能据此断定柜台回显请求或明确传1后必然恢复。

以下最小修正已由OC报告在`96c55f7`实现；最新接受口径及待在线项见下一节，不再要求重复开发或原则回签：

1. 仅期货资金查询请求显式使用SDK声明的`THOST_FTDC_BZTP_Future`，补充实际发送的BrokerID/InvestorID/AccountID/CurrencyID/BizType原值审计及离线回归；其他字段沿用明确配置，不猜账号、不扩大其他业务范围。
2. 保留响应原值和当前失败保护。**不得将响应NUL替换为"1"、跨作用域聚合、补零或改写此次失败。** Relay本轮无wire/解析放宽；新增六种缺失/空/NUL/非字符串BizType回归，确认整批拒绝且原文不变。
3. OC修正完成后，双方明确新只读窗口，Relay只发一笔新身份的`account.asset.query`；核对请求BizType、真实响应、末页、scope及标准结果。资金通过不等于报撤获准。
4. 若响应仍为00，保留失败及原始资金，联合核实柜台语义后再设计显式未知范围表达；不能为了通过验收在Relay单方补默认值。已有持仓/订单/成交证据无需无条件重采。

本轮不授权自动启动/登录或发送新查询。原即时接收时限已过，迟到证据按历史审计核实；不刷新batch/延长timeout伪造及时完成，raw pending不强制改为delivered。

## 接受OC定向修正说明

用户转交OC实现`96c55f7`（基于`db59783`）：资金请求显式使用字符`'1'`（hex31），AccountID/CurrencyID保持原空字段，不猜资金账户或币种；新增`query_request`审计，响应标准化及wire不变。Relay接受此最小修正方向，不需要新增解析兼容或放宽资金scope。

OC报告Ubuntu33项测试及非PIE sanitizer4项通过，但本机`reference/testdata`尚未发现所述`biztype_review_20261117`材料，故仅登记为OC自测结论，不宣称Relay已独立复验，也不以此认定已部署/在线资金通过。不要求为缺材料重做一轮原则讨论。

下一轮由用户确认已部署该版本并启动新的只读窗口后执行，保持已有路由/组/epoch，新session、新message/idempotency，仅一笔`account.asset.query`：

1. Relay重新读取新鲜只读心跳并核对来源、恢复状态、session/代次/TradingDay，先持久登记再发送。旧命令/失败回包不重写、不重投。
2. OC提供同一请求的`query_request`：api=ReqQryTradingAccount、stage=before_sdk_call，以及BrokerID/InvestorID/AccountID/CurrencyID/BizType五个全长成员hex；请求BizType应为31，其余身份与明确配置一致。
3. `before_sdk_call`只证明调用前对象已审计，必须再关联同message/ticket/SDK ID/session/代次的同步返回、query_result、真实末页、原始资金scope及标准reply。审计写入失败不得推定SDK已调用，更不得自动重发。
4. 若真实响应BizType为31且其他校验通过，再确认该次资金采样有效；若仍00，保留failed/missing_biz_type并向柜台核实，不覆盖响应或单方引入未知scope兼容。
5. 只复测资金，不无条件重采已核实的持仓/空订单/空成交。成功不自动升级为全账户经济范围、最终清算、业务快照发布或报撤通过；逐命令持久ACK/恢复仍按其专项证据验收。

本次仅接收说明和更新恢复记录，没有访问业务Redis、启动/重启、查询、确认结算、报撤或环境切换。原独立审计及数据库位点保持不变。

## 独立材料复核

材料位置`reference/query_review_20261117/`，7项SHA256清单全部通过。完整前缀359行的sequence、previous及逐record SHA256全部通过，当前会话319行与前缀第41..359行逐字节一致。摘要链是完整性证据，不冒充第三方签名。

- 4条命令与Relay固定命令文件、PG登记、Redis发布ID匹配；4条OC发布回复与Relay PG原文、摘要及物理ID匹配。
- 4项query_result分别有1/12/0/0原始行及真实末页；ticket、SDK request_id、session、代次、交易日、同步返回与标准reply一致。三项成功采样可确认，资金失败原因可确认。
- 12条持仓逐行核对原生账户/证券/市场、多空/套保/持仓日期/投资单元、数量及冻结量；OpenCost/PositionCost/UseMargin十进制文本转换回double后，与SDK小端原始字节一致，不人为舍入。
- 当前会话295条心跳与Relay PG原文一致；start记录明确production_mode=false、trading_enabled=false、settlement_confirm=never。
- 包内没有逐命令durable ACK原始记录，只有stopped/unresolved_commands=0及此前Relay读取的PEL=0；故不宣称已独立完成持久ACK/进程恢复验收。无需为资金定向修正重复索要整包，后续恢复专项再补此项证据。
- 原生资金请求字段审计尚缺，不能把源码检查当抓包。业务快照发布及非空订单/成交、报撤/实时事件仍未验收。

复核报告`outputs/recovery/ctp_dongwu_joint_20261117/queries-20261117T012425Z/oc-independent-review.json`，本机只读核验脚本`.runtime/ctp-dongwu-review-20261117.py`；原始材料与旧报告未覆盖。未执行新柜台查询、重启、结算确认、环境或权限变更。

## Relay持久化与恢复

独立库`relay_ctp_dongwu_20261117_test`，Unix socket `.runtime/ctp-dongwu-pg-20261117`、port15434、user relay_ctp_audit；PG保留运行，无TCP监听，无自动服务。生产与9092业务库未改。

- 证据目录：`outputs/recovery/ctp_dongwu_joint_20261117/queries-20261117T012425Z/`。含4条固定命令、4条持久发送回执、4条原始回复观察、target、raw收件日志及`verification.json`。
- 4条发送责任均published，无未知发送；回复共8,695字节，全部落raw，独立review和业务快照尚未创建。
- reply owner=`79aee5722dc1aae8261aab380fc2bc3689c65ccf461a042eb12fda7b6a911326`，cursor=`1794878669835-0`，接收器已退出。
- 心跳按上一轮真实owner/cursor续读新增295条，共327条/404,767字节；最新owner=`3247fdb92e40e974e50772affb1271cc4981646a8448b048497babbd36f88761`，cursor=`1794878715012-0`，采集器已退出。所有PG原文SHA复算不一致数0。
- 后续只按上述真实位点显式接管，不重跑一次性四查询脚本、不清空数据库/Stream/pending。未发报撤、未确认结算、未重启OC/9092、未改环境或权限。
