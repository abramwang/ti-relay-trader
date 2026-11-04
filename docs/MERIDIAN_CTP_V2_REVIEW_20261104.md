# Relay 对 Meridian CTP v2 回签的复核与补充要求

日期：2026-11-04，Asia/Shanghai；离线复核约 21:23。来源为 Meridian 本日《Relay-CTP下一轮协调事项回签》。本文件由 Relay 对接 Meridian，不要求 OC 转交或适配 Meridian。

## 结论与状态

M1/M2 的文档和实现入口已交付，可以关闭“接收格式未知”的待办；不能据此关闭完整验收。本轮独立复核发现类型校验与请求隔离两项缺口，修正后再复验。M3 继续等待获授权真实样本，SHFE 生产目录和交易均不放行。

Meridian 报告发布版本 0.1.158；这是其服务发布信息，不把它误写成 Meridian Python SDK 版本。未独立核验其部署/重启过程，也未执行登录、采集、订阅或发布。本轮只读取本地契约、Shadow、源码，并在内存中调用校验函数。

## 需要 Meridian 修正

### V1：错误类型被静默转换，已离线复现

`scripts/build_futures_instrument_catalog.py` 的 `positive_int_required/nonnegative_int_required` 使用 `int(value)`，行数、request_id 等也先转换；生命周期通过 `bool(row.get("is_trading"))` 解释。

用同日 Shadow 构造仅在内存中的完整形状控制组，再每次改变一个字段，实际结果：

| 输入变异 | 当前结果 | 期望 |
| --- | --- | --- |
| min_limit_order_volume=1.9 | 接受并变为1 | 拒绝非整数，不截断 |
| min_limit_order_volume=true | 接受并变为1 | 拒绝布尔量 |
| row_count=238.9、实际238行 | 接受 | 拒绝，不能当行数闭合 |
| query_completions.request_id=true | 接受并当正请求号 | 拒绝 |
| is_trading="false"、phase="1" | 接受并规范化为active | 拒绝字符串布尔值，不能提升可交易状态 |

请在原始输入边界显式检查整数/布尔类型及范围，至少覆盖请求号、各层行数、委托量、乘数、交割年月和 is_trading；拒绝 null、非法字符串和非有限数，不靠强制转换修补。明确 JSON 整数的约束，避免 `bool` 作为 Python `int` 子类漏检。行情/金额的精度规则保持原契约，不套用股票两位小数。

现有拒绝门禁有效的对照：single_contract、is_last_received=false、重复原生身份均被拒绝；原始 sample_only 样例通过 validate-only CLI 返回退出码1、production_eligible=false，符合预期。

### V2：每条回调应先核对请求身份，静态源码风险

`native/ctp_futures_instrument_sync/ctp_futures_instrument_sync.cpp` 的 `OnRspQryInstrument` 当前顺序为：处理错误、追加 row、非末页直接返回、仅末页检查 response_request_id。错请求/迟到的非末页有机会进入 rows_；即使最后另一次正常末页完成，现有全局行数和 Shadow 对比也不能证明每行来自正确请求。

请在追加任何行、记录错误或完成标记之前，检查当前有效请求、请求号、连接/采集代次及请求范围。错请求/旧代次数据留诊断但不进入当前行集、不终结当前请求；完成行数按该请求实际接收行计算。具体实现由 Meridian 决定，不要求增加 Redis wire。

请补假 SPI 测试：错请求非末页、迟到旧行、错请求末页/错误、超时后回调、断线旧代次回调；均不得替代当前完整性证明。本项未运行 C++ 或在线故障注入，不宣称真实柜台已经发生串页。

## Shadow 抽查

本地质量报告为 20261104、238 合约/20 品种、production_catalog_promoted=false。读取以下原始 Shadow 行，不是自行定义标准代码，也不是与真实柜台逐行对账：

| Meridian security_id | 原码 | 乘数 | 最小价位 | 上市日 | 到期日 | 限价量边界 |
| --- | --- | ---: | ---: | --- | --- | --- |
| CU2611.SHFE | cu2611 | 5 | 10 | 20251118 | 20261116 | 未提供 |
| AU2611.SHFE | au2611 | 1000 | 0.01 | 20260818 | 20261116 | 未提供 |
| RB2611.SHFE | rb2611 | 10 | 1 | 20251118 | 20261116 | 未提供 |
| OP2611.SHFE | op2611 | 40 | 2 | 20251118 | 20261116 | 未提供 |

Shadow publication 为 `d88a721c08c8602e898145ec18fb7b44cadefcb5ff1f95a424b3a78a01dba16d`，checksum 为 `30b01604b4975795b8009026418af0cda13861f9c92b52230610766b1375ca97`。仅冻结本轮证据，不作为未来硬编码白名单。真实 Trader 快照、实际账户权限/可交易性均未取得，不能补委托量默认值或把 Shadow 提升为 production_ready。

## Relay 与 OC 的责任

v2 顶层范围/起止时间、逐交易所 request_id/IsLast/行数，以及31个必填合约行字段已有明确接收约定。Relay 可以据此评估适配，但尚未取得 OC 最新原始 QueryResult 样本，不能声称已验证无损映射。原名称 hex、未知/空字段、哨兵、原码大小写及失败证据都不能凭空补齐。

OC M2 交付明确为显式单合约查询；这只满足诊断，不能靠拼接或对照 Shadow 推造 exchange_full 请求及末页。优先考虑 Meridian 已有完整交易所采集器，在用户授权窗口独立采集；不因此要求 OC 实现全目录扫描。若选用 OC 已有真实完整证据，先由 Relay 核实是否存在及范围，然后承担格式适配，不让 OC 直接对接 Meridian。

InstrumentField 中的委托量和保证金参数本身不证明账户专属权限、账户最终保证金率或完整下单准入。“补齐柜台静态量边界”不能写成“账户权限已验收”。

## 复现与验收边界

在 Relay 根目录用 Meridian 的既有 Python 环境执行：

```bash
/home/ti_meridian_data/.venv/bin/python -B tests/ctpcontract/review_meridian_query.py \
  --meridian-root /home/ti_meridian_data
```

脚本不写文件、不调用 collector/build_catalog，不登录或发布。只 mock 原始 JSON 读取，调用真实 validator 和 Shadow 检查；所有控制组/变异均为合成，源 hash 也不用于证明柜台数据。当前9例中4例符合期望、5例错误接受，退出码1用于报告 V1，不代表 Relay 服务故障。该脚本是可选跨仓库复核，不加入常规 Go 测试依赖；环境或 Shadow 更新后需重新核对控制组。

本轮指纹：

- v2 机器契约：`f52c93d78041d39d366843c043be0c2ce5afe5d2dc2cb8e71b7ae726ca582bf8`
- 形状样例：`28c9a5038c3710ec6946cee8d40516ac9bd9aa01b3f35e0b1fa4f7e101b9204c`
- Python 校验器：`dbeb37b84bd7e6243db8a59beb1015c97e32fce79f806c45d1bbaeed4c78080c`
- C++ 采集器：`ed2b4b3e8a9acb89526d586a88ecdcfa05671228819579adb2168103c0591a69`

下一步 Meridian 修正 V1/V2 并回签，Relay 复跑类型反例并核对请求隔离测试；OC 继续金额/恢复离线工作。取得真实源授权后再进行目录晋级和独立行情验收。未修改两方工程，未改变生产环境、服务进程、凭据或账本。
