# CTP 成交身份与启动回放修正

日期：2026-11-17，Asia/Shanghai。Relay给OC的具体修正提案；不是新登录或交易窗口授权。

## 晚间更新：只读交付已接，转次日执行

OC `0cd38b6`新包7项摘要、21个离线场景的58心跳/13回复及9反例已核验；Relay已实现严格`readonly_query_evidence.v1`、源范围/原生身份/计数门禁、会话来源固定和诊断。非空订单/v2成交通过临时PG关联及重放验证，23组本机Redis/PG演练通过；不是实际迁移或报撤验收，旧50/18/32原状态保留。

**现在只需转给OC [11月18日唯一执行清单](OC_CTP_JOINT_EXECUTION_20261118.md)**：下一交付为最终assessment及跨日/状态承接回归，不再等待只读实现或重复原则回签。以下准备记录保留历史背景；新窗口、结算和报撤均未授权执行，当前TEST环境不变。

## 盘外准备收口：统一演练与查询审计工具

按用户“尽量今天完成不依赖柜台回报的基础工作”要求，新增下列可执行交付，不继续做重复原则回签：

- `scripts/test-ctp-offline.sh`：清除继承的共享Redis/PG测试目标，启动私有Unix socket Redis和临时PG，运行Python契约、Go测试/vet、23组链路演练及4项存储故障回归，构建inspect/capture/query/replies/trade五个工具。日志、退出阶段、二进制摘要保留到新的受限`outputs/recovery/ctp-offline-*`目录。只构建，不替换9092服务，不启动OC。
- `internal/futures/local_rehearsal_test.go`：复用真实Relay发送/接收/PG代码，覆盖四查询完整/失败/部分页、原文分页、位点与owner恢复、丢历史拒绝、发布未知不重发、独立撤单门禁、成交先到、v1/v2防重及CLI。输入为合成fixture，绝不投向共享Redis；不是OC SDK或真实报撤验收。
- `scripts/build-ctp-query-review.py`：从独立OC原始审计完整前缀、Relay原始命令及已核对target生成review草稿；核对原文件SHA、逐条链、原生命名范围、会话/代次/交易日、原始接收命令字节、ticket和SDK请求身份。只输出新目录，不覆盖旧文件，不联网、不登记、不发命令。
- 草稿必须同时有原命令接收及唯一`query_result`；单独`before_sdk_call`不够，reply也不能反过来作为请求证据。若有`query_request`，其身份及顺序必须匹配。失败/部分/缺失ErrorID保持原值，审计链仅证明材料内部一致，不等于券商签名或最终业务成功。
- 当天四查询359行原始链已离线复验，4份草稿均通过Go接收器`check_only_no_connections`。证据为`outputs/recovery/ctp_dongwu_joint_20261117/offline-builder-{0,1,2,3}/`；历史资金仍为`missing_biz_type`。未重新注册过期查询，不写真实联调PG，不恢复Redis旧键。

17:55验证收口：12项工具单测、111项Python契约、全Go/vet、23组私有Redis/PG演练及4项存储故障回归通过；另补跑整个futures包的临时PG race回归，耗时378.658秒，含1万订单+1万成交的分页、入账、关联、重启重放不重复和降级保护。全PG阶段未给共享Redis目标的专项按设计跳过，Redis覆盖以单独23组隔离演练为准，不说所有可选在线用例已通过。证据目录`outputs/recovery/ctp-offline-20261117T174715.4NPSjF/`（`postgres-full.log`为随后独立补跑，已并入统一脚本的固定阶段）；临时Redis/PG均退出清理。大容量耗时含race及合成证据验证，不作为生产吞吐承诺。

### 一次运行

```bash
env LD_LIBRARY_PATH="$PWD/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu" \
  RELAY_TEST_PG_BIN="$PWD/.runtime/ctp-pg-toolchain/root/usr/lib/postgresql/17/bin" \
  RELAY_FUTURES_LOCAL_REDIS_SERVER="$PWD/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server" \
  RELAY_CTP_CONTRACT_PYTHON="$PWD/.runtime/ctp-contract-venv/bin/python" \
  bash scripts/test-ctp-offline.sh
```

不安装系统服务、不修改Redis配置、不依赖共享实例空闲；缺工具/依赖直接失败，不自动下载或切环境。本地Redis8.0.2与共享8.2.2仍有版本差异，不扩大部署可靠性结论。

查询审计草稿工具参数（路径/身份必须来自本次真实发布记录，不复用历史示例）：

