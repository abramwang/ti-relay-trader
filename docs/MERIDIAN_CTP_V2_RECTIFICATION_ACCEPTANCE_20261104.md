# Meridian CTP v2 整改独立复核

日期：2026-11-04，Asia/Shanghai。来源：Meridian《Relay-CTP-v2复核整改回签》。接续 [初次复核](MERIDIAN_CTP_V2_REVIEW_20261104.md)，本文为当前状态，初次问题记录保留历史证据。

## 结论

V1 类型校验和 V2 每行请求隔离在本轮离线范围内复核通过，可关闭这两项整改待办。M1/M2 接收约定已明确；M3 的授权真实样本、SHFE 目录晋级、实时行情、完整夜盘及结算仍未验收。不能把离线通过升级为生产就绪或账户权限证明。

## Relay 实际执行

| 验证 | 结果 | 证明边界 |
| --- | --- | --- |
| review_meridian_query.py | 9/9，failed=0、退出码0 | 原五个错误类型反例现在均拒绝；控制组/范围/末页/重复身份符合预期，只用内存合成数据 |
| Meridian test_build_futures_instrument_catalog.py | 19 passed | 使用 Meridian 既有虚拟环境，禁用字节码和pytest缓存，测试输出写入临时目录，不发布实际目录 |
| test-ctp-futures-instrument-query-gate.sh | 编译及执行退出码0 | 编译临时二进制并执行假SPI门禁测试，不创建真实CTP会话 |
| 源码及六份SHA256 | 与回签完全一致 | 检查实际采集器在处理错误/追加行/末页前分类请求，行数来自当前请求；不是实盘故障注入 |

实际执行命令：

```bash
/home/ti_meridian_data/.venv/bin/python -B tests/ctpcontract/review_meridian_query.py --meridian-root /home/ti_meridian_data
/home/ti_meridian_data/.venv/bin/python -B -m pytest -q -p no:cacheprovider /home/ti_meridian_data/tests/test_build_futures_instrument_catalog.py
bash /home/ti_meridian_data/scripts/test-ctp-futures-instrument-query-gate.sh
```

未重新执行完整采集器构建脚本；该项仅记录 Meridian 报告本地编译通过。未运行 collector、登录、订阅、修改cron、部署或重启两方服务。

## 接收约定变化

- 整数严格拒绝 bool/float/string/null；布尔值只接受 true/false，数值必须有限。日期明确允许合法 YYYYMMDD 整数或字符串，Relay 接受这一显式 union，不推广为任意强制转换。
- 每条 query_completions 现在必填正整数 `connection_generation`，与 request_id、is_last_received、row_count 一并留证。仍沿用 v2 名称但契约指纹已变化，未来适配必须同时记录文件指纹，不只检查版本字符串。
- 连接代次必须来源于实际采集上下文，不能由 Relay 写常数补齐。旧候选缺此字段时不可伪造完成证明；如需兼容已留证旧样本，必须单独评审证据，不静默填默认值。
- 请求隔离门禁检查活动请求、请求号、连接代次；实际采集器另核对行的交易所，错范围失败关闭。假SPI测试覆盖错请求/错误/末页、旧代次、迟到旧行和超时后回调；没有据此宣称所有并发及真实断线场景已验收。

## 给 OC 的影响

这次变化是 Meridian 静态快照导入与采集门禁，不是 `relay.stream.v1` 或 `ctp.f0.draft.2` 交易 wire 升级。六份共享 JSON 不变，OC 不需要为本次状态更新重新验证同一批 JSON，也不要求增加 Meridian 客户端、全交易所扫描或新的 Redis 字段。

Relay 已更新 [给 OC 的回签确认](OC_CTP_DRAFT2_ACCEPTANCE_REVIEW_20261104.md) 及其说明文档，重新打包固定提交源码。旧 `51a4d6b` 材料包保留作历史，此次转交应使用新包，避免旧“V1/V2待整改”文字造成误解。OC 的金额材料、完整wire和实际持久恢复待办不变。

## 本轮证据指纹

| 文件 | SHA256 |
| --- | --- |
| ctp-futures-instrument-query-v2.json | 34e2cd952065770ebf492c084fe80870cf4d048f630f7f87bc37542f32d2623f |
| ctp-futures-instrument-query-shape-v2.json | b8a0c83aa2b75a5b3695f851cfc3407027f8f3134eecdc692aed0008721783fe |
| build_futures_instrument_catalog.py | cecd2ecd34e24dc3d8ede1fe1bc29bdbeb917cbcc11f829e677aa606954b968f |
| ctp_futures_instrument_sync.cpp | dd87e885af049281111bde0bfd160e58cd80acc2a54ef6c6b1ae36963aecb145 |
| query_request_gate.h | 9080044b818d9906daccc60a5db5952b925aad2982b703ddc0fe4e74643d8d4a |
| query_request_gate_test.cpp | 2625898e64d19aece5fb090a267430cab66b9d1098ce87983b6335e662da1cd8 |

下一步由 Relay 协调获授权真实 exchange_full 样本及目录/行情分阶段验收，继续采用 Meridian 标的标准；OC 可以继续不依赖在线行情的离线开发，不必等待上述窗口。
