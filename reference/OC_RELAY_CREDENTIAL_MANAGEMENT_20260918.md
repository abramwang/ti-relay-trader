# OC 与 Relay 华鑫账户凭据管理协议

日期：`2026-09-18`

适用程序：`oc_trader_commander_huaxin`

协议：`relay.stream.v1`、`oc.secret.v1`

## 1. 目标与边界

一个 OC 进程只服务一个 Relay 标准账户。进程启动时通过参数选择运行环境和账户：

```bash
./oc_trader_commander_huaxin \
  --env prod \
  --account-id 501000114077 \
  --config ./config.ini
```

Relay 负责华鑫登录凭据的录入、加密、版本管理、写入 Redis 和轮换审计。OC 只读取本账户密文，在内存中解密并登录柜台。

本方案的安全目标是避免共享服务器上出现券商账户明文配置，并降低配置被普通登录用户直接查看、复制或误传的风险。它不用于对抗能够读取 OC 进程内存或逆向二进制的特权用户。

以下内容不通过普通 Redis Stream 传输，也不能进入 raw message、reply、event、DLQ 或日志：

1. 华鑫登录用户
2. 华鑫登录密码
3. 华鑫动态密码
4. 环境凭据加密密钥
5. Redis 认证密码

## 2. 启动参数

托管模式参数：

```text
--env test|prod
--account-id <Relay标准账户>
--config <非敏感公共配置文件，可选，默认 ./config.ini>
```

规则：

1. `--env` 和 `--account-id` 必须同时出现。
2. `account-id` 只允许数字，长度为 6 到 20。
3. 参数账户是 Relay 标准账户，例如 `501000114077`，不是华鑫原始柜台账户 `50100011407701`。
4. 一个进程启动后不能切换环境或账户；切换必须启动新进程。
5. 为平滑升级，旧格式 `./oc_trader_commander_huaxin ./config_xxx.ini` 暂时保留。

## 3. 固定 Redis 环境配置

TEST 和 PROD 的 Redis 地址、端口、密码及凭据加密密钥在构建时注入，最终进入部署二进制，不写入仓库配置文件。

构建环境变量：

```text
OC_HUAXIN_TEST_REDIS_HOST
OC_HUAXIN_TEST_REDIS_PORT
OC_HUAXIN_TEST_REDIS_PASSWORD
OC_HUAXIN_TEST_CREDENTIAL_KEY_ID
OC_HUAXIN_TEST_CREDENTIAL_KEY_HEX

OC_HUAXIN_PROD_REDIS_HOST
OC_HUAXIN_PROD_REDIS_PORT
OC_HUAXIN_PROD_REDIS_PASSWORD
OC_HUAXIN_PROD_CREDENTIAL_KEY_ID
OC_HUAXIN_PROD_CREDENTIAL_KEY_HEX
```

`CREDENTIAL_KEY_HEX` 必须是 32 字节随机密钥的小写或大写十六进制表示，共 64 个十六进制字符。TEST 和 PROD 必须使用不同密钥。

示例构建流程只展示变量名，不应把真实值写入脚本或 shell history：

```bash
umask 077
export OC_HUAXIN_TEST_REDIS_HOST='...'
export OC_HUAXIN_TEST_REDIS_PORT='6379'
export OC_HUAXIN_TEST_REDIS_PASSWORD='...'
export OC_HUAXIN_TEST_CREDENTIAL_KEY_ID='hx-test-202609'
export OC_HUAXIN_TEST_CREDENTIAL_KEY_HEX='...64 hex chars...'

export OC_HUAXIN_PROD_REDIS_HOST='...'
export OC_HUAXIN_PROD_REDIS_PORT='6379'
export OC_HUAXIN_PROD_REDIS_PASSWORD='...'
export OC_HUAXIN_PROD_CREDENTIAL_KEY_ID='hx-prod-202609'
export OC_HUAXIN_PROD_CREDENTIAL_KEY_HEX='...64 hex chars...'

cmake3 ..
make -j"$(nproc)"
```

CentOS 7 构建机还需要 OpenSSL 开发包：

```bash
yum install -y openssl-devel
```

构建目录中会生成包含这些值的临时头文件，构建目录权限必须为 `0700`，发布时只复制最终二进制和华鑫依赖库。最终 OC 二进制本身包含 Redis 密码和环境主密钥，必须由专用运行用户持有并设置为 `0700`；不能以 `0755` 放在共享目录供其他用户读取。

