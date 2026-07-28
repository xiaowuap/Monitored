# 安全模型

## 角色

- Viewer：只读资产、遥测、告警、诊断和审计。
- Operator：Viewer 能力，加发现、规则、维护窗口、标注审核和动作草案。
- Admin：凭据、通知、用户、动作批准和平台配置。

用户密码使用 bcrypt cost 12。初始环境变量管理员用于 bootstrap；随后可通过 `/api/v1/users` 创建独立角色。会话是 HMAC 签名、HttpOnly、SameSite=Strict Cookie；HTTPS 环境启用 Secure。

## 凭据

连接与通知 secret 使用 Master key 派生的 AES-256-GCM 加密，并把连接类型/端点或通知类型/名称作为 associated data，防止跨记录替换。API 只返回 `hasCredentials/hasSecret`，不返回密文或明文。

生产必须使用随机 32+ 字符 Master key、文件/容器 secret 挂载和受限数据库账户。更换 Master key 前需要先导出并重新封装 secret；当前版本不自动双密钥轮换。

## Agent

正式边界是 TLS 1.3 双向认证。客户端证书身份必须匹配 Agent ID。添加设备使用绑定 Agent ID 的短期一次性令牌；Agent 本地生成 Ed25519 私钥，Master 只签 CSR。短期证书会在已认证的 gRPC 流上自动换钥续签，凭据以单个 `0600` 文件原子替换。Agent CA 私钥只允许 Master 的受限 signer 读取。开发 Compose 的 insecure gRPC 有明显配置项，不能用于跨主机网络，而且不能签发轮换证书。REST bearer ingest 只作为兼容通道。

自主更新默认由 Agent 本地策略禁用。二进制包经过 Admin 上传、目标 OS/架构匹配、短期
HMAC 下载授权、长度/SHA-256 校验和 `--version` 试运行后才原子替换，并保留 `.previous`。
Docker 只接受不可变 digest；容器重建命令必须在 Agent 本机配置，Master 不能注入。挂载
Docker socket 等同授予宿主机 root，属于显式高权限部署选择。详见 [Agent 自主更新](agent-updates.md)。

## 动作

- Master 与 Agent 均重新解析命令，永不调用 Shell。
- 拒绝 `; | & > < $ ()` 等控制语法。
- 可执行文件、工作目录和环境变量有允许列表。
- 规范载荷包括目标、argv、目录、环境、身份和超时，审批后以 SHA-256 冻结。
- Admin 审批生成 15 分钟一次性令牌；任务 claim 后数据库立即移除明文令牌。
- `run_identity` 会映射为真实 OS uid/gid；root Agent 若找不到低权限账户会拒绝执行，不会静默以 root 代跑。
- root 默认禁用。启用前 Admin 必须完成 TOTP；每次 root 审批都重新校验密码和 TOTP，且目标 Agent 还必须显式设置 `MONITORED_ALLOW_ROOT_ACTIONS=true`。
- 非交互、超时、进程组终止、stdout/stderr 上限。

示例执行器允许列表是安全基线，不代表所有列出的命令参数都是业务允许的；生产应按动作 kind 使用更窄的参数模板。容器 Agent 默认以 65532 运行，即使设置 root 开关也不具备 root 执行能力；需要 root 的现场 Agent 必须由运维明确提供对应系统权限。

## LLM

遥测、日志与设备文本被明确标为不可信数据，system prompt 要求忽略其中指令。输出必须解析为受限 JSON 结构。标注只能成为 Pending 建议；接受时也只更新展示别名和类别，不改原始身份、单位、换算或阈值。LLM 不触发告警、不执行命令；动作建议只能转为人工审批草案。

## 扫描与审计

CIDR 扫描不使用默认密码、不猜口令，只访问明确协议/端口，并限制地址数、并发和速率。审计采用 previous hash/entry hash 追加链，可检测应用层篡改；它不声称防御同时掌握数据库与 Master key 的宿主机管理员。需要更强保证时把审计流复制到外部 WORM/SIEM。