```text
python3 scripts/build-ctp-query-review.py
  --audit-file <OC原始审计完整前缀> --audit-sha256 <已核对文件摘要>
  --command-file <Relay已登记并发布的原始命令> --command-id <PG原命令ID>
  --target-file <独立核对的本次target>
  --broker <route broker> --gateway <gateway> --account <logical account>
  --native-broker <BrokerID> --user <UserID> --investor <InvestorID>
  --batch-id <新batch ID> --review-id <新review ID> --timeout-ms <1..300000>
  --output-dir <尚不存在的受限目录>
```

输出`review.json/evidence.jsonl/report.json`。evidence逐行保留原字节，完整链SHA/头摘要/选中sequence在report中；选中证据超过接收器256KiB上限则失败，不截断。先用`relay-ctp-replies`无capture/receive参数的默认离线模式核验，然后才按执行单显式登记；接收器仍检查PG原发布target/原命令身份和**原发送时刻的截止时间**，生成草稿不能延长过期请求。未拿到独立审计时先保存raw，不能从reply构造可信请求。

### 下一窗口前完成，不占交易时间

| 责任方 | 开窗前必须完成 | 当前状态 |
| --- | --- | --- |
| Relay | 23组隔离演练、4项存储故障、五工具构建、原始审计自动生成/接收契约 | 工具与首轮演练完成，日志按运行目录留存 |
| OC | 独立只读原生通知持久化、完整查询外部证明、旧50条source-manifest/legacy-links、恢复门禁/心跳及准备工具 | 晚间0cd38b6已交付并完成Relay离线复核；实际准备/初始化/迁移未执行 |
| OC交付 | 同一版本实际编码的只读心跳、非空订单/成交分页、失败/空/部分与损坏/重启测试结果及SHA | 新包已验，下一步仅交最终assessment/跨日状态承接及实际JSON，见顶部次日执行单 |
| 双方 | 核对旧责任不变、新候选无占用、run_id/epoch/组和独立PG绑定；落盘所有离线命令模板，但不预发占位查询 | 必须在实际初始化前重新核对，不把历史空路由结果当预留 |
| Relay/OC | 报撤模式必须基于修复后的主程序完成离线回归；只读通过不自动获得交易或结算权限 | 新窗口和单次结算授权另行明确，13:35旧授权不复用 |

交易时段只留：真实认证登录及会话确认、获授权的结算确认、当前订单/持仓完整查询、现场合约/价格约束验证、一手有效报单及该单真实撤成、原文与落库对账。价格、TradingDay、会话和交易权限不能提前伪造；未知、意外成交或绑定丢失仍停止。资金BizType缺失保留独立问题，不补字段，也不将有限测评准入说成资金范围已核验。

服务保持TEST；生产权威复算继续等待用户明确切回。离线准备不切环境、不变更cron，也不抹去旧18 audit_only/32 pending。

## 17:27 Relay独立Redis故障回归完成

OC新只读实现/样例尚未收到，不猜字段推进运行。Relay先完成不依赖它的存储故障回归，代码为`internal/futures/redis_storage_local_test.go`，四项测试带race重复三轮共12次通过；默认全Go测试及vet通过。**只新增测试，不改变发布语义，不重启API/OC，不初始化候选路由。**

| 用例 | 本机独立结果 |
| --- | --- |
| noeviction容量拒写 | 临时实例降低maxmemory至已用内存以下，SET/XADD/EVAL真实返回OOM；原string原字节/无TTL保持，evicted=0，命令流未增加 |
| 查询发布与PG责任 | 先持久attempted，再在真实EVAL处遇OOM；Relay返回unknown、不伪称未发出或完成。恢复容量并重新构造CommandStore后仍返回原attempted，不再次append；使用独立可丢弃PG随机schema |
| 报单发布 | 通过合成TEST授权/新鲜心跳及原生约束检查后，真实trade publisher在OOM处返回unknown，cmd.trade无新增；无SDK或柜台参与 |
| AOF受控重启 | appendonly=yes/everysec，WAITAOF证明本机刷盘后SIGTERM并重新启动；Stream原文/entry、epoch、示意cursor、消费组/PEL及consumer/待确认entry保持。确认publisher连到新进程后，旧run_id被inspect和原子发布同时拒绝，不补发 |

隔离方式：仅显式`RELAY_FUTURES_LOCAL_REDIS_SERVER`指定本机可执行文件，测试创建0700临时目录、Unix socket、`port=0`，不接受外部Redis URL、不读取应用配置。退出停止所有测试Redis并清理临时目录。Redis包及依赖只解包到`.runtime/redis-isolated-tools`，未安装/启动系统Redis服务。测试Redis为Debian包8.0.2，共享实例为8.2.2；**本轮不冒称相同版本或共享部署认证**。

容量故障采用受控降低内存限额，不是高负载填满主机；AOF测试是先刷盘后的正常进程重启，不是断电/磁盘损坏/未刷盘崩溃证明；示意cursor是隔离Redis键，不替代Relay PG receiver恢复验收。共享TEST实例的启动配置、持久挂载、真实8.2.2重启仍待另约窗口；原16:23至16:28五分钟两键留存观察结论不扩大。旧路由没有恢复或重建。

