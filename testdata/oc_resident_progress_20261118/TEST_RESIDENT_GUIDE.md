# CTP TEST 常驻配置

更新：2026-11-18。实现 `a53f446/4b8e217`。交易授权唯一归属 Relay。本说明不构成新的登录、结算或交易授权；新版本尚未部署。

## 升级配置

使用现有主程序、SDK v6.7.13 和原路由。新模板为 `config.execution.example.json`；模板含占位符，不能直接启动。保留现有 scope、Redis 来源/run_id/epoch/group/consumer、state_directory、凭据及已有 reconciliation，不创建新路由或删除历史状态。

- 删除整个 `assessment` 段，包括授权时段、permission_profile、合约/方向/手数及价格例外次数。
- 删除 `broker.trading_enabled`。执行入口内部启用 SDK 写能力，不是另一个需运维同步的账户许可。
- 保留 `broker.settlement_confirm="never"`，新增下面的 `execution` 段。
- 保留 `fill_identity_version="v2"`；旧 v1 状态不能直接改字段升级或删掉重建。
- 顶层 `process_duration_seconds=0` 或省略，表示常驻至运维 SIGTERM/SIGINT，不设自动退出倒计时。仅有意限定的离线/联调运行才填写正数秒数。
- 执行配置不再需要 `duration_seconds`、`max_commands`。旧执行配置保留 max_commands 不再形成累计查询限额；只读专用测评入口的限制未在本轮修改。

```json
{
  "trade_date": "20261119",
  "confirm_settlement_once": false,
  "defer_unregistered_replay": true
}
```

上例是 `execution` 的内容。trade_date 是本次预期柜台 TradingDay 身份绑定，不是按自然时钟划定的交易许可。日期须以本轮实际柜台为准；连接代次或交易日变化仍保守阻断执行，默认常驻入口保留诊断，不宣称自动跨日运行已实现。

`confirm_settlement_once` 默认 false，仅在 Relay/用户明确要求执行一次确认时设为 true。原 once 文件继续防重，未知结果不能删除重做；取消重复交易授权不等于自动确认结算。

旧 `assessment` 配置预检/业务启动明确返回 `legacy_assessment_authorization_removed_use_execution_config`。混合新旧配置、在新配置保留 `trading_enabled` 或塞入授权窗口/profile/次数均拒绝，避免静默扩大旧许可。历史模板、原材料及注入式离线用例保留，但不再是可启动的另一套业务授权路径。

## 启动与停止

CLI 名称和 runtime_mode 暂保留历史名称，避免另起程序或改变消息路由：

```bash
./oc_trader_commander_ctp --test-assessment-config /绝对路径/execution.json --check-config
./oc_trader_commander_ctp --test-assessment-config /绝对路径/execution.json --run-test-assessment
```

第一条不联网、不创建 flow/state。第二条会登录真实 TEST 柜台、消费 Relay 命令；配置显式确认结算时还有一次结算副作用，必须安排操作窗口后执行。业务逻辑没有自建撮合、自动报撤、平仓或报价选择。Redis 命令写权限应只交给 Relay 受控发布入口。

一个原生账户一个进程；启动前检查已有实例。停止使用记录的准确 PID：`kill -TERM PID`。保留日志、flow、journal、outbox、identity、审计及 once；有未决交易先由 Relay 核对，不能杀进程或清 pending 冒充撤单。

配置正数时仍限制在86400秒内；零值不受此时间上限限制。查询已领取数仅供统计，不再耗尽进程的查询资格。journal 存储硬上限仍保留，不能称为已经具备无限容量或完整无人值守能力。

新版容量心跳见 `OC_RELAY_CTP_RESOURCE_USAGE_20261118.md`：80%仅预警，journal满额停止新命令领取；历史输出和回调继续处理，但后续实际持久化失败仍会阻断柜台执行。不能删 journal、清 pending 或换路由来恢复“次数”。该增量尚未部署，需 Relay 接收器同步兼容。

默认常驻入口在运行期严重故障后保持阻断诊断状态，不自动退出、重建状态或重新执行命令；健康的诊断链路继续发 recovery_blocked 心跳，诊断链路失败时仅有界退避输出本地错误。此时柜台连接会关闭，不是业务恢复完成。配置/SDK校验或账户锁等启动前失败仍明确拒绝启动。当前重连、跨日与持久存储改造尚未完成，见 `OC_RELAY_CTP_RESIDENT_PROGRESS_20261118.md`。

## 执行与心跳

- OC 不再维护交易日时段、单笔额度、固定合约/买入方向的本地授权。午休或收盘不使执行能力自动失效，Relay 决定是否发布新单或撤单；OC 不读取或镜像 Relay 的授权状态。
- `authorization_owner="relay"` 位于 `adapter_context.admission_evidence`。`accepting_*` 表示连接、结算、编号、快照和恢复等实际执行能力，不表示用户已授权。
- 新单/撤单能力不由行情缓存时效决定。撤单目标在接收具体命令后核验；当前目标尚未存在不等于全账户撤单功能不可用。
- 当前已实现能力仍是 SHFE 普通限价投机单、B/S、O/C/CT/CY 和本地订单撤单；其他动作按实际未支持返回，不将能力限制称为授权白名单。
- 逐单原生合约/价格检查暂保留，只有收到新命令才准备所需事实；没有空闲后台行情刷新。Relay 替代准入通过后再移除，不因此猜价格或跳过必要检查。
- 命令首次接收检查 sent_at，超过 5 秒或来自未来时刻拒绝；准备阶段上限 15 秒，SDK 前仍核对原命令年龄和单调时钟期限。去掉授权时段不等于过期排队命令可无限执行。
- 身份/路由/来源校验、幂等、真实结算状态、OrderRef 防冲突、原输出持久化及发布确认后 ACK 保留；已发送或结果未知不自动重报。

新旧心跳最小字段差异和 Relay 接线见 `OC_RELAY_CTP_EXECUTION_AUTHORITY_20261118.md`。Relay 当前仍需部署对应严格解析与发布门禁，不能直接忽略旧心跳的 false 或把新能力 true 当作授权。

## 只读入口不变

只读专用入口继续使用 `--test-readonly-config FILE --run-test-readonly`，保持 `settlement_confirm=never`、`trading_enabled=false`。这是程序连接能力的区分，不是第二个账户交易许可。支持原四查询及 `instrument.constraints.query`，查询失败不把资金 BizType=00 补成余额0。

下一轮固定版本、路由及状态做真实独立事实、多笔报撤、拒绝后继续、受控重启及整日收件验收。本轮离线通过不代表生产、CentOS7、完整日间/夜盘或普通账户资金风险口径已经通过。
