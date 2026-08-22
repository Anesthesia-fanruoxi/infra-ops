# 06 — 安全设计

## 1. 威胁面与对策总览

| 威胁 | 对策 |
|------|------|
| 凭据明文泄露（拖库/备份外泄） | AES-256-GCM 加密落库，主密钥不落仓库 |
| 日志泄露密钥 | 全链路脱敏（rules.md §3.2） |
| 未授权访问 Web | 登录会话 + HttpOnly Cookie，一期单管理员 |
| SSH 中间人 | host key TOFU 指纹校验 |
| SQL 注入 | 全占位符参数（rules.md §3.4） |
| 主密钥丢失 | 提供 keygen 与轮换流程，密文无法恢复需重置凭据 |

## 2. 主密钥（secret_key）管理

- 算法：AES-256-GCM，密文结构 `nonce(12B) || ciphertext || tag`，整体存 BLOB。
- 主密钥为 **32 字节随机数**，`infra-ops keygen` 子命令生成，输出 base64。
- 装载顺序（与配置优先级一致）：env `INFRA_OPS_SECRET` 优先，config.yaml
  `security.secret_key` 兜底。两者皆空则启动失败并提示生成命令。
- **禁止提交真实密钥**；config.yaml.example 仅留空占位，.gitignore 排除 config.yaml。
- 轮换：提供 `infra-ops rekey --old --new`（二期实现，一期文档预埋），
  逐条解密重加密 credentials 表。

## 3. 凭据使用链路

1. 用户在页面输入 secret（私钥/密码）→ api 层内存中持有。
2. api 层调 common/crypto.Encrypt 落库；私钥额外解析公钥指纹用于展示。
3. 后续任何 SSH 操作：store 取密文 → api 调 common/crypto.Decrypt → 传入
   common/sshx 建连。
4. 明文 secret 不进入日志、不返回任何 GET 接口、不写入 info_json 快照。

## 4. 登录鉴权（一期单管理员）

- 账号：config `auth.username`；口令 bcrypt 哈希存 `auth.password_hash`，
  由 `infra-ops gen-password` 生成（不存明文）。
- 会话：登录成功后签发随机 session token，存内存 map + HttpOnly Cookie
  （`infra_ops_session`，SameSite=Lax，Secure 按部署决定），有效期 12h。
  一期单实例内存会话即可（重启需重新登录可接受）；不引入 Redis/JWT 状态。
- 中间件统一拦截 `/api/v1/*`（login/healthz/version 除外），未登录返回 401。
- 连续登录失败 5 次锁定 5 分钟（内存计数）。

## 5. SSH host key TOFU

- 首连：接受并记录 `host_keys` 表（addr → fingerprint）。
- 复连：指纹不一致 → 拒绝连接，返回错误码 1003，写审计 `host.test` 失败详情。
- 显式重置：提供"重置指纹"操作（host.update 触发），用于合法重装系统场景，需审计。
- 禁止默认 InsecureIgnoreHostKey；仅 config 显式 `ssh.host_key_policy=insecure`
  且每次连接打 WARN 日志时允许（rules.md §3.5）。

## 6. 审计（写操作强制留痕）

- 由中间件在写操作响应成功后落 audit_logs（rules.md §4.5），记录 action、
  目标、脱敏 detail、来源 IP。
- 登录成功/失败同样审计。审计表只增不删，一期无清理策略（量小）。

## 7. 部署建议（写入 README）

- 二进制以低权限专用用户运行，但需持有可解密的主密钥 env。
- 监听建议绑定内网网卡或经防火墙限制来源；如需公网暴露必须先加 TLS 反代。
- 定期备份 `data/infra-ops.db` 与 config.yaml（含主密钥则备份同等敏感，须加密存放）。
