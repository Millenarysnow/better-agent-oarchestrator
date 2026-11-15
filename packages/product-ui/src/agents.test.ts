import { describe, expect, it } from "vitest";
import { AGENT_OPTIONS, agentLabel, getAgentIdentity } from "./agents";

describe("agent identities", () => {
	it("names the local CodeBuddy CLI separately from WorkBuddy", () => {
		expect(AGENT_OPTIONS.filter((id) => id === "codebuddy")).toHaveLength(1);
		expect(agentLabel("codebuddy")).toBe("CodeBuddy Code");
		expect(getAgentIdentity("codebuddy")).toMatchObject({ id: "codebuddy", initial: "C" });
	});
	it("keeps OpenCode 2 a distinct worker option with its own label", () => {
		expect(AGENT_OPTIONS).toContain("opencode-v2");
		expect(agentLabel("opencode")).toBe("OpenCode");
		expect(agentLabel("opencode-v2")).toBe("OpenCode 2");
		expect(getAgentIdentity("opencode-v2")).toMatchObject({
			id: "opencode-v2",
			label: "OpenCode 2",
			logoKey: "opencode-v2",
		});
	});
});
