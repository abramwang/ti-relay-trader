# 绩效修复代码与连续预览

日期：2026-10-27，Asia/Shanghai。承接本日资金证据验收，先修逻辑并只读试算，不发布新的金融事实。

## 已修正

### T0候选隔离

自动推断的买入候选必须有有效 Meridian PCF 最小单位，且原始 OrderQty 为该单位的正整数倍。不能用成交量代替委托量判断，也不硬编码etfarb标签或证券代码。显式 t0_order_group_id 仍允许拆单。部分成交不足、缺PCF、多个合法买单形成的歧义继续阻断。

387/388的10月21日53,700与53,800份底仓不再污染百万份T0候选。新增8组回归验证成本状态和成交消费集合，未移除歧义门禁。

### 逆回购现金时刻

根因不是无法得到时间，而是读取 broker_close 时把 captured_at 当成 Asset.updated_at，覆盖了原始资金回包时间：

| 账户 / 日期 | 原始资金时间 | 逆回购成交 | 整批完成时间 |
| --- | --- | --- | --- |
| 388 / 10-21 | 15:01:01.587 | 15:01:16 | 15:01:20.148929 |
| 768 / 10-26 | 15:01:01.668 | 15:01:12 | 15:01:15.241672 |

资金实际早于逆回购，但旧代码比较整批完成时间，把本金重复加入。`GetAssetSnapshot` 和 `GetLatestAsset` 对 broker_close 现在取 raw_payload.asset.updated_at（兼容 broker_asset.updated_at），不更改原始 captured_at 列。其他快照类型继续保持原口径。缺源时间不再猜成采集完成时间，收盘派生写入会明确失败；原始 broker_close 保留。

派生快照的 reverse_repo 审计增加 cash_observed_at，沿用原有“仅计入不晚于资金回包时刻的成交本金”算法，无金额补丁，无券商枚举扩展。该修正不能外推为柜台现金语义或全部私有清算范围已证实；同一时刻仍有不确定性时应继续复核。

已有错误close行不会因代码发布自动变更，必须另行备份后按原始输入重建。金融历史尚未改写。

## 连续只读模式

`relayctl performance-rebuild -preview-chain`：

- PostgreSQL repeatable-read、read-only事务，不执行落库。
- 仅将同时可用的成本和NAV结果保留在内存，供次日计算使用；按账户隔离。
- 某日失败或阻断后，该账户余下日期停止预览，不能借旧版本跳过失败日。
- `-preview-chain` 与 `-persist` 在连接数据库前互斥拒绝。
- 报告明确 persist=false、preview_chain=true，单项结果 persisted=false。

示例：

```bash
go run ./cmd/relayctl performance-rebuild \
  -config .runtime/active-config.yaml \
  -accounts 307000051387,307000051388 \
  -date-from 20261021 -date-to 20261027 -preview-chain -timeout 10m
```

真实结果：两户10月21/22/23/26/27共10个账户日全部可连续试算。21日成本estimated，后四日calculated；NAV均provisional，未再出现 ambiguous_t0_order_group、previous_cost_state_gap、previous_economic_nav_gap。该轮仍沿用既有资金账与T0暂估，未注入新券商清算事实，不能直接当最终修复计划发布。

三份新资金表10月20..26日各5行的既有只读资金审计也通过，零恒等式差异。没有调用旧资金审计工具的persist，因为其默认零待结权益/其他资产归回购并不适合本轮全部ETF样本。

## 验证与产物

- `go test ./...` 通过。
- `go test -race ./internal/performance ./internal/api ./internal/ledger ./cmd/relayctl` 通过。
- `TestBrokerCashTimestampReadOnly` 使用只读事务验证真实两户原始时刻，并用CTE覆盖新/旧raw结构、缺时刻、derived close不变及两条资产查询SQL。
- API回归覆盖现金早于/晚于逆回购、缺时刻禁止写快照。

私有产物目录：`outputs/recovery/closure_preview_20261027/`。

| 文件 | SHA256 |
| --- | --- |
| t0_chain.json | fba195369cf7307b21b14de266629ad0c8c734129485525b685dba176a7650bc |
| funds_307000051387.json | e027831e2d529f0d42e12748529d00ff30d3ae15b59f1d156503796b0cc9c5eb |
| funds_307000051388.json | 3fe62f3f532c236523e40d5ed06bf9cb951bd82d41ab9727d926071e784a7d69 |
| funds_314000045768.json | d1237ad736225c071eefb12221bebba6d55416d3587f775670b9f76111328b18 |

## 下一步

将实际现金差额、费用和源日权益承接加入独立修复预览，消除和旧资金记录的重复；以新券商文件对照源日收益与记账日收益差异。完成新备份后再版本化修复close/reconcile/成本/NAV。768仍保留旧恢复写保护、待处理私有权益和未归属场外交收，不扩为常规人工导入。生产环境、零下单权限及独立worker不变。
