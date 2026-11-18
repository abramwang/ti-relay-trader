# CTP TEST 常驻配置

本说明用于下一轮经授权的 TEST 整日联调。不是生产上线说明，也不是新的登录、结算或交易授权。使用现有主程序，不增加模拟柜台或临时直连程序。

## 固定部署

- 固定已验收的程序及配套 v6.7.13 SDK；不要在运行期间自动替换版本。
- 沿用双方确认的 gateway、group、epoch 和本地 state_directory；不要为测试次数换路由、删除 journal、清 pending 或重建丢失的 epoch。
- 一个账户一个进程；启动前检查已有实例。日志、flow、身份、outbox、审计及 settlement once 一并保留。
- TEST 强制 `environment=test`、`is_production_mode=false`。本轮仍只支持已配置的东吴账户及官方前置，不是通用生产入口。

## 只读常驻

在既有受限只读配置上设置下面三个顶层字段，其他身份、Redis 绑定和凭据不改：

```json
{
  "duration_seconds": 300,
  "process_duration_seconds": 32400,
  "max_commands": 1000
}
```

`process_duration_seconds` 是本次进程最长存活秒数，范围 1..86400；未提供时仍使用原 `duration_seconds`。32400 表示最多九小时，不是每五分钟重启。`max_commands` 为本进程接收查询的上限，不是 SDK 调用次数；一笔事实查询有两段 SDK 采样。总 journal 仍有 2000 条命令的保护上限，已有历史也占用空间，不能删历史来续额度。

```bash
./oc_trader_commander_ctp --test-readonly-config /绝对路径/readonly.json --check-config
./oc_trader_commander_ctp --test-readonly-config /绝对路径/readonly.json --run-test-readonly
```

第一条仅预检。第二条会登录真实 TEST 柜台并消费已登记查询，必须安排窗口后运行。保持 `settlement_confirm=never`、`trading_enabled=false`；常驻参数不授予结算或交易权限。支持原四查询及 `instrument.constraints.query`。查询失败仍是失败，不把资金 BizType=00 补成期货资金。

## 交易日常驻

复用现有 assessment 配置和 `--test-assessment-config` 入口，不新增 runtime_mode。顶层使用 `process_duration_seconds=32400`、`duration_seconds=300`、`max_commands=1000`；assessment 使用：

```json
{
  "permission_profile": "test_day_relay.v1",
  "starts_at": "2026-11-19T09:00:00+08:00",
  "expires_at": "2026-11-19T15:00:00+08:00",
  "trade_date": "20261119",
  "confirm_settlement_once": false,
  "defer_unregistered_replay": true
}
```

日期只是配置示例，必须替换为双方授权的当日日期；不得沿用过期授权。其余 assessment 必需字段沿用现有配置，`fill_identity_version=v2`；原配置中的历史来源证明继续保留，不新增迁移要求，也不删除已有证明。

```bash
./oc_trader_commander_ctp --test-assessment-config /绝对路径/assessment.json --check-config
./oc_trader_commander_ctp --test-assessment-config /绝对路径/assessment.json --run-test-assessment
```

- 可提前启动登录；day 模式的只读查询不再要求进入报撤时段或先完成隐式合约初始化，仍需连接和恢复状态就绪，遵守串行流控。
- 报撤只在配置的当日日间授权和当前必要门禁内执行。午休及到期关闭报撤，进程继续接收迟到回调和只读查询，直到进程时限或 SIGTERM。
- 不自动发单、撤单、平仓或重复确认结算。`confirm_settlement_once=false` 的重启按原 once 和真实状态查询恢复；未知结果先核对。跨交易日 once 更新尚未自动化，不能删 once 文件获得第二次授权。
- 无关外部回报可延期留证，但不伪造完整标准订单页；身份冲突、未决执行和持久化故障仍阻断对应运行责任。
- 当前保留逐单原生检查。Relay 独立事实发布及准入替代路径联合通过后才移除，不因常驻配置跳过。

## 停止与验收

用启动时记录的准确 PID 执行 `kill -TERM PID`，保留最终心跳和 `readonly.audit.jsonl`，不要按模糊名称批量杀进程或删除状态。停止前先由 Relay 清点可执行订单、成交和未决责任；OC 不自动清仓。

下一轮固定版本覆盖开盘、午休、收盘及尾部回报，验证多笔报撤、拒单/拒撤后继续、查询失败后继续。在无未决交易且明确授权的窗口验证重启不重报。离线回归不代替整日在线稳定性、重连或生产验收。