本机复验（显式隔离工具和可丢弃数据库，不使用生产/真实联调库）：

```bash
env LD_LIBRARY_PATH="$PWD/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu" \
  RELAY_FUTURES_LOCAL_REDIS_SERVER="$PWD/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server" \
  RELAY_FUTURES_TEST_DATABASE_URL="host=$PWD/.runtime/ctp-dongwu-pg-20261117 port=15434 user=relay_ctp_audit dbname=relay_ctp_terminal_test_20261117 sslmode=disable" \
  go test -race ./internal/futures -run '^TestLocalRedis' -count=3 -v
```

环境决定：17:20生产权威复算脚本访问当前TEST 9092，返回`no enabled performance accounts selected`，Meridian权威水位已ready；不是今日15:01收盘采集/结算失败。用户随后明确“保持测试，稍后切换”，因此不切生产、不补跑、不修改cron；此任务继续列为未完成，不误标为CTP或行情故障。

## 16:37候选已收到：直接开发，只读入口尚不可启动

已收到用户转交的《OC 新只读绑定候选及旧回调关联方案》。OC引用的本文上一版SHA256 `ffde16eec39568398c09e8cdd0907fd993bec503844018b857f44e831a4f7fef`与Relay本机一致。**接受候选命名及只追加关联、旧证据不变的设计，OC可直接按以下顺序完成实现，无需再等原则回签。** 当前只是文字候选，未核验空路由、预留、创建配置/状态或授予初始化、登录权限；OC仍报告业务基线5ec0e31，不能当新功能已交付。

### 唯一候选与边界

| 项目 | 候选 |
| --- | --- |
| env / broker / account | `test / ctp_4900 / 300052141` |
| native BrokerID / UserID / InvestorID | `4900 / 300052141 / 300052141` |
| gateway | `ctp-dongwu-readonly-recovery-20261117` |
| group / consumer | `oc-ctp-dongwu-readonly-recovery-20261117` / `oc-ctp-dongwu-readonly-recovery-20261117-owner1` |
| epoch | `dongwu-readonly-0d304a7a29800b24d832803bed3007ab`，未写入 |
| Redis / source | 既有TEST DB0 / `shared-test-redis`，启动前重查实际run_id，不改部署身份 |
| 模式 | `test_broker_read_only`；production_mode=false、settlement_confirm=never、trading_enabled=false |
| 窗口 | 待另行确认，最多300秒、心跳1秒；目前无T0/session/代次/TradingDay |

前缀`relay:test:v1:ctp_4900:ctp-dongwu-readonly-recovery-20261117`，六流及epoch后缀沿既有协议；交易组只用于责任检查，不消费新交易。候选名日期不约束未来柜台TradingDay。

用户随后已粘贴`oc.ctp.readonly-binding.candidate.v1`候选内容：scope、六条流、epoch键、group/consumer和只读限制与本节一致；journal/identity/audit三个完整SHA256已重新与Relay保存的原包文件比对，一致。旧18+32=50、pending订单22+成交10=32，未处理/未修改/未向新路由重放标志一致。材料是经过消息转义的粘贴文本，**仅确认内容及所引用源文件摘要，不宣称取得OC候选文件原字节或其文件SHA**；OC本机路径不视为Relay已创建的目录。

`candidate_not_initialized`、`runtime_ready_for_legacy_reconciliation=false`、`not_a_runtime_config_or_stream_message=true`及五项`unimplemented`保持原含义。候选声明的run_id仍为16:04历史观察，当前无占用检查、T0/session/代次/TradingDay，不生成发送目标、不投递候选JSON。候选内容无需修改或再次回签，按下列三块完成五项缺失实现，交付实际只读心跳/查询样例后继续接线。未来source-manifest仍须包含原scope/session/epoch等完整绑定，不能仅凭这份候选摘要代替已实现的关联存储。

### OC先交三块实现，不扩大任务

1. **来源留档与追加关联。** 实现source-manifest、legacy-links及重启复核；引用原scope/session/epoch与三个文件完整摘要，按旧journal摘要/callback ID/evidence摘要唯一定位。50条原文、18 audit_only/32 pending及原outcome不改。缺源、摘要变化、未知执行/ACK或经济冲突停止；同scope升级器不能当跨gateway迁移器。
2. **真正的只读运行接线。** 原生通知先持久化，再核对/关联；不得关闭通知来制造pending=0。接当前完整订单查询来源证明及按请求身份的结果校验，不把非空未知订单当空页成功。仅缺查询证明的可关联项允许必要只读查询，其他恢复/审计阻断保留；不确认结算、不报撤、不因关联成功发外部order.event/fill.event。**不要求先核销全部历史pending才启动只读核对，更不要求本轮实现通用外部事件释放。**
3. **可验证输出与准备工具。** 补只读心跳中的旧/新责任计数、核对范围和实际查询准入语义；精确字段名、位置、类型、完整性规则随实际编码器JSON一次交付。候选gateway进入显式准备工具允许列表，保持空路由/来源检查。附增量测试日志/SHA，不要源码、二进制、凭据或整包重复传输。

