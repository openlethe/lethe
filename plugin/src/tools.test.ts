import { describe, it } from "node:test";
import assert from "node:assert";
import { LetheTools } from "./tools.js";

describe("Lethe tool names", () => {
  it("uses names accepted by the Responses API", () => {
    const tools = new LetheTools({
      endpoint: "http://localhost:18483",
      apiKey: "",
      agentId: "test-agent",
      projectId: "test-project",
    });

    const names = [
      tools.getRecordTool().name,
      tools.getLogTool().name,
      tools.getFlagTool().name,
      tools.getTaskTool().name,
      tools.getSearchTool().name,
    ];

    assert.deepStrictEqual(names, [
      "lethe_record",
      "lethe_log",
      "lethe_flag",
      "lethe_task",
      "lethe_search",
    ]);
    for (const name of names) {
      assert.match(name, /^[a-zA-Z0-9_-]+$/);
    }
  });
});
