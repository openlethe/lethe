import { type ContextEngine, type ContextEngineInfo } from "openclaw/plugin-sdk";
import type { AgentMessage } from "@mariozechner/pi-agent-core";
import type { BootstrapResult, AssembleResult, CompactResult, IngestResult, ContextEngineRuntimeContext } from "./context-engine-types.js";
export interface LetheContextEngineConfig {
    endpoint: string;
    apiKey: string;
    agentId: string;
    projectId: string;
    /**
     * Optional noisy diagnostic logging. When disabled (default), Lethe keeps
     * checkpoints but does not turn every tool call/thread marker into events.
     */
    autoLog?: boolean;
    /**
     * Optional compatibility adapter for Memory Git context. Disabled by
     * default: Charon owns the versioned-memory path.
     */
    memoryGitContext?: boolean;
}
interface AssembleParams {
    sessionId: string;
    sessionKey?: string;
    messages: AgentMessage[];
    tokenBudget?: number;
    prompt?: string;
    /** Hard cap on recent events fetched from Lethe (default 5). */
    hardLimit?: number;
}
interface AfterTurnParams {
    sessionId: string;
    sessionKey?: string;
    sessionFile: string;
    messages: AgentMessage[];
    prePromptMessageCount: number;
    isHeartbeat?: boolean;
    tokenBudget?: number;
    runtimeContext?: ContextEngineRuntimeContext;
}
interface CompactParams {
    sessionId: string;
    sessionKey?: string;
    sessionFile: string;
    tokenBudget?: number;
    force?: boolean;
    currentTokenCount?: number;
    runtimeContext?: ContextEngineRuntimeContext;
}
export declare function letheFetch(endpoint: string, apiKey: string, path: string, body?: unknown, signal?: AbortSignal): Promise<Response>;
export declare class LetheContextEngine implements ContextEngine {
    private cfg;
    readonly info: ContextEngineInfo;
    constructor(cfg: LetheContextEngineConfig);
    bootstrap({ sessionId, sessionKey, }: {
        sessionId: string;
        sessionKey?: string;
    }): Promise<BootstrapResult>;
    ingest({ sessionId, sessionKey, message, isHeartbeat, }: {
        sessionId: string;
        sessionKey?: string;
        message: AgentMessage;
        isHeartbeat?: boolean;
    }): Promise<IngestResult>;
    afterTurn(params: AfterTurnParams): Promise<void>;
    assemble(params: AssembleParams): Promise<AssembleResult>;
    compact(params: CompactParams): Promise<CompactResult>;
    dispose(): Promise<void>;
    private reportAssembly;
}
export {};
