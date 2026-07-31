# PostgreSQL 备份与恢复

更新时间：`2026-07-31`

## 目标与边界

Relay 使用 PostgreSQL 保存订单、成交、ETF 划转、资金持仓快照、任务、对账、绩效输入和 Redis Stream 原始归档。备份必须同时覆盖业务账表、`raw_stream_messages` 和 `relay_schema_migrations`，恢复验收不能只检查数据库能否打开，还需要验证指定交易日可以从 raw archive 幂等重放。

恢复演练永远创建一次性数据库，不直接覆盖 `relay_trader` 或 `relay_trader_test`。真实 DSN 只从未跟踪配置或 `RELAY_DATABASE_URL/RELAY_DATABASE_ADMIN_URL` 读取，脚本和报告不打印凭据。

## 创建备份

生产备份：

```bash
scripts/backup-postgres.sh config/relay.prod.yaml outputs/backups
```

测试备份：

```bash
scripts/backup-postgres.sh config/relay.local.yaml outputs/backups
```

脚本执行以下检查：

1. 校验 DSN 实际数据库名等于 `database.expected_name`。
2. 使用 PostgreSQL custom format、zstd level 6、`--no-owner --no-acl` 生成一致性归档。
3. 生成 `*.dump.sha256`，恢复前必须通过 SHA256 校验和 archive catalog 读取检查。
4. 生成 `*.manifest.json`，记录服务端版本、migration 版本、数据库大小、raw 日期范围和关键表行数。
5. 备份目录权限为 `0700`，文件受当前用户 umask `0077` 保护。

建议在交易日 OC 关停和账本同步稳定后执行，例如 `18:00 Asia/Shanghai`。当前脚本不自动删除历史备份，也不上传外部存储；本机备份不能替代异机或对象存储副本。

## 恢复演练

指定归档和交易日：

```bash
scripts/restore-postgres-drill.sh \
  outputs/backups/relay_trader_YYYYMMDDTHHMMSS+0800.dump \
  2026-07-31 \
  config/relay.prod.yaml
```

省略交易日时，默认使用 manifest 中 raw archive 的最后日期。脚本会：

1. 校验 SHA256 和 custom archive catalog。
2. 创建唯一 `relay_restore_*` 临时数据库，并注册 trap 保证退出时销毁。
3. 恢复全部 schema 和数据，核对 migration、约束验证状态和关键表行数是否与 manifest 完全一致。
4. 在临时库执行指定交易日的 `relayctl ledger-replay`，覆盖 orders、fills 和 transfers 三阶段。
5. 要求重放前后订单、订单事件、成交、ETF 划转和 raw 行数完全一致。
6. 要求孤立订单事件、孤立成交和重复订单幂等键均为 0。
7. 将结果写入本机 `outputs/restore-drills/restore_*.json`，报告和归档目录不提交 Git。

容器当前提供 PostgreSQL 17 客户端，而数据库服务端是 PostgreSQL 15。脚本会自动识别该组合，使用 `pg_restore --file=- | psql` 兼容路径，并仅过滤 PostgreSQL 15 不支持的 `SET transaction_timeout = 0`；管道启用 `pipefail` 且两端错误即停。

## 2026-07-31 演练记录

生产库全量演练已完成：

| 项目 | 结果 |
| --- | --- |
| 源数据库大小 | `2,182,364,519` bytes |
| 备份归档大小 | `58,135,716` bytes |
| migration | `19` |
| 恢复目标 | 一次性 `relay_restore_*`，完成后已删除 |
| 回放交易日 | `2026-07-31` |
| 当日 raw archive | `39,108` |
| 订单 | `6,911 -> 6,911` |
| 订单事件 | `22,735 -> 22,735` |
| 成交 | `8,318 -> 8,318` |
| ETF 划转 | `297 -> 297` |
| 孤立订单事件/成交 | `0 / 0` |
| 重复订单幂等键 | `0` |
| 未验证约束 | `0` |
| 重放 parser/ledger errors | `0 / 0` |

订单阶段存在 1 条历史空 `message_type` raw 记录被按 unsupported 跳过；它不属于可重建订单/成交，stage `error_count=0`，重放前后账表数量一致。

## 正式灾备注意事项

1. 不要把 `.dump`、manifest、SHA256 或恢复报告提交 Git。
2. 不要直接对生产数据库执行 `pg_restore --clean`。
3. 正式事故恢复时先恢复到新数据库，完成本手册全部验证，再通过服务端配置切换 DSN。
4. 切换前更新 `database.expected_name`，保持生产 `trading_enabled=false` 完成只读验收。
5. 本机至少保留最近若干交易日备份；异机副本、加密、保留周期和恢复时间告警仍属于 P10 发布运维增强项。
