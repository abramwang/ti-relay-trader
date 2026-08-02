# Relay Python DataFrame 版本策略

更新时间：`2026-08-02 CST`

状态：`accepted`

上游提案：`/home/ti-prism-factor/docs/cross-project-python-dataframe-version-policy.md`

## Relay 结论

Relay 接受 QuantStage 的 pandas 共同基线，但当前不在生产交易链路或公共 Python SDK 中新增 pandas 依赖。

| 使用位置 | Relay 口径 |
| --- | --- |
| Go API/worker | 无 Python DataFrame 运行时依赖 |
| `relay-sdk` | 保持 Python 标准库实现，`dependencies=[]` |
| 盘前、盘后和每日绩效任务 | 当前不依赖 pandas；继续使用 JSON/CSV 和标准库模型 |
| 交割单导入、绩效 golden fixture、临时对账工具 | 如使用 pandas，必须通过项目 constraints 固定 `pandas==2.3.3` |
| 未来公共 SDK DataFrame extra | 只有形成明确需求后才增加，兼容范围必须是 `pandas>=2.3,<3` |

精确约束文件为：

```text
configs/constraints/quantstage-pandas.txt
```

需要 DataFrame 的隔离环境使用：

```bash
python -m pip install -c configs/constraints/quantstage-pandas.txt pandas
```

不要为了版本对齐在 Relay 主虚拟环境或 `relay-sdk` 基础安装中预装 pandas。没有 DataFrame 计算的进程保持更小的依赖和 ABI 面。

## 数据边界

1. Relay HTTP/SSE 和 Redis Stream 协议继续使用结构化 JSON；CSV 只用于明确的导出、人工金标和未来交割单文件输入。
2. 不使用 pickle 或 pandas 私有序列化作为跨服务协议，也不把 DataFrame 内存布局当作正式 schema。
3. A 股业务 timestamp 必须显式按 `Asia/Shanghai` 解释或携带时区；交易日继续使用既有 `YYYYMMDD`/schema 字段，不依赖 pandas 自动推断。
4. nullable dtype、索引、排序、缺失值、`NaN` 和无穷值必须由导入 schema 或 fixture 显式约束；正式 JSON 账本不接受非有限数值。
5. 若未来使用 Arrow IPC 或 Parquet，PyArrow 版本单独按 Python/native ABI 管理，不能从 pandas 版本反推。

## 验收与升级

- 单元测试固定检查 constraints 中只有 `pandas==2.3.3`，并确认 `relay-sdk` 没有 pandas 依赖或导入。
- 未来引入 DataFrame 工具时，测试报告必须记录 Python、pandas、NumPy、PyArrow 和相关 SDK 版本，并比较 dtype、schema、行序、时区、缺失位图和 checksum。
- pandas patch/minor 升级先通过 QuantStage 联合 fixture，再更新 constraints、测试证据和本文件；公共 SDK 只保持 `>=2.3,<3` 兼容范围，不锁 patch。
- NumPy 和 PyArrow 不属于本次统一范围，Relay 不额外创建未经验证的共同版本。

## 当前现场确认

`2026-08-02` 检查结果：Relay `.venv` 未安装 pandas、NumPy、PyArrow 或 Polars；`relay-sdk 0.1.24` 的 `dependencies` 为空；Go API/worker 和现有 Python 任务不导入这些包。当前无需重建 SDK 安装包或重启 9092。