回归重点：原18/32不变；重复/重排不重复关联；同完整键经济冲突拒绝；缺源/损坏/ACK未知阻断；不完整查询不证明来源；跨gateway保留两端ID及原生元组；跨日当前空查询不核销旧日责任；重启保留关联与未决状态。只读可查询时结算及报撤仍关闭。当前入口缺失这些实现，不安排试启动探测。

### Relay并行职责及一处口径更正

- 本轮已核对代码：`test_broker_read_only`目前验证`query_collection_ready`与`transport_recovery_clear`及登录/代次/交易日，并强制结算、订单交易快照ready及报撤false；**没有pending计数字段契约**。下方早前“旧模式仍要求pending=0”只适用于不带延期策略的assessment，不能泛指只读模式。未知adapter审计扩展可被保存，不等于已理解新准入语义。
- 不把`local_assessment_only.v1`搬到只读模式，不提前猜字段或放宽恢复门禁。收到实际只读心跳及正反例后，同轮实现独立解析、责任可见性与查询门禁回归；新路径完成前不建立发送目标或向候选路由发布查询。
- 开窗前重新核对Redis来源、完整空路由和PG旧范围零交易事实/新范围零登记；上轮零事实结论不可替代开窗前核验。若已有入账事实则停止做显式范围消歧，不因gateway变更把同一原生事实记成第二笔成交，不自动建立生产范围归并规则。
- 旧gateway的派生ID和新查询ID各自保留，关联靠完整原生事实及证据；不改原gateway，不搬cursor/ACK，不导入旧消息冒充新收到。跨日查询无历史能力时，旧pending可继续明确留档，不能补日期、补终态或用空页证明消失。

当前不改代码契约、配置、环境或数据库，不执行Redis写入/初始化/登录。资金BizType=00继续失败、外部事件释放单列；只读恢复闭合后才另约日间单笔报撤窗口，原13:35授权不延长。此前“等待候选”由本节更新为“候选已定、等待上述实现及实际JSON”。生产17:10任务仍须用户确认切回生产，不能为了联调自动切换。

## 收盘后增量已核验：可推进新绑定准备

用户已上传`reference/oc_ctp_postclose_review_20261117/`。本节替代下方16:10“尚待材料”的状态，不改变历史失败记录。**接受本轮离线交付，无需再讨论成交编码或重传材料；下一步是具体的新只读绑定/状态关联方案。** 不是已经完成真实报撤、实际旧状态迁移或持久性验收。

### 独立核验与Relay改动

- 四项清单摘要通过；v4 JSON SHA256为`26a57a0b701798a6ba3503b5b472888815aa4993939a017d219cbcb5932be665`。原50条回调、24条身份观察、8条审计链重新核验通过；新迁移摘要的50行、10个v2候选及4组旧键拆分与前次材料完全一致。
- 32项延后记录逐条对应原pending callback ID及原证据摘要，`business_processed=false`；18条audit_only不变。正常场景64 pending由原32与模拟再次回放组成，不是64笔成交；未获得完整运行后磁盘状态，不宣称独立执行了OC迁移。
- Relay实际解码通过17条心跳、两页完整查询（12行订单）及2条同一单的working/cancelled事件。另核对2个accepted回执结构/关联及调用计数；所有新查询、结算、报撤、Redis/ACK均为模拟，原log仅为17条心跳摘要，不冒称真实Stream原文。ACK未知场景无业务输出、SDK调用0、recovery=false；没有独立完整故障轨迹，在线ACK恢复仍待验。
- 修复旧Relay将所有pending都视为阻断所造成的初始化死锁：仅`test_broker_assessment`且显式`callback_policy=local_assessment_only.v1`允许延期子集。四个新字段必须同时存在；非负整数、`deferred+blocking=pending`、`callback_reconciliation_complete == (pending==0)`严格校验。缺字段、未知策略、矛盾计数、任何blocking均不放行。
- `transport_recovery_clear`继续是独立必要条件，不能由blocking=0替代；只读初始化与报撤准入仍分开。不带延期策略的assessment仍要求pending=0，生产不接受测评模式；独立只读模式的限制见顶部更正。结算、订单/持仓快照、源/epoch/session、短时授权、行情/原生约束及一手报撤预算不变；历史时间不刷新、不把材料发送到真实路由。
- 心跳诊断输出显式显示pending/deferred/blocking及reconciliation_complete，局部测评ready不能隐藏历史责任。新版本inspect/query/replies/trade CLI已构建；未启动业务接收/发送进程，未重启9092或OC，不改当前TEST环境。

