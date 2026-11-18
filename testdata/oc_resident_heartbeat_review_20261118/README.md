# OC 常驻完整心跳

生成器实现：`bc7a7c7`，`ctp_assessment_runtime_test --resident-heartbeat-review FILE`。
这三条 body 来自实际主循环和心跳编码器，使用临时 JSON journal、假 SDK/IO；未登录、未投递任何真实 Redis Stream。

`heartbeat_bodies.json` 的 `cases[].body` 是完整 JSON 文本字符串，解析外层后对该字符串的 UTF-8 原字节核验 `body_sha256/body_bytes`，不要先重排内层 JSON。三个场景依次为正常常驻、容量满额、运行期锁存；均不是 stopping 摘要。

样例使用历史合成业务时间及测试账户路由，绝不能发送到实际账户 Stream，也不能据正常样例的 true 授权交易。`runtime.log` 是该次测试的控制台输出，其 Stream ID 是假 IO 返回值，不是 Redis 运输证据。此材料不证明 SQLite 后端或真实暂时断连恢复，作用仅为 Relay 对现有完整心跳的逐字复核。

本目录位于 OC 本机 `/home/Titian_Cpp/reference/resident_heartbeat_review_20261118`，未自动传送到另一台 Relay 主机。`SHA256SUMS` 校验三个文件原字节。
