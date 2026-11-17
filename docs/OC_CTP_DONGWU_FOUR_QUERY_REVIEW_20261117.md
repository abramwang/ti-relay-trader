# 东吴 CTP 首轮真实四查询反馈

日期：2026-11-17，Asia/Shanghai。实际发送窗口09:24:25至09:24:30。

## 结论

真实TEST命令登记、Redis发送、OC回复、Relay原始收件已跑通：4条查询各发送一次、各收到一条回复，4条命令的本地文件/PG/Redis原文一致，4条回复的Redis/PG原文和SHA256一致。

**资金标准转换失败；四查询整体验收尚未通过。** 持仓返回12条并通过Relay行结构校验；订单/成交各返回完整空列表。尚未取得OC独立请求审计，不把回复中的ticket/SDK ID自证为已核实上下文，也未发布业务快照。

| 动作 | OC结果 | 数据/说明 | ticket / SDK request_id（回复宣告，待独立审计核对） |
| --- | --- | --- | --- |
| account.asset.query | failed / QUERY_FAILED | missing_biz_type；standard_projection失败 | 1 / 3 |
| account.positions.query | completed / OK | 12条，index=0、is_last=true | 2 / 4 |
| order.list.query | completed / OK | items=[]，index=0、is_last=true | 3 / 5 |
| fill.list.query | completed / OK | items=[]，index=0、is_last=true | 4 / 6 |

持仓按多空/昨仓等原始维度保留，不合并同代码多空，不把行结构通过升级为账户经济范围已核验。订单/成交空列表是本次回包事实，不证明非空交易链路已验收。

## 本轮身份与消息

- 路由：`test / ctp_4900 / ctp-dongwu-assessment-20261117 / 300052141`。
- 原生BrokerID=4900、UserID=300052141经OC确认认证/登录回包一致；InvestorID=300052141已明确配置，业务覆盖仍待原始结果核实。
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

以上是标准回复的自述，说明失败位置在OC资金标准转换，而非Relay未收到响应；不能仅凭这些字段断定SDK原始BizType为空、权限不足或柜台无资金。`callback_error_id=null`不能改写为券商返回0。

请OC一次性提供：

1. 本轮`readonly.audit.jsonl`中四条原命令、实际ticket/SDK请求、同步返回、原始各行及末页、标准转换、发布和ACK记录；提供纯数据即可，无需程序文件。
2. 重点核对资金第1项的原始`BizType`命名成员hex/实际长度，以及BrokerID、AccountID、CurrencyID、TradingDay、SettlementID。确认是柜台真实未填、驱动读取/编码丢失，还是标准投影误判。
3. 若原始BizType有效，修复投影并用相同原始输入离线回归；若真实为NUL/未填，先给证据，双方再确定显式未知范围表达。**不要猜填"1"、默认聚合多行、补零资金或去掉作用域约束。** Relay当前也要求非空单字符，不存在单改OC空串就自然兼容的假设。
4. 修正后只需安排新的资金采样窗口复测；已有三类回包保留，除非证据或实现变化确有需要，不重复整轮查询。新的采样有新命令身份，不覆盖原失败，也不重投旧SDK请求。

不需要新增原则回签。审计到齐后Relay复核事实；现有即时接收超时不会靠刷新batch或增加timeout改写，迟到证据按历史审计处理。尚无独立证据时不把raw pending强制改成delivered。

## Relay持久化与恢复

独立库`relay_ctp_dongwu_20261117_test`，Unix socket `.runtime/ctp-dongwu-pg-20261117`、port15434、user relay_ctp_audit；PG保留运行，无TCP监听，无自动服务。生产与9092业务库未改。

- 证据目录：`outputs/recovery/ctp_dongwu_joint_20261117/queries-20261117T012425Z/`。含4条固定命令、4条持久发送回执、4条原始回复观察、target、raw收件日志及`verification.json`。
- 4条发送责任均published，无未知发送；回复共8,695字节，全部落raw，独立review和业务快照尚未创建。
- reply owner=`79aee5722dc1aae8261aab380fc2bc3689c65ccf461a042eb12fda7b6a911326`，cursor=`1794878669835-0`，接收器已退出。
- 心跳按上一轮真实owner/cursor续读新增295条，共327条/404,767字节；最新owner=`3247fdb92e40e974e50772affb1271cc4981646a8448b048497babbd36f88761`，cursor=`1794878715012-0`，采集器已退出。所有PG原文SHA复算不一致数0。
- 后续只按上述真实位点显式接管，不重跑一次性四查询脚本、不清空数据库/Stream/pending。未发报撤、未确认结算、未重启OC/9092、未改环境或权限。