复验入口（只读文件，不登录、不连接Redis/PG）：

```bash
python3 scripts/audit-ctp-postclose-review.py reference/oc_ctp_postclose_review_20261117 outputs/recovery/ctp_fill_v2_review_20261117
RELAY_OC_CTP_POSTCLOSE_REVIEW_DIR="$PWD/reference/oc_ctp_postclose_review_20261117" go test ./internal/futures -run 'Test(OCPostclose|Assessment)' -count=1
```

代码验证：全Go测试、定向race与vet、111项Python契约测试通过；新回归覆盖部分字段、错误计数/类型、ACK未知、过期心跳、生产拒绝和延期责任可见。Python首次误用普通`.venv`缺jsonschema，改用既有`.runtime/ctp-contract-venv`后全部通过，未改服务依赖。需PG/真实Redis的集成用例本轮未启用，不能将全包通过解释为在线验收。OC42项CTest仅按交付日志记录，未运行OC二进制。

### 请OC直接准备下一步

请给**新只读gateway、group、epoch候选及对应状态关联方式**，明确旧18 audit_only/32 pending继续保留在哪里，如何在新会话中重新核对；不搬旧cursor、不改旧回调gateway或把旧输出补回新流。Relay核对空路由/来源及独立PG起点后，再确认允许登录的窗口。只读窗口结算/报撤保持关闭；若现有主程序尚不能独立运行该只读路径，直接注明实际缺项，不用新增原则说明。

下一交易时段再确认测评窗口及单次结算授权，用新session取得初始化快照后执行一手报单/对应撤单。原13:35授权不复用，今日无线上报撤成功结论。资金BizType及通用外部回放释放仍独立未验，不以局部测评通过替代。

Redis在线配置改善已做首轮观察：随机独立开发前缀下1个string及1个stream，31次检查跨越300秒，值/entry/body/PTTL=-1均不变，run_id一致，evicted_keys始终12326，AOF写状态ok；结束仅清理本次两个键，清理成功。证据`outputs/recovery/ctp_redis_retention_20261117/5519565e16cbcec1e6bddbc615b06d0d.json`。这不是压力测试，不恢复旧键；16:28全量SCAN旧下午前缀仍0键，未重建。未在共享Redis造OOM、重启或修改配置。容量耗尽时拒写、发布未知、启动配置及重启持久性仍是后续专项。生产17:10权威复算仍需用户确认切回，不自动切换。

## 16:10收盘后回执：接增量材料，不重做原则审阅

已收到用户转交的OC16:04状态回执。OC报告业务基线`5ec0e31`、文档HEAD `33fa6e6`，已实现v2查询/事件、显式测评回放隔离、无本地命令旧身份的归档升级，以及journal/identity文件或锁缺失时持续拒绝；42项CTest及真实50回调的离线运行回归通过。**以上新实现目前为OC报告，Relay尚未收到v4材料，不将其记作独立验收或真实状态迁移完成。** 旧报告的24条观察/revision25计数已获OC更正。

### Relay本轮独立核对

16:09:28只读查询TEST Redis，结果与OC一致：Redis8.2.2/master/DB0，maxmemory=2147483648、noeviction，appendonly=yes、appendfsync=everysec；aof_last_write_status及aof_last_bgrewrite_status均ok。used_memory=415583912，evicted_keys=12326、expired_keys=307。run_id仍`94f801dc68e3699cc681feb6dbb48f19e46aef9e`且SCAN前后一致；下午前缀0键，不能把旧期望epoch当当前值，**组缺失时pending/lag不可读取，不是0**。

证据：`outputs/recovery/ctp_dongwu_assessment_pm_20261117/preflight-20261117T080928.414206400Z.json`。仅白名单CONFIG GET/INFO/SCAN及读取，没有CONFIG SET、写键、清理、初始化或负载压测。当前在线参数不证明启动配置已保存、挂载持久可靠、负载/重启恢复通过；noeviction也不代表内存不足时写入会成功，后续仍保留原发布失败/未知结果保护。配置改善不恢复历史丢键，不据累计计数直接归责某次丢失。

独立PG仍5笔历史查询、0交易发布、0业务观察/执行事实/事件；本机旧原包重新核验50回调、24观察、8条审计链通过，三个源文件SHA与OC新回执一致。原审计SHA为`da68ea85c741a8fd27f1766cdb5bf08fc52d6c42207c2615281e3897a9812d4f`。这证明本机已收材料的一致性，不等于访问了OC当前磁盘。OC报告当前无进程/新session、原状态未迁移、once不存在；旧13:35配置不可启动，未用结算授权也不得自动复用。

