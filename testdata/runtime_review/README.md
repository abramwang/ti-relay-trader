# 2026-11-07 TEST 运行时第一步材料

实现基线：`db65186`，规范路由修正：`aef9ebd`。
追加配置数字边界修正：`22574ce`；普通 CTest 日志覆盖该修正，Redis/sanitizer JSON 保留 `db65186` 的实际身份。
交付正文：`../OC_RELAY_CTP_RUNTIME_STAGE1_20261107.md`。

- `shared_redis_20261107.json`：真实 TEST Redis 的 48 个组件场景，SDK 为模拟函数。
- `main_cli_20261107.json`：实际主程序的预检、三次隔离进程执行、阻断和半行审计拒绝证据；没有柜台会话。
- `heartbeat_examples.v1.json`：从主程序证据选择的三种原心跳 body 和实际物理键，没有改写原 body。
- `ctest_20261107.txt`：28 项普通 CTest 原日志。
- `asan_ubsan_20261107.txt` 和 `sanitizer_private_20261107.json`：非 PIE sanitizer 检查及私有 Redis 组件复验；不是原 PIE 或厂商 SDK 验收。
- `SHA256SUMS`：文件原字节校验。

解包后进入 `runtime_review` 执行 `sha256sum -c SHA256SUMS`。
样例中的账户/订单均为虚构，不可投递业务流。原 body 里的历史样例日期与采集时间分离。
48 个组件场景不是 48 个在线柜台用例；主程序也不是实际四查询/交易服务。

归档名：`oc-ctp-runtime-stage1-20261107.tar.gz`。仅交接正文、JSON、日志和清单，不含配置/源码/二进制。
