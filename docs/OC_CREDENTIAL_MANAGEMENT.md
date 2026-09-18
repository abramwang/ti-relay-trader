# OC 账户凭据管理

更新时间：`2026-09-18`

本文记录 Relay 对 `oc.secret.v1` 的实现。完整契约以
[`reference/OC_RELAY_CREDENTIAL_MANAGEMENT_20260918.md`](../reference/OC_RELAY_CREDENTIAL_MANAGEMENT_20260918.md)
为准。

## 边界

- 一个 OC 进程只服务一个 `environment + broker_id + account_id`。
- Relay 负责录入、AES-256-GCM 加密、Redis 版本切换和 PostgreSQL 审计。
- OC 只读取本账户密文并在进程内解密；不通过业务 Stream 传递凭据。
- 登录用户、登录密码、动态密码、Redis 密码和主密钥不得进入日志、raw、reply、event、DLQ、API 响应或 SDK。
- Python SDK 不提供凭据管理方法；这是 Relay 内网运维能力，不是策略接口。

## Redis Key 与加密

凭据使用普通 Redis String：

```text
relay:{test|prod}:v1:huaxin:{account_id}:secret:credentials:current
relay:{test|prod}:v1:huaxin:{account_id}:secret:credentials:v{version}
```

版本信封使用 `AES-256-GCM`、12 字节随机 nonce、16 字节 tag 和带 padding 的标准 Base64。AAD 必须逐字节等于：

```text
oc.secret.v1|{env}|huaxin|{account_id}|{version}|{key_id}
```

TEST 与 PROD 必须使用不同的 32 字节主密钥。Relay 只从进程环境读取 Key ID 和 hex key：

```text
RELAY_OC_HUAXIN_TEST_CREDENTIAL_KEY_ID
RELAY_OC_HUAXIN_TEST_CREDENTIAL_KEY_HEX
RELAY_OC_HUAXIN_PROD_CREDENTIAL_KEY_ID
RELAY_OC_HUAXIN_PROD_CREDENTIAL_KEY_HEX
```

跨语言兼容测试向量：

```text
key_hex: 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f
nonce_b64: AAECAwQFBgcICQoL
AAD: oc.secret.v1|test|huaxin|00030484|7|hx-test-vector-1
plaintext: {"broker_login_user":"demo-user","broker_password":"demo-password","dynamic_password":"123456"}
ciphertext_b64: PCC0aaqOp2nSLfjs2IcnGPCz9RbKWTsZVQjI8G4McpAtMsyOwKp36ivUHp778EdKintarz6zzrUS50tqa5SanJQe6li3qEgAcT3JMZ/ufZtP5bsGVFlxWc/OqvlO0sQ=
tag_b64: 6+hjilk8OXUKgZiV/1RLAw==
```

该向量只用于实现验收，禁止将示例 key 用于任何环境。

本机模板为 `config/relay.credentials.env.example`。真实文件固定为
`config/relay.credentials.env`、权限 `0400` 或 `0600`，已被 Git 忽略。服务脚本会在启动 API 前加载该文件；密钥必须与 OC 对应环境构建时注入的值完全一致。

## 写入与停用

轮换顺序固定为：

1. 先把 `started` 审计写入 PostgreSQL。
2. 获取账户级短锁并分配大于当前值的新版本。
3. 使用新 nonce 写入不可覆盖的 `credentials:vN`。
4. 读取、严格解析、校验身份并执行一次认证解密。
5. 最后才原子更新 `credentials:current=N`。
6. 写入 `succeeded` 审计；失败则写入错误码，不删除历史版本。
7. 单独重启目标账户 OC，以心跳中的版本确认生效。

停用只删除 `current` 指针，不删除历史密文和交易账本。已经登录的 OC 不会自动退出，因此必须停止或重启对应账户进程。

CLI：

```bash
set -a
source config/relay.credentials.env
set +a

go run ./cmd/relayctl credentials status \
  -config .runtime/active-config.yaml -account 00030484 -verify

go run ./cmd/relayctl credentials rotate \
  -config .runtime/active-config.yaml -account 00030484 \
  -operator relay-admin -input /secure/path/credential.json

go run ./cmd/relayctl credentials disable \
  -config .runtime/active-config.yaml -account 00030484 \
  -operator relay-admin -confirm-account 00030484
```

输入文件权限不得超过 `0600`，结构仅包含：

```json
{
  "broker_login_user": "...",
  "broker_password": "...",
  "dynamic_password": ""
}
```

## Web 运维入口

`/operations` 的“OC 账户凭据”模块可以查询状态、写入新版本和停用当前版本。启用条件：

1. 本地 YAML 设置 `operations.credential_admin_enabled: true`。
2. `RELAY_CREDENTIAL_ADMIN_TOKEN` 至少 32 个字符。
3. 当前环境主密钥可用，PostgreSQL migration 30 已应用。

Relay 只部署在受控内网，因此允许从内网 HTTP 页面操作。管理员令牌仍必须提供，只保存在浏览器 `sessionStorage`；密码输入不会保存，提交完成后立即清空。生产轮换必须再次准确输入目标账户，停用在两个环境都要求账户确认。

API 路由 `/v1/admin/credentials*` 不加入公开交易 Schema，也不进入 Python SDK。响应只包含环境、账户、版本、Key ID、签发时间、信封 SHA256 和重启要求，不回显密文或明文。

## 心跳与失败关闭

Relay 读取 OC 托管模式心跳中的：

```text
credential_status
credential_version
credential_key_id
credential_source
managed_account_id
```

`credential_status != loaded` 或 `state_text=credential_not_ready` 时，下单准入以 `OC_CREDENTIAL_NOT_READY` 失败关闭。已加载凭据但 `managed_account_id` 与路由账户不一致时，以 `OC_CREDENTIAL_IDENTITY_MISMATCH` 失败关闭；版本、Key ID 或来源字段不完整时以 `OC_CREDENTIAL_METADATA_INVALID` 失败关闭。旧版 OC 不提供这些字段时保持兼容，便于逐账户滚动升级。

## 上线步骤

1. 分别生成 TEST/PROD 主密钥并通过安全渠道注入 Relay 与 OC 构建环境。
2. 应用 migration 30，配置 Relay 管理令牌和目标环境 Key ID/Key。
3. 先在 TEST 为单账户写入凭据，启动新版 OC，确认 `credential_status=loaded`、版本和账户一致。
4. 验证查询、最小订单、事件、重启恢复以及业务 Stream 不含明文。
5. PROD 逐账户写入并逐进程切换；任何时候都不批量重启全部账户。
6. 全部验收后删除旧明文账户配置及包含凭据的历史副本。

当前 TEST 仍使用旧 OC 的 `relay:prod:*` 兼容命名。切换新版托管 OC 时，必须在同一维护窗口把 TEST 的 `redis.env` 和账户 `stream_prefix` 一起改为 `relay:test:*`，不能只改一端。