仓库提供统一配置脚本。真实值只保存在受信任构建机的忽略文件中：

```bash
cd /home/Titian_Cpp/reference/部署
cp build_profiles.env.example build_profiles.env
chmod 600 build_profiles.env
# 填写 TEST/PROD Redis 连接及两套独立主密钥后执行：
./configure_managed_build.sh
cmake --build /home/Titian_Cpp/oceanus/src/oc_trader_commander_huaxin/build -- -j"$(nproc)"
```

`build_profiles.env` 不得提交、备份到普通共享目录或复制到券商服务器。Relay 仅需通过安全渠道获得对应环境的 `CREDENTIAL_KEY_ID` 与 `CREDENTIAL_KEY_HEX`，Redis 密码按双方原有部署边界管理。

## 4. Stream 与凭据 Key

OC 根据参数生成业务 Stream：

```text
relay:{env}:v1:huaxin:{account_id}:cmd.trade
relay:{env}:v1:huaxin:{account_id}:cmd.query
relay:{env}:v1:huaxin:{account_id}:reply
relay:{env}:v1:huaxin:{account_id}:event
relay:{env}:v1:huaxin:{account_id}:hb
relay:{env}:v1:huaxin:{account_id}:dlq
```

凭据使用普通 Redis String，不使用 Stream 或 Hash：

```text
relay:{env}:v1:huaxin:{account_id}:secret:credentials:current
relay:{env}:v1:huaxin:{account_id}:secret:credentials:v{credential_version}
```

`current` 的值是十进制正整数版本号。版本 Key 的值是完整 JSON 加密信封。

凭据不设置短 TTL。若需要停用账户，Relay 删除 `current` 或将账户标记为禁用，并协调重启 OC。已经登录的 OC 不会因为 Redis Key 被删除而自动退出柜台。

## 5. 加密规范

算法固定为：

```text
AES-256-GCM
key: 32 bytes
nonce: 12 random bytes, every encryption must be unique
tag: 16 bytes
encoding: standard Base64 with padding
plaintext: UTF-8 compact JSON
```

附加认证数据 AAD 必须按以下格式逐字节拼接，不包含换行：

```text
oc.secret.v1|{env}|huaxin|{account_id}|{credential_version}|{key_id}
```

例如：

```text
oc.secret.v1|prod|huaxin|501000114077|3|hx-prod-202609
```

Redis 中的加密信封：

```json
{
  "protocol": "oc.secret.v1",
  "algorithm": "A256GCM",
  "env": "prod",
  "broker_id": "huaxin",
  "account_id": "501000114077",
  "credential_version": 3,
  "key_id": "hx-prod-202609",
  "nonce_b64": "...",
  "ciphertext_b64": "...",
  "tag_b64": "...",
  "issued_at": "2026-09-18T10:00:00+08:00"
}
```

解密后的 JSON：

```json
{
  "broker_login_user": "...",
  "broker_password": "...",
  "dynamic_password": "..."
}
```

`dynamic_password` 可以为空字符串；`broker_login_user` 和 `broker_password` 不能为空。

Relay 和 OC 必须校验信封中的 `protocol`、`algorithm`、`env`、`broker_id`、`account_id`、`credential_version` 和 `key_id`。任一字段不一致均不得尝试解密或登录。

## 6. Relay 写入与轮换流程

首次配置或轮换凭据时：

1. Relay 获取当前版本并生成更大的新版本号。
2. Relay 使用目标环境的密钥和全新随机 nonce 加密凭据。
3. Relay 先写入版本 Key，例如 `credentials:v3`。
4. Relay 读取并校验写入结果。
5. Relay 最后更新 `credentials:current=3`。
6. 运维重启对应账户 OC。
7. Relay 等待新 OC 心跳确认 `credential_status=loaded`、`credential_version=3`、`credential_key_id=hx-prod-202609`。
8. 验收完成后，Relay 可以按保留策略删除旧版本密文。

禁止先更新 `current` 再写版本 Key。OC 不支持运行过程中热切换华鑫登录身份，凭据更新通过单账户进程重启生效。

## 7. OC 启动流程

1. 校验命令行环境和账户。
2. 选择编译进二进制的 TEST 或 PROD Redis 端点。
3. 根据环境和账户生成 Stream 与凭据 Key。
4. 连接并认证 Redis。
5. 读取 `credentials:current`。
6. 读取对应版本的加密信封。
7. 校验所有信封身份字段。
8. 使用目标环境密钥执行 AES-256-GCM 认证解密。
9. 校验明文 JSON 必填字段。
10. 将登录凭据交给华鑫驱动并发起柜台连接。
11. 订阅该账户 Redis 命令 Stream。

