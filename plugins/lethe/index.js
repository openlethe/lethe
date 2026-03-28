import { definePluginEntry, emptyPluginConfigSchema } from "openclaw/plugin-sdk/plugin-entry";
import { LetheContextEngine } from "./context-engine.js";
import { LetheTools } from "./tools.js";
export default definePluginEntry({
    id: "lethe",
    name: "Lethe",
    description: "Persistent memory layer for AI agents — the antidote to the river Lethe.",
    configSchema: {
        ...emptyPluginConfigSchema(),
        jsonSchema: {
            type: "object",
            properties: {
                endpoint: {
                    type: "string",
                    description: "Lethe server endpoint (e.g. http://localhost:8080/api)",
                    default: "http://localhost:8080",
                },
                apiKey: {
                    type: "string",
                    description: "API key for Lethe server authentication (optional)",
                },
                agentId: {
                    type: "string",
                    description: "Agent identifier used when creating sessions",
                    default: "default",
                },
                projectId: {
                    type: "string",
                    description: "Project identifier for grouping sessions",
                    default: "default",
                },
            },
            required: [],
        },
    },
    register(api) {
        const cfg = (api.pluginConfig ?? {});
        const endpoint = cfg.endpoint ?? "http://localhost:8080/api";
        const apiKey = cfg.apiKey ?? "";
        const agentId = cfg.agentId ?? "default";
        const projectId = cfg.projectId ?? "default";
        // Register the context engine (owns session context: bootstrap, assemble,
        // afterTurn, compact). Lethe owns compaction so ownsCompaction = true.
        api.registerContextEngine("lethe", () => new LetheContextEngine({ endpoint, apiKey, agentId, projectId }));
        // Register memory tools: memory.record, memory.log, memory.flag, memory.task
        const tools = new LetheTools({ endpoint, apiKey, agentId, projectId });
        api.registerTool(() => tools.getRecordTool());
        api.registerTool(() => tools.getLogTool());
        api.registerTool(() => tools.getFlagTool());
        api.registerTool(() => tools.getTaskTool());
    },
});
