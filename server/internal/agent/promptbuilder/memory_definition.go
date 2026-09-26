package promptbuilder

// 记忆（user_memories）定义的唯一来源。
//
// 链路上有三处 prompt 需要知道「记忆是什么」，它们全部引用/拼接本常量，不得各写一份：
//   - 抽取段（post_chat）：agent/extractor.go 的 buildMemoryInstructionXML，按此定义从对话里捞候选
//   - 决策段（两入口）：agent/record_loop_types.go 的 system prompt，按此定义判断这条候选该怎么入库
//   - 主循环准则 4：static.go 的 corePrinciples，按此定义判断何时触发 record_memory
//
// 改定义只动这里。历史上三处各写一份，出现过漂移（抽取段说「最多 3 条」、主循环只说
// 「反思、决定、经验、观点」，决策段干脆没有）——那正是本常量要解决的问题。
const MemoryDefinition = `记忆是从对话里沉淀下来的、将来聊天还会用到的用户长期信息，包括：用户的决定与理由、
结论与洞察、观点与判断、偏好与习惯、状态与计划、人物关系、经验教训，以及用户分享的外部信息。

一条记忆 = 一件事（一个 topic + 一句 summary + 若干 facts 细节），不是一段对话的摘要。
topic 是一个主题词：非空、≤5 字、只含中英文数字、一个词，禁止斜杠/逗号/多主题。
逐日流水（睡眠打卡、体重、饮酒、运动、时间日志等按天读数）不属于记忆，属于文档台账。`
