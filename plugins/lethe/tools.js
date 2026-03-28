import { Type } from "@sinclair/typebox";
function letheHeaders(apiKey) {
    const h = {
        "Content-Type": "application/json",
    };
    if (apiKey)
        h["Authorization"] = `Bearer ${apiKey}`;
    return h;
}
async function lethePost(endpoint, apiKey, path, body) {
    return fetch(`${endpoint}${path}`, {
        method: "POST",
        headers: letheHeaders(apiKey),
        body: JSON.stringify(body),
    });
}
// ---------------------------------------------------------------------------
// Tool schemas (Typebox)
// ---------------------------------------------------------------------------
// memory.record — deliberate decision with reasoning
const RecordParams = Type.Object({
    content: Type.String({
        description: "The decision or reasoning to record. Be specific: include the what, why, and any constraints considered.",
    }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
    tags: Type.Optional(Type.Array(Type.String(), {
        description: "Optional tags to organize this record (e.g. ['architecture', 'decision'])",
    })),
});
// memory.log — ambient observation
const LogParams = Type.Object({
    content: Type.String({
        description: "An observation, event, or note worth remembering.",
    }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
    tags: Type.Optional(Type.Array(Type.String())),
});
// memory.flag — agent self-reported uncertainty
const FlagParams = Type.Object({
    content: Type.String({
        description: "What the agent is uncertain about — a hypothesis, a guess, or a known gap. Automatically creates a thread to track this uncertainty across sessions.",
    }),
    confidence: Type.Number({
        description: "Confidence score from 0.0 (complete guess) to 1.0 (certain).",
        minimum: 0,
        maximum: 1,
    }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
});
// memory.task — track a task through status transitions
const TaskParams = Type.Object({
    title: Type.String({
        description: "Short, descriptive title for the task.",
    }),
    status: Type.Union([
        Type.Literal("todo"),
        Type.Literal("in_progress"),
        Type.Literal("done"),
        Type.Literal("blocked"),
    ], { description: "Current status of the task." }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
    parentEventId: Type.Optional(Type.String({
        description: "Link this status change to a prior task event.",
    })),
});
// ---------------------------------------------------------------------------
// LetheTools
// ---------------------------------------------------------------------------
export class LetheTools {
    cfg;
    constructor(cfg) {
        this.cfg = cfg;
    }
    // ---------------------------------------------------------------------------
    // Tool factories
    // ---------------------------------------------------------------------------
    getRecordTool() {
        return this.makeTool({
            name: "memory.record",
            description: "Record a deliberate decision the agent has made, including the reasoning behind it. Use this for architecture choices, trade-off resolutions, and any conclusions reached during the session.",
            params: RecordParams,
            label: "Record Decision",
            execute: async (toolCallId, params) => {
                const { content, sessionKey, tags } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "record",
                    content,
                    tags: tags ?? [],
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                return {
                    content: [
                        {
                            type: "text",
                            text: `Recorded: ${content}${tags?.length ? ` [${tags.join(", ")}]` : ""}`,
                        },
                    ],
                    details: { ok: true, event_id: data.event_id },
                };
            },
        });
    }
    getLogTool() {
        return this.makeTool({
            name: "memory.log",
            description: "Log an ambient observation, event, or note. Lower stakes than record — use this to track what's happening without requiring structured reasoning.",
            params: LogParams,
            label: "Log Observation",
            execute: async (toolCallId, params) => {
                const { content, sessionKey, tags } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "log",
                    content,
                    tags: tags ?? [],
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                return {
                    content: [{ type: "text", text: `Logged: ${content}` }],
                    details: { ok: true, event_id: data.event_id },
                };
            },
        });
    }
    getFlagTool() {
        return this.makeTool({
            name: "memory.flag",
            description: "Flag a knowledge gap, uncertainty, or educated guess. The confidence score surfaces this for human review. Use when you know you're working with incomplete information.",
            params: FlagParams,
            label: "Flag Uncertainty",
            execute: async (toolCallId, params) => {
                const { content, confidence, sessionKey } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "flag",
                    content,
                    confidence,
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                const threadNote = data.thread_auto_created
                    ? ` [thread created: ${data.thread_id}]`
                    : "";
                return {
                    content: [
                        {
                            type: "text",
                            text: `Flagged (confidence ${confidence}): ${content}${threadNote}`,
                        },
                    ],
                    details: { ok: true, event_id: data.event_id, thread_id: data.thread_id },
                };
            },
        });
    }
    getTaskTool() {
        return this.makeTool({
            name: "memory.task",
            description: "Track a task through status transitions (todo → in_progress → done | blocked). Each transition is recorded as a separate event with a parent link, building a full audit trail.",
            params: TaskParams,
            label: "Update Task",
            execute: async (toolCallId, params) => {
                const { title, status, sessionKey, parentEventId } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "task",
                    content: title,
                    task_status: status,
                    parent_event_id: parentEventId,
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                return {
                    content: [
                        {
                            type: "text",
                            text: `Task [${status}]: ${title}`,
                        },
                    ],
                    details: { ok: true, event_id: data.event_id, status },
                };
            },
        });
    }
    // ---------------------------------------------------------------------------
    // Tool builder
    // ---------------------------------------------------------------------------
    makeTool({ name, description, params, label, execute, }) {
        return {
            name,
            description,
            parameters: params,
            label,
            execute,
        };
    }
}