### 下一步最小交付与执行顺序

1. **只传增量**：`recorded-flow-20261117-v4.json`、对应`.log`、`postclose-ctest-20261117.txt`及SHA256清单；附新模式心跳/延期回放字段说明（已含JSON则不另写）。此前原50条包不用重复，不交换源码、二进制或私有配置。
2. Relay直接核对实际输出编码及原文、v2身份、模拟本地报撤的origin/终态，以及ACK未知/锁或文件缺失仍拒绝。重点核对旧18 audit_only/32 pending如何保留或延期，不把延期当delivered、不能伪造本地origin或隐去经济/执行未知；合成报撤不记为真实柜台成功。局部TEST测评隔离若验证通过，可单独收口，不要求先完成所有外部订单生产化；但不能升级为通用外部回放处理已经完成。
3. 材料通过后，按已知零交易责任和保留的旧回调明确**新的只读绑定或显式迁移方案**。OC给实际gateway/group/epoch/状态关联方案，Relay核对占用及独立接收起点；旧命令不重放、旧cursor不搬迁，不能改写原回调gateway或将其补发到新流冒充完整恢复。当前不分配或初始化新路由。
4. 另行明确允许登录的只读窗口，确认实际版本、新session/代次/TradingDay后再采集；结算确认及报撤关闭。后续交易测评仅在券商允许的日间时段另开明确窗口，沿用一手报单/对应撤单目标但不复用过期授权。本轮未发查询、未启停OC、未修改环境或生产权限。

当前环境仍为用户指定TEST；17:10生产权威复算仍依赖生产9092，须按用户指令切回，不自动切换。

## 最新回执：材料已核验，Relay接收实现完成

收到OC `ef0e1e5 / b75ccf2`反馈及用户上传的`reference/fill-v2-review-20261117.tar.gz`。九段编码已共同确认，不再等待原则回签。**本节取代此前“仅候选、未收到原包、等待OC确认”的状态；不表示OC运行版本已可启动，也没有新交易窗口。**

### 独立核验

受限解包位置：`outputs/recovery/ctp_fill_v2_review_20261117/`，原件保持不变，不加入Git或公开文档附件。外层9项、原始退出包7项SHA均通过；journal/identity封装摘要、50条回调摘要及选定原始字段、24条身份观察摘要、8条审计链连续性均复核通过。

| 检查 | Relay独立结果 |
| --- | --- |
| 原始回调 | 50条，18 audit_only、32 pending，未改状态 |
| 可派生订单 | 22条回调、12个原生委托键；来源待证明，不当作本轮订单 |
| 成交 | 10条原始成交、10个不同v2身份；逐条重算九段字节及SHA，核对原生ID、方向、开平、价格、数量和源成交时间 |
| 旧键碰撞 | 6个v1键，其中4个一对多；没有最后一条覆盖、合并买卖两侧或伪造origin |
| 外部关联 | 10条成交均在本包找到匹配原生委托及合约/方向/开平/数量关系；这不是来源证明或发布许可 |
| 观察记录 | 实际24条、identity revision=25。请OC将文字中的“25条观察”更正为24，其他结论不变 |
| 本机真实联调库 | 只读复核仍5条旧查询、0交易发布、0业务观察/成交事实/事件；不是OC本机零命令的转述 |
| 结算边界 | 提供的state副本无once，审计链无settlement记录；结合OC调用前持久once的说明支持未调用，不能单凭文件缺失证明完整SDK执行史 |

原始journal文件SHA `97acdffc62c2f2d5be2fcff72cb3c7281b96be53630f73d34e81470c2dc1a5e8`；identity文件SHA `27b8ccc68925dde67944fc80c8bb09b924c37e7ad3ca0f8e01a9945cd53970ba`；候选派生JSON SHA `e7a4b76bd7f046dde8d74cf3a6e031d2b653596550c93dd15317a0d01f710927`。核验前后源文件摘要不变。

可复验的只读命令，无SDK/Redis/数据库操作，不导入状态：

```bash
python3 scripts/audit-ctp-fill-v2-review.py outputs/recovery/ctp_fill_v2_review_20261117
```

OC提供的42项CTest/4项sanitizer日志是OC本机证据，Relay未运行OC二进制，也未独立重演其完整TradeEvents/outbox恢复。当前通过的是材料及派生身份核验，不是运行迁移、发布、报撤或最终F0验收。

### Relay已完成的接收约束

