# Relay 常驻进程

更新时间：`2026-08-02`

## 生产拓扑

Relay 生产运行拆为两个独立 Go 进程：

| 进程 | 外部监听 | 职责 | 日志 |
| --- | --- | --- | --- |
| `relay-api` | `0.0.0.0:9092` | 文档门户、交易终端、HTTP API、Meridian 薄代理和 SSE | `/var/log/relay/relay-api.log` |
| `relay-worker` | 仅 `127.0.0.1:19092` 健康端口 | Redis output stream 消费、PostgreSQL 落账、checkpoint、事件驱动自动刷新 | `/var/log/relay/relay-worker.log` |

生产配置使用：

```yaml
worker:
  embedded_ledger_sync: false
  health_addr: "127.0.0.1:19092"
  health_url: "http://127.0.0.1:19092/readyz"
```

兼容配置不声明 `embedded_ledger_sync` 时仍默认由 docs/API 进程运行内嵌同步，便于开发和单进程测试。生产必须显式设为 `false`，避免 API 和 worker 同时消费并推进同一组 checkpoint。

## 事件桥

进程拆分后，worker 在账本批次成功合并后通过 PostgreSQL channel `relay_ledger_events_v1` 发送 `relay.ledger_event.v1` 轻量通知。API 使用独立连接执行 `LISTEN`，收到通知后继续向原有 `/v1/events/stream` 发布：

- `order.changed`
- `order.cancel.rejected`
- `fill.changed`
- `asset.changed`
- `positions.changed`

SDK 的订单状态、成交和撤单拒绝回调协议不变。通知只携带账户、stream 位点、变更计数和必要的撤单拒绝字段，不包含原始报文；PostgreSQL 账表仍是权威数据源。通知是实时唤醒信号，不承担账表持久化或断线重放。解码端接受旧的无 envelope 事件以支持 API/worker 独立滚动发布，未知 schema 会拒绝并记录日志。

`GET /v1/status` 增加两个依赖：

- `worker`：9092 对本机 worker `/readyz` 的短超时探测。
- `event_bridge`：API 的 PostgreSQL LISTEN 连接是否 ready。

## 服务命令

统一入口：

```bash
scripts/relay-runtime-service.sh status
scripts/relay-runtime-service.sh start
scripts/relay-runtime-service.sh stop
scripts/relay-runtime-service.sh restart
```

独立操作：

```bash
scripts/relay-docs-service.sh restart
scripts/relay-worker-service.sh restart
scripts/relay-runtime-service.sh logs-api
scripts/relay-runtime-service.sh logs-worker
```

运行二进制、PID 和上一版本位于未跟踪目录 `.runtime/`。每次成功构建新二进制时，现有版本保存为对应的 `.previous`。独立回滚命令：

```bash
scripts/relay-runtime-service.sh rollback-api
scripts/relay-runtime-service.sh rollback-worker
```

回滚只交换目标进程二进制，不停止另一个进程。配置 schema 或 Redis wire schema 不兼容的发布仍须按发布记录同时回滚配套配置或 OC，不能只做二进制交换。

## 容器自启动

安装统一 watchdog：

```bash
scripts/relay-runtime-service.sh install-cron
```

该命令删除旧 `RELAY_DOCS_AUTOSTART` 块并安装 `RELAY_RUNTIME_AUTOSTART`：容器启动时及每分钟分别检查两个进程。生产配置出现任何 `trading_enabled: true` 时，自动启动默认拒绝，除非显式设置受控放行变量。

## 环境切换

本机环境切换继续使用：

```bash
scripts/switch-relay-env.sh test
scripts/switch-relay-env.sh production
```

脚本先停止当前运行时，再把所选未跟踪配置记录为 `.runtime/active-config.yaml`，并通过统一入口启动。cron watchdog 会沿用该选择，不会在下一分钟悄悄切回另一环境。配置为 `worker.embedded_ledger_sync=true` 时只启动带内嵌同步的 API；配置为 `false` 时启动独立 API 和 worker，避免双消费者。

## 发布验收

1. `scripts/relay-runtime-service.sh status` 的两个进程都必须 healthy。
2. `/v1/status.dependencies.worker=ok` 且 `event_bridge=ok`。
3. `/v1/operations/status` 的 24 条 output stream lag 符合当前时段，不能因拆分出现双消费者或位点倒退。
4. 执行 `RELAY_EVENT_BRIDGE_TEST_CONFIG=config/relay.prod.yaml go test ./tests/integration -run TestLivePostgresEventBridge -v`，只发送专用虚拟账户的临时通知，不写交易账表。
5. 再运行 `scripts/check-readonly-release.py`；生产 `trading_enabled` 必须保持 0。
