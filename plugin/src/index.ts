import { definePluginEntry, emptyPluginConfigSchema } from "openclaw/plugin-sdk/plugin-entry";
import type { OpenClawPluginApi, OpenClawPluginDefinition } from "openclaw/plugin-sdk/core";
import { LetheContextEngine } from "./context-engine.js";
import { LetheTools } from "./tools.js";

const plugin: OpenClawPluginDefinition = definePluginEntry({
  id: "mentholmike-lethe",
  name: "Lethe",
  description:
    "Persistent memory layer for AI agents — the antidote to the river Lethe.",
  configSchema: {
    ...emptyPluginConfigSchema(),
    jsonSchema: {
      type: "object",
      properties: {
        endpoint: {
          type: "string",
          description:
            "Lethe server endpoint (e.g. http://localhost:18483)",
          default: "http://localhost:18483",
        },
        apiKey: {
          type: "string",
          description:
            "API key for Lethe server authentication (optional)",
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
        autoLog: {
          type: "boolean",
          description:
            "Enable diagnostic automatic event logs for tool calls/thread markers. Checkpoints are always written. Default: false.",
          default: false,
        },
        memoryGitContext: {
          type: "boolean",
          description:
            "Opt in to the experimental Memory Git context adapter. Charon is the default owner of versioned memory.",
          default: false,
        },
      },
      required: [],
    },
  },
  register(api: OpenClawPluginApi) {
    const cfg = (api.pluginConfig ?? {}) as {
      endpoint?: string;
      apiKey?: string;
      agentId?: string;
      projectId?: string;
      autoLog?: boolean;
      memoryGitContext?: boolean;
    };

    const endpoint = cfg.endpoint ?? "http://localhost:18483";
    const apiKey = cfg.apiKey ?? "";
    const agentId = cfg.agentId ?? "default";
    const projectId = cfg.projectId ?? "default";
    const autoLog = cfg.autoLog ?? false;
    const memoryGitContext = cfg.memoryGitContext ?? false;

    // Register the context engine (owns session context: bootstrap, assemble,
    // afterTurn, compact). Lethe owns compaction so ownsCompaction = true.
    api.registerContextEngine("mentholmike-lethe", () =>
      new LetheContextEngine({
        endpoint,
        apiKey,
        agentId,
        projectId,
        autoLog,
        memoryGitContext,
      })
    );

    // Register memory tools: memory.record, memory.log, memory.flag, memory.task, memory_search
    const tools = new LetheTools({ endpoint, apiKey, agentId, projectId });
    api.registerTool(() => tools.getRecordTool());
    api.registerTool(() => tools.getLogTool());
    api.registerTool(() => tools.getFlagTool());
    api.registerTool(() => tools.getTaskTool());
    api.registerTool(() => tools.getSearchTool());
  },
});

export default plugin;