1. Go查询和实时成交解码均支持严格v1及SHFE v2；不替换旧v1原文，不允许v2套用其他交易所。同键价格/数量/合约/开平/源时间变化仍保留事实冲突。
2. 完整查询内部（含跨页）拒绝混合v1/v2。持久账本以`env/broker/account/gateway/trade_date`为范围，由第一条合法成交固定身份版本；跨消息混用的原文保留、范围标记blocked，第二版本不生成额外经济成交。版本从持久观察按原顺序重建，重启不能解除。
3. 相同v2成交从查询和实时重复到达只产生一笔执行事实，保留各次费用证据。不能因为gateway_order_id/会话变化重复计数，也不能靠重新发送把旧v1自动升级。
4. 本次真实联调库无既存成交，后续在来源/epoch和OC派生状态核验后可首次接v2；**不是自动迁移许可**。其他已存在v1的账本必须另做显式重建/更正核对，本次未实现通用v1迁移执行器。
5. 原`query-page.schema.json`及历史fixture保持原字节，新扩展为`testdata/ctp/query-page-fill-v2.schema.json`，文档ID `urn:relay:ctp:query-page:draft:2:fill-identity-v2`。wire仍为`relay.stream.v1 / ctp.f0.draft.2`，身份版本由fill_id前缀区分；JSON Schema只验证结构，哈希及跨页/账本混用由解码器校验。Python参考检查器已改用扩展schema。

共享向量仍为`testdata/fixtures/ctp_fill_identity_v2_candidate_20261117.json`，SHA `e032e5b4e90ab2d0266fffbddf37d19ff475090c9cb5800c0aa1ebc80587a42b`，其中candidate标签保留为原材料历史标记。原schema SHA `988891918ed1abc6889832d1aa446a515d77e52ca8d5e9d359f566f63beee47c`不变。

验证：Go全包、专项race及独立临时PG的双版本/重复查询与实时/重建回归、相关vet、Python契约111项通过。PG回归使用独立可丢弃测试库，不写真实联调账本。本轮未重启9092或替换运行二进制；源码已具备接收能力，启用新接收进程前须按该版本构建。

### OC接下来直接完成

- 将v2贯通查询、实时、重连及恢复；按已审核计划建立带来源摘要的派生身份，不覆盖旧journal/identity，不同时发布v1/v2为两笔成交。
- 完成可持久恢复的外部来源证明及输出，明确50条回调逐类结果；18条audit_only不能清成业务成功，32条pending不能仅因已算出新ID就全部清空。
- 分离只读查询准入与交易准入：允许必要的完整查询解决“可关联但待来源证明”的回放；命令/ACK/来源未知及经济冲突继续阻断。请给实际新心跳JSON及回归结果，Relay据此核验门禁，不从pending数量猜测ready。
- 交付可运行修复版本及上述结果后再安排新窗口。原13:35截止不延长；不自动重启、重新确认结算、报撤或换路由规避责任。资金BizType仍独立未解决，不补默认值。无需再次讨论编码，无需源码或二进制互传。

## 已确认的问题

用户转交OC版本7d83ccd的退出报告：`callback_invalid_fill / fill_identity_conflict`。SHFE、20261117、ad2701、TradeID=5013的两条成交，分别属于OrderSysID=31174/买、31234/卖。现有七段键没有包含委托身份或买卖方向，因而得到相同fill_id。这是双方共有的身份契约缺口，不是OC单方实现偏离。

最初Relay的`internal/futures/trade.go`同样仅支持v1七段键，查询与实时成交均受影响。最初仅按报告身份元组复现；本轮已经接收原包并完成上述独立核验和接收实现。旧运行二进制不能直接接收新成交，需在后续受控窗口使用新构建；未提供的费用不补造。

OC报告未创建settlement once，也没有请求/返回/回调审计，异常位于确认前drain；这支持本轮未调用结算SDK、未消耗单次授权的结论，证据来源明确为OC。Relay独立证明本轮两命令流0条、PG无新命令/交易发布；不凭同步0或TradingDay独立宣布本次登录/结算验收通过。13:35原窗口已截止。

## 最小成交键候选

以下精确编码已经OC回签并由双方离线核验，Relay源码已支持；真实发布仍待OC运行修复、旧责任复核和新的授权窗口。

```text
编码域: ctp.identity.v2/fill/
固定九段:
env, route broker_id, logical account_id, gateway_id,
TradingDay, ExchangeID, TradeID, OrderSysID, trade_side

每段: 十进制UTF-8字节长度 + ':' + 原字节
无分隔换行，无JSON序列化参与哈希
fill_id = 'ctp-fill-v2-' + SHA256(上述完整编码字节)小写hex
trade_side使用现有标准B/S，不使用整数0/1；CTP原生Direction '0'->B、'1'->S。
```

