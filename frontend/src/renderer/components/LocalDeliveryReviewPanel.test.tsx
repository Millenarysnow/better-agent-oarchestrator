import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { components } from "../../api/schema";
import type { WorkspaceSession } from "../types/workspace";
import { LocalDeliveryReviewPanel } from "./LocalDeliveryReviewPanel";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: get, POST: post, PUT: put }) }));

const session: WorkspaceSession = { id: "worker-1", workspaceId: "p1", workspaceName: "Local", title: "Task", provider: "codex", kind: "worker", status: "idle", updatedAt: "2026-11-17T00:00:00Z", prs: [] };
type Local = components["schemas"]["LocalReviewSummary"];
const delivery: Local = { delivery: { id: "report-1", sessionId: session.id, baseSha: "a".repeat(40), targetSha: "b".repeat(40), targetBranch: "main", createdAt: session.updatedAt }, reworkCount: 0, stale: false };

function show(local: Local | null) {
	get.mockImplementation(async (path: string) => ({ data: path.endsWith("/local") ? { localReview: local } : { reviews: [], runs: [], activeReviewers: [] } }));
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return { ...render(<QueryClientProvider client={client}><LocalDeliveryReviewPanel session={session} /></QueryClientProvider>), client };
}

describe("local delivery review", () => {
	beforeEach(() => { vi.clearAllMocks(); post.mockResolvedValue({ data: {} }); put.mockResolvedValue({ data: {} }); });

	it("starts a local review without a PR and keeps acceptance explicit", async () => {
		show(delivery);
		await userEvent.click(await screen.findByRole("button", { name: "Run local review" }));
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/trigger", expect.objectContaining({ body: { local: true, rerun: false } })));
		expect(screen.getByRole("button", { name: "Accept delivery" })).toBeDisabled();
	});

	it("accepts only the displayed approved delivery", async () => {
		show({ ...delivery, status: "complete", verdict: "approved" });
		await userEvent.click(await screen.findByRole("button", { name: "Accept delivery" }));
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/local/decision", expect.objectContaining({ body: { deliveryId: "report-1", decision: "accepted", note: "" } })));
	});

	it("prevents stale acceptance and explains the one round rework limit", async () => {
		show({ ...delivery, stale: true, reworkCount: 1, status: "complete", verdict: "approved" });
		expect(await screen.findByText(/no longer matches the worker/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Accept delivery" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Review again" })).toBeDisabled();
		expect(screen.getByText(/Further blockers need a user decision/)).toBeInTheDocument();
	});

	it("requires a reason for blocking rework", async () => {
		show({ ...delivery, status: "complete", verdict: "changes_requested" });
		const button = await screen.findByRole("button", { name: "Return blocking work" });
		expect(button).toBeDisabled();
		await userEvent.type(screen.getByRole("textbox"), "Fix the failing acceptance criterion");
		await userEvent.click(button);
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/local/decision", expect.objectContaining({ body: { deliveryId: "report-1", decision: "changes_requested", note: "Fix the failing acceptance criterion" } })));
	});

	it("refreshes the local verdict when review change events invalidate the shared review key", async () => {
		const { client } = show({ ...delivery, status: "running" });
		expect(await screen.findByRole("status")).toHaveTextContent("Reviewing");
		get.mockImplementation(async (path: string) => ({ data: path.endsWith("/local") ? { localReview: { ...delivery, status: "complete", verdict: "approved" } } : { reviews: [], runs: [] } }));
		await act(() => client.invalidateQueries({ queryKey: ["session-reviews", session.id] }));
		expect(await screen.findByRole("button", { name: "Accept delivery" })).toBeEnabled();
	});
});
