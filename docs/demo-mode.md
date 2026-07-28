# Demo Mode

Demo Mode 用于产品浏览、培训和无设备演示。它是显式运行模式，不是错误回退。

## 保证

- 页面顶部持续显示 `DEMO MODE · 演示模式`。
- 不请求 `NEXT_PUBLIC_API_BASE`。
- 所有资产、趋势、告警、诊断和审计来自 `app/monitored-data.ts` 的确定性 fixture。
- UI 操作仅改变当前浏览器内存状态；刷新后恢复。
- 不保存凭据、不创建真实用户、不通知、不运行 discovery、不执行命令。

## 启动

```bash
docker compose -f compose.demo.yaml up --build
```

或本地：

```bash
NEXT_PUBLIC_MONITORED_MODE=demo NEXT_PUBLIC_API_BASE= npm run dev
```

## Live Mode 的反回退约束

`NEXT_PUBLIC_MONITORED_MODE=live` 时，API 未配置、401、网络错误或空列表都显示真实状态。Live 页面不会把样例记录用于显示、计数或错误回退。

默认 `NEXT_PUBLIC_ALLOW_RUNTIME_SWITCH=false`，构建模式被固定，浏览器历史状态不能把 Demo 容器切成 Live，也不能把生产 Live UI 切成样例数据。仅在需要同一构建做现场演示时显式设置为 `true`；此时顶部切换状态保存在浏览器 localStorage。
