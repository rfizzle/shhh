# Capabilities

What shhh can do, and why each thing exists. One document per capability;
none of them names a Go symbol (see [the rules](../README.md#the-four-rules)).

| Document | What it covers |
|---|---|
| [`generation.md`](generation.md) | Turning a sentence into a command, and everything around that decision |
| [`chat.md`](chat.md) | The conversation: answers, reads, delegates to read-only personas, changes nothing |
| [`coding-agent.md`](coding-agent.md) | The agent that edits, runs and checks its own work |
| [`subagents.md`](subagents.md) | Handing part of a job to a child agent |
| [`headless.md`](headless.md) | Running it from a script: the exit codes, the event stream, and what a run with nobody watching does not get |
| [`skills.md`](skills.md) | Folders of instructions for one kind of task, loaded when a task matches |
| [`mcp.md`](mcp.md) | Tools from outside: MCP servers the person defined, what they may do, and who may start one |
| [`todo.md`](todo.md) | The project's backlog: items as files, what is ready, what is archived |
| [`secrets.md`](secrets.md) | Values commands can use and the model never sees |
| [`approvals-and-safety.md`](approvals-and-safety.md) | Deciding whether something runs, with the facts in hand |
| [`containment.md`](containment.md) | What an approved command can actually reach |
| [`providers.md`](providers.md) | The LLM backends, gateways, and how failures are classified |
| [`sessions-and-memory.md`](sessions-and-memory.md) | History, resumable sessions, durable memory, metrics |
| [`reports.md`](reports.md) | Answers that are pages: local graphical report views, stored and served |
| [`evidence.md`](evidence.md) | Reducing bulky tool output, and keeping the original retrievable |
| [`configuration.md`](configuration.md) | Settings, resolution order, and diagnostics |