沿用既有OrderSysID/TradeID两端ASCII空格去填充及定长字符解码规则，原始字节保留；前导零、大小写不改。缺失/无效字段保留为原始待处理或冲突，不能猜值。OrderRef、OC进程session、原生FrontID/SessionID、接收时间、逻辑gateway_order_id、价格/数量/费用均不参与成交身份。方向无法正确映射不允许创建候选键。

OrderSysID标识所属委托，TradeID标识其成交；显式买卖方向区分本轮发现的两侧事实。同一新复合键若出现数量、价格、合约、开平或源时间矛盾，仍是执行事实冲突，不能再加随机后缀消除。完整复合键改变也不等于自动证明“新成交”：原始身份修正应作为更正关系核对，不能把修正或冲正按新成交追加收益。

共享纯JSON：`testdata/fixtures/ctp_fill_identity_v2_candidate_20261117.json`。前两条身份转录自报告，第三条前导零用例明确为合成数据；包括字节串和独立计算摘要。对应本轮两侧候选为：

| OrderSysID / 方向 | 候选fill_id |
| --- | --- |
| 31174 / B | ctp-fill-v2-c43bada1b2248121701a3ae04bb765280df13289d2cb918ef6a2339410a4f8c7 |
| 31234 / S | ctp-fill-v2-fb3f70c23031778a9f971c799c545aac3a551c3384c0c649f0ef5c8583ad1de3 |

这是SHFE首期修正候选，不泛化宣称所有交易所/经纪商的TradeID全局唯一性已证明。外部订单`external-ctp-v1-*`、本地gateway_order_id、命令幂等及ACK规则不变；查询/实时/启动回放/重连必须用同一成交编码，不能各走一套。标的定义继续以Meridian为准，这次不需要Meridian增加字段。

## 旧状态与混合版本

1. 不改写原journal、callbacks、identity观察结果和已发布body，不删除旧冲突或将pending改成已处理。
2. 一条v1身份现在可能对应多条v2事实，禁止单值“旧ID->新ID”替换。必须逐条根据原始完整元组生成有版本的派生记录，保留旧观察ID及摘要；不以“最后一条覆盖”修正历史。
3. v1/v2不能同时作为两笔经济成交计数。迁移前核对各端已发布/已入账责任；有既存成交则需显式版本切换及别名/更正关系，不在运行时双写累加。Relay此次独立真实TEST库尚无成交业务入账，但仍须实际查询确认后处理，不能推及其他库。
4. 本轮OC命令/outbox为0不代表50条原始回调可以丢弃。先提供只读迁移计划，证明所处理的是持久身份派生状态，不引起SDK查询/报撤或重发历史命令。原13:30停止记录保留。
5. 双方完成新编码和旧记录处理回归、明确新输出版本后再共同启用。不能只靠换gateway/state绕过冲突，不要求重建Redis；已有绑定必须再次核验。

## 启动回放与本轮交易的边界

- 这40条订单/10条成交是私有流回放，源时间早于进程启动。不能关联成下午本轮报单或撤单，也不能填本轮origin_message_id；来源不明时不伪造本地命令。
- 不把所有非本地委托一律丢弃。能依据原生投资者/交易日/ExchangeID/OrderSysID完成外部关联的，按既有外部规则留证；属于历史或未支持范围的，保留明确分类和原因。`audit_only`不等于已完成业务处理。
- 修正后把这50条回调按原顺序及重排/重复进行离线回放，解释18条audit_only及32条pending分别如何结束。已知两侧成交都保留，不因同TradeID合并；无法关联的仍待处理，不清空队列冒充ready。
- 不能为避开这次回放直接更换SubscribePrivateTopic模式，也不能仅提高队列上限或忽略异常。正常的历史回放应可处理；真实身份/经济冲突仍阻断。
- 如果初始化查询是解除待关联状态的必要条件，应让只读查询准入与交易准入分别表达，不能让“等待查询才能解决的pending”反过来禁止查询。须指出实际等待依赖后再改，不笼统取消所有pending门禁。

## 双方并行交付

OC：九段编码和原始材料已完成；剩余为原主程序运行切换、来源证明、回放处理、查询准入及持久恢复回归、新心跳。不要为此写临时直连程序。

Relay：已完成v2严格解码、分页/跨消息版本保护、查询实时防重与临时PG重建回归；独立复核本轮原材料。旧fixture保持原字节，尚未升级正在运行的接收进程或进行在线验收。

交付只需本说明及共享JSON，不交换源码或二进制。双方收到纯材料即可并行开发；下一轮只需回答编码是否一致、旧派生记录如何处理、50条回放结果及真实运行版本，不再重开架构讨论。修复完成后另约日间窗口；本次原窗口不得自动顺延，即使单次结算授权尚未使用也不自动重启。
