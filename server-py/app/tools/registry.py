import json
from .base import BaseTool, ToolResult, ExecuteContext


class Registry:
    def __init__(self):
        self._tools: dict[str, BaseTool] = {}

    def register(self, tool: BaseTool):
        self._tools[tool.name] = tool

    def generate_prompt(self) -> str:
        """生成 LLM 工具定义 + 输出格式约定"""
        parts = ["\n\n===== 工具使用 =====\n"]
        parts.append("你可以使用以下工具来完成用户的请求。\n")
        parts.append("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

        for tool in self._tools.values():
            parts.append(f"## 工具: {tool.name}\n")
            parts.append(f"描述: {tool.description}\n\n")
            parts.append("函数:\n")
            for fn in tool.functions():
                parts.append(f"  - {fn['name']}: {fn['description']}\n")
                props = fn.get("parameters", {}).get("properties", {})
                required = fn.get("parameters", {}).get("required", [])
                for key, prop in props.items():
                    typ = prop.get("type", "string")
                    desc = prop.get("description", "")
                    req = " (必填)" if key in required else ""
                    parts.append(f"    - {key} ({typ}{req}): {desc}\n")
                    if "enum" in prop:
                        parts.append(f"      可选值: {'/'.join(str(v) for v in prop['enum'])}\n")
                parts.append("\n")

        parts.append("===== 输出格式 =====\n\n")
        parts.append("需要调用工具时，回复一个 JSON 对象：\n\n")
        parts.append('{"reply":"给用户的回复","tool_calls":[{"tool":"<工具名>","function":"<函数名>","args":{...}}]}\n\n')
        parts.append("如果不需要使用工具（纯聊天），直接回复纯文本即可。\n")
        parts.append("一条回复中 tool_calls 可以包含多个工具调用，按顺序执行。\n")
        parts.append("工具执行结果会追加到对话中，你可以据此继续回复用户。\n\n")

        parts.append("===== 重要规则 =====\n\n")
        parts.append("1. 带枚举值的字段必须严格从可选值中选择，不得自创分类！\n")
        parts.append("2. 如果用户描述模糊无法确定分类，必须用纯文本回复询问用户，不要猜测！\n")
        parts.append("3. 只有用户明确或根据上下文99%确定时，才调用工具。\n")

        return "".join(parts)

    async def execute(self, ctx: ExecuteContext, tool_name: str, func_name: str, args: dict) -> ToolResult:
        tool = self._tools.get(tool_name)
        if tool is None:
            return ToolResult(success=False, error=f"unknown tool: {tool_name}")
        return await tool.execute(ctx, func_name, args)
