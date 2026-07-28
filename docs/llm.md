# LLM 标注与诊断

LLM 是可选的辅助层。没有配置 provider 时，采集、规则、Incident、通知和动作审批仍正常工作。所有设备文本都被包在“不可信遥测”边界内；模型输出必须通过受限 JSON 解析。

## Provider

Worker 环境变量：

```dotenv
MONITORED_OPENAI_BASE_URL=https://provider.example/v1
MONITORED_OPENAI_API_KEY=...
MONITORED_OLLAMA_BASE_URL=http://ollama:11434
MONITORED_LLM_TOKEN_BUDGET=8000
```

OpenAI-compatible 使用 `/chat/completions` JSON object 响应；Ollama 使用 `/api/chat` JSON format。API key 只进入 Worker，不会发给 Web。

## 传感器标注

未知传感器先按原始名称、单位、来源和路径入库，不等待模型。Operator 可显式排队：

```json
POST /api/v1/llm/suggestions/generate
{
  "sensorId": "<uuid>",
  "provider": "ollama",
  "model": "qwen3:8b",
  "context": {"site": "rack-a"}
}
```

Worker 自动加入资产 DMI/型号/metadata、目标传感器、阈值、最新值和最多 20 个相邻通道。结果进入 Pending；`/llm/suggestion-jobs` 查看 job，`/llm/suggestions` 查看结果。Rejected 结果会阻止同一传感器被反复建议。

Accepted 只更新 `display_alias` 与受限类别。原始名称、原始单位、source path、scale、硬件/用户阈值始终保留。

## 诊断

手动诊断：

```json
POST /api/v1/diagnoses
{
  "incidentId": "<uuid>",
  "provider": "openai-compatible",
  "model": "your-model",
  "context": {"operatorNote": "fan replaced yesterday"}
}
```

Worker 组装 Incident、最多 100 个关联告警、目标资产及父子拓扑、最多 40 个高风险/最近传感器和 24 小时趋势。完整序列化证据包用于近似 token 预算；超限 job 标记 `budget_exceeded`，不会截断后悄悄提交。

模型必须返回摘要、假设、证据、数据缺口、建议检查、操作建议、回滚说明与置信度。模型输出不会改变规则，也不会直接创建可执行任务。

## 可选自动诊断

自动诊断默认关闭。规则 expression 可显式加入：

```json
{
  "operator": ">",
  "value": 80,
  "autoDiagnosis": {
    "enabled": true,
    "minSeverity": "critical",
    "provider": "ollama",
    "model": "qwen3:8b",
    "cooldownSeconds": 3600,
    "tokenBudget": 6000
  }
}
```

只有确定性规则进入 Firing 后才排队；`minSeverity`、同一 Incident/provider/model 的 cooldown 和较小的规则级 token budget 均会生效。LLM 从不参与告警是否触发的判断。
