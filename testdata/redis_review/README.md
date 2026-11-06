# CTP Redis 恢复实测材料

说明：[OC 给 Relay 的交付说明](../OC_RELAY_CTP_TEST_REDIS_DELIVERY_20261106.md)。
最终代码基线 `07598735ea3f8bdc3a26e754a9f042b5f0b0f688`。

| 文件 | 用途 |
| --- | --- |
| `shared_test_redis_recovery.final.v1.json` | 原 TEST Redis 8.2.2 的最终 37 场景，含原始 body、Stream ID、PEL 与调用次数 |
| `private_redis_recovery.final.v1.json` | 私有 Unix socket Redis 7.2.14 的同组复验 |
| `asan_private_redis_recovery.v1.json` | 非 PIE ASan/UBSan 二进制在私有 Redis 的同组复验 |
| `ctest_20261106.txt` | 完整 27 项普通 CTest 原日志 |
| `asan_ubsan_20261106.txt` | 非 PIE sanitizer 的 RESP 解码、日志两入口原日志；37 场景另见 JSON |
| `shared_test_redis_recovery.v1.json` | 首轮 34 场景历史证据，不替代 final 文件 |
| `SHA256SUMS` | 上述证据与交付正文的原字节摘要 |

20 个崩溃窗口是九个共享窗口分别测试报单/撤单，再各增加一个 ACK 成功但本地确认未落盘窗口。
额外 ACK 窗口保持需核对，是安全阻断，不是自动完成。另有 15 个安全场景、2 个查询/重投场景。

所有账户、命令、SDK 返回与业务行均为虚构；Redis 操作和本地日志持久化是真实执行。
这些样例**不能投递实际 Stream**，不证明真实柜台交易或 Relay 端到端账本已通过。
JSON 的二进制指纹只用于绑定本次执行物，不包含二进制本身。原 body 内的样例时间不代表采集时间；
实际开始采集时间见顶层 `collected_at`（+08:00）。

归档：`oc-ctp-test-redis-review-20261106.tar.gz`，仅文档、JSON、原日志和校验清单。
解包后进入 `redis_review` 执行 `sha256sum -c SHA256SUMS`。
