# Agent 自主更新

Monitored 的 Agent 更新复用现有 mTLS/gRPC 任务流，不开放新的远程 Shell。只有
Admin 可以缓存和下发版本；目标 Agent 还必须通过本地环境变量选择允许的更新方式。
每次缓存、下发、失败、暂存和完成都会写入审计链。

## 两种更新方式

### Master 缓存二进制

1. 在“设置 → Agent 与证书 → Agent 自主更新”上传与 Agent 的 OS/架构匹配的 Release
   `monitored-agent_<version>_<os>_<arch>.bin`；不要直接上传 `.tar.gz`。
2. Master 把包保存到持久化 `MONITORED_AGENT_UPDATE_CACHE_DIR`，记录大小和 SHA-256。
3. 下发时 Master 生成最长 24 小时有效的 HMAC 下载地址，并通过已认证的 gRPC 流发送任务。
4. Agent 下载到目标目录的临时文件，校验大小、SHA-256，并执行 `--version` 验证目标版本。
5. Agent 把当前文件保存为 `.previous`，原子替换二进制，报告任务结果后退出；systemd
   `Restart=on-failure` 启动新版本。重新注册且版本匹配后，Master 才标记安装成功。

systemd 安装使用：

```dotenv
MONITORED_AGENT_UPDATE_MODE=binary
MONITORED_AGENT_UPDATE_INSTALL_PATH=/usr/local/bin/monitored-agent
MONITORED_AGENT_UPDATE_RESTART_DELAY=5s
```

仓库内的 unit 为状态目录和 `/usr/local/bin` 增加写权限以支持同目录原子 rename；这意味着
Agent 服务账户可以写该目录，应避免在其中放置无关可执行文件。若新版本无法启动，可把
`/usr/local/bin/monitored-agent.previous` 恢复为正式文件后重启服务。上传前仍应核对
Release 的官方校验和；Master 的摘要保证缓存到安装过程不被静默修改，不替代发布签名。

### Docker Pull

Docker 清单必须包含不可变的 `sha256:` 镜像摘要。Agent 会执行：

```text
docker pull <image>@sha256:<digest>
```

Master 不会下发容器重建命令。激活动作只能来自 Agent 主机的
`MONITORED_AGENT_DOCKER_RESTART_COMMAND`，因此控制面被攻破时不能借更新任务注入 Shell。
本地策略示例：

```dotenv
MONITORED_AGENT_UPDATE_MODE=docker
MONITORED_AGENT_DOCKER_RESTART_COMMAND=docker compose -f /opt/monitored-agent/compose.yaml up -d --force-recreate agent
```

官方 Agent 镜像包含 Docker CLI；容器化 Agent 仍需把 Docker socket 以受控方式挂入，并允许
其 UID 访问 socket。Docker socket 等同宿主机 root 权限，不应默认挂载；无法接受该边界时，
使用 systemd 二进制更新。未配置本地重建命令时，镜像只会被拉取并显示为 `staged`，不会假装
已经激活。

## 配置

Master：

| 环境变量 | 默认值 | 含义 |
| --- | --- | --- |
| `MONITORED_AGENT_UPDATE_CACHE_DIR` | `./data/update-cache` | 持久化包缓存 |
| `MONITORED_AGENT_UPDATE_MAX_BYTES` | `268435456` | 单个二进制包上限 |

Agent：

| 环境变量 | 默认值 | 含义 |
| --- | --- | --- |
| `MONITORED_AGENT_UPDATE_MODE` | `disabled` | `disabled`、`binary`、`docker`、`auto` |
| `MONITORED_AGENT_UPDATE_INSTALL_PATH` | 当前可执行文件 | 二进制原子替换目标 |
| `MONITORED_AGENT_UPDATE_RESTART_DELAY` | `5s` | 报告结果后等待多久重启 |
| `MONITORED_AGENT_DOCKER_RESTART_COMMAND` | 空 | 仅由现场管理员配置的容器重建命令 |

`auto` 同时允许二进制和 Docker 策略，除受控测试环境外不建议使用。