任何一步失败都必须 fail closed：不连接华鑫、不消费交易命令、不发布可交易状态，并持续通过 heartbeat 报告原因。

## 8. Heartbeat 增量字段

托管模式心跳增加：

```json
{
  "credential_status": "loaded",
  "credential_version": 3,
  "credential_key_id": "hx-prod-202609",
  "credential_source": "relay_redis_encrypted",
  "managed_account_id": "501000114077"
}
```

`credential_status` 取值：

```text
pending
missing
invalid_envelope
decrypt_failed
invalid_plaintext
loaded
```

未达到 `loaded` 时：

```json
{
  "state": "DEGRADED",
  "state_text": "credential_not_ready",
  "broker_ready": false,
  "accepting_trade_commands": false,
  "accepting_cancel_commands": false
}
```

Heartbeat 不得包含 Redis Key 全文之外的密文、nonce、tag、登录用户、密码或动态密码。

## 9. Redis 权限建议

Relay 对凭据 Key 拥有写权限；OC 只需要读取权限。业务权限和凭据权限应限制到目标环境、券商和账户前缀。

如果 Redis 版本支持 ACL：

1. Relay 用户允许写入 `*:secret:credentials:*`。
2. OC 用户只允许 `GET` 本账户凭据 Key，并拥有本账户 Stream 所需权限。
3. 禁止 OC 执行 `SET/DEL` 修改凭据。
4. 禁止其他账户 OC 读取本账户凭据 Key。

## 10. Crontab 部署

一个脚本可以启动多个独立账户进程：

```bash
OC_ENV=prod
ACCOUNTS=(314000045768 314000046830 307000051387)

for account in "${ACCOUNTS[@]}"; do
  ./oc_trader_commander_huaxin \
    --env "$OC_ENV" \
    --account-id "$account" \
    --config ./config.ini &
done
```

正式脚本必须增加 PID 文件、防重复启动、独立日志和逐账户停止，不使用 `killall`。

仓库参考实现位于：

```text
reference/部署/start_services.sh
reference/部署/stop_services.sh
reference/部署/cront.md
reference/部署/config.ini.example
reference/部署/build_profiles.env.example
reference/部署/configure_managed_build.sh
```

公共 `config.ini` 只保留华鑫柜台地址、FENS/终端注册信息和 OC 运行参数，以下字段必须留空：

```ini
user =
pass =
dynamic_password =
```

部署前建议设置：

```bash
chmod 700 /home/userztcf/oceanus/start_services.sh
chmod 700 /home/userztcf/oceanus/stop_services.sh
chmod 700 /home/userztcf/oceanus/oc_trader_commander_huaxin
chmod 600 /home/userztcf/oceanus/config.ini
```

TEST 与 PROD 可以使用同一二进制，但华鑫柜台 `locations`、FENS 和终端注册参数可能不同，因此应分别准备非敏感公共配置，例如 `config.test.ini`、`config.prod.ini`，并通过 `OC_CONFIG` 选择。`--env` 负责选择 Redis 和密钥，不会替换华鑫柜台地址；上线前必须核对配置文件与目标环境一致。

从旧部署迁移时，先由 Relay 写入所有账户凭据，再逐账户启动新进程并验证 heartbeat。确认全部账户为 `credential_status=loaded` 后，应从共享服务器删除原 `config_<account>.ini` 及可能包含凭据的历史副本；旧日志也应检查后按保留策略清理。

## 11. Relay 验收清单

1. TEST 参数只连接 TEST Redis，PROD 参数只连接 PROD Redis。
2. OC 只读取参数账户的凭据和 Stream。
3. 错误账户、错误环境、错误版本和错误 `key_id` 均不能登录。
4. 修改密文、nonce、tag 或 AAD 任一字节后，OC 必须报 `decrypt_failed`。
5. 凭据缺失时 OC 不消费交易命令，心跳为 `credential_not_ready`。
6. 凭据正确时心跳报告准确版本，华鑫登录完成后才允许交易。
7. Relay 轮换版本并重启单账户 OC 后，新版本生效，其他账户进程不受影响。
8. OC 日志、Redis 业务 Stream、DLQ 和 raw 归档中不存在凭据明文。
