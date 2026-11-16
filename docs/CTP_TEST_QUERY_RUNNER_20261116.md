# CTP TEST 独立查询入口

日期：2026-11-16，Asia/Shanghai。

## 当前已完成：校验与持久登记

新增`cmd/relay-ctp-query`，复用已有`DecodeCommand`、`CommandStore`与000037结构，不修改wire、不新增迁移、不连接行情或柜台。

- 仅允许TEST CTP的资产、持仓、订单、成交四类查询；生产、华鑫、报单、批量报单、撤单及未知动作均拒绝。
- 逻辑路由与native BrokerID/UserID/InvestorID分别显式配置，不从账户号猜后缀或从收到的reply反推身份。
- 读取已经固定的命令JSON，保留原字节、message/request/correlation/idempotency身份；默认零连接，输出目标cmd.query键和原文SHA256。命令文件最大64KiB，解析按既有严格契约。
- `--register --persist-test-intent`成对启用后，只向指定独立TEST数据库登记。DSN来自`RELAY_CTP_QUERY_DATABASE_URL`，不读取运行配置；显式库名须以`_test`结尾，并与DSN和实际连接库名一致。没有自动建库/迁移。
- 重复命令复用原command_id；同message或幂等键改变意图时保留冲突审计，CLI以非零退出。不覆盖原命令；报告同时列出当前输入及原始message/body摘要，避免混淆别名。
- 始终`command_published=false`、`broker_accepted=false`、`business_authorized=false`。登记成功不是发送成功，更不是OC接受或查询完成。

这个阶段**没有Redis发送实现**，`--run/--publish`不受支持。不能拿登记结果当交易许可，也不能自行接一个手工XADD绕过后续出站责任处理。

## 操作边界

以虚构身份说明默认校验形式，非东吴真实身份或可直接执行的业务命令：

```bash
go run ./cmd/relay-ctp-query \
  --env test --broker ctp --gateway relay-dev-query \
  --account 990000000117 --native-broker 4900 \
  --user fake-user --investor investor01 \
  --database relay_query_test --command-file /path/to/fixed-query.json
```

实际命令必须由操作者准备符合既有共享契约格式的原始JSON；不要复制测试夹具到真实账户路由，也不要每次重试刷新sent_at/message_id来绕过冲突。指定已有独立TEST库并明确授权后，才追加登记两开关；输出失败时先查登记审计，不能假定事务未提交。

## 验证

5项CLI/race测试通过，包括真实独立本机PG的登记、跨连接重开重放、改变动作冲突、原文不覆盖；其余覆盖四查询离线检查、危险参数、交易动作及错误脱敏。`go test ./...`和定向`go vet`通过；全量命令未配置集成库的测试依原规则跳过。

本次仅创建独立本机`relay_ctp_query_cli_test`并在随机schema执行000037测试，schema已清理、PG已停止。原心跳审计库仍17条/18,745字节/末位点`1794835752035-0`，未改。没有读取Redis凭据、没有向任何真实路由发送查询/交易、没有启动柜台或重启9092。

## 下一步：受控发送与结果关联

1. 增加持久化出站责任：登记、准备尝试、Redis已确认发布、发布结果未知分别留证。进程退出/网络超时/写后PG失败时不自动重发，不将“没有本地成功回执”解释成“未发出”。
2. 显式绑定实际TEST来源/run_id/epoch、账户与session/连接代次/TradingDay，并在发布前验证新鲜只读心跳和query_collection_ready。旧transport-only不能放行；不创建/清理OC组或改epoch。
3. 发布仅使用登记原文；保留实际Stream entry_id。按OC原始请求证据建立ticket/SDK request_id关联，再接既有分页收件/完成证明及快照持久化。未知字段不从首条reply自行取值当“独立预期”，必要时用本轮OC运行审计核实。
4. 在已授权白天窗口按资产、持仓、订单、成交逐项验收，严格区分完整空、失败、未完成及成功。不因本CLI完成登记就标整个真实四查询入口完成。

本轮没有新增OC或Meridian协议要求，双方无需再做原则回签；11-17 09:05仍是候选窗口，不是自动登录任务。
