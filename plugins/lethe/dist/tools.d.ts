import type { AgentTool } from "@mariozechner/pi-agent-core";
export interface LetheToolsConfig {
    endpoint: string;
    apiKey: string;
    agentId: string;
    projectId: string;
}
declare const RecordParams: import("@sinclair/typebox").TObject<{
    content: import("@sinclair/typebox").TString;
    sessionKey: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TString>;
    tags: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TArray<import("@sinclair/typebox").TString>>;
}>;
declare const LogParams: import("@sinclair/typebox").TObject<{
    content: import("@sinclair/typebox").TString;
    sessionKey: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TString>;
    tags: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TArray<import("@sinclair/typebox").TString>>;
}>;
declare const FlagParams: import("@sinclair/typebox").TObject<{
    content: import("@sinclair/typebox").TString;
    confidence: import("@sinclair/typebox").TNumber;
    sessionKey: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TString>;
}>;
declare const TaskParams: import("@sinclair/typebox").TObject<{
    title: import("@sinclair/typebox").TString;
    status: import("@sinclair/typebox").TUnion<[import("@sinclair/typebox").TLiteral<"todo">, import("@sinclair/typebox").TLiteral<"in_progress">, import("@sinclair/typebox").TLiteral<"done">, import("@sinclair/typebox").TLiteral<"blocked">]>;
    sessionKey: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TString>;
    parentEventId: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TString>;
}>;
declare const SearchParams: import("@sinclair/typebox").TObject<{
    query: import("@sinclair/typebox").TString;
    limit: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TNumber>;
    sessionKey: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TString>;
    eventType: import("@sinclair/typebox").TOptional<import("@sinclair/typebox").TUnion<[import("@sinclair/typebox").TLiteral<"record">, import("@sinclair/typebox").TLiteral<"log">, import("@sinclair/typebox").TLiteral<"flag">, import("@sinclair/typebox").TLiteral<"task">]>>;
}>;
export declare class LetheTools {
    private cfg;
    constructor(cfg: LetheToolsConfig);
    getRecordTool(): AgentTool<typeof RecordParams>;
    getLogTool(): AgentTool<typeof LogParams>;
    getFlagTool(): AgentTool<typeof FlagParams>;
    getTaskTool(): AgentTool<typeof TaskParams>;
    getSearchTool(): AgentTool<typeof SearchParams>;
    private makeTool;
}
export {};
