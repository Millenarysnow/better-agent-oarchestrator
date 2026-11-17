import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { components } from "../../api/schema";
import { clientForSessionHost } from "../lib/host-clients";
import { apiErrorException } from "../lib/api-error-exception";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { sessionReviewsQueryKey, sessionReviewsQueryOptions } from "../lib/session-reviews";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { Switch } from "./ui/switch";

type Surface = NonNullable<components["schemas"]["ListReviewsResponse"]["reviewerSurface"]>;

export function LocalDeliveryReviewPanel({ session, hostId, onOpenReviewer }: {
	session: WorkspaceSession;
	hostId?: string;
	onOpenReviewer?: (surface: Surface) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [note, setNote] = useState("");
		const localKey = ["local-delivery-review", hostId ?? "local", session.id];
	const localQuery = useQuery({
		queryKey: localKey,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/reviews/local", { params: { path: { sessionId: session.id } } });
			if (error) throw apiErrorException(error);
			return data?.localReview ?? null;
		},
		refetchInterval: 5000,
	});
	const local = localQuery.data ?? session.localReview;
 useEffect(() => setNote(""), [session.id, local?.delivery.id]);
	const reviews = useQuery(sessionReviewsQueryOptions(session, Boolean(local), undefined, hostId));
	const running = local?.status === "running";
 const anyRunning = running || (reviews.data?.runs ?? []).some((run) => run.targetKind === "local" && run.status === "running");
	const mutation = useMutation({
		mutationFn: async ({ action, enabled }: { action: "run" | "cancel" | "auto" | "accepted" | "changes_requested" | "needs_input"; enabled?: boolean }) => {
			const id = session.id;
			const client = clientForSessionHost(hostId);
			const params = { path: { sessionId: id } };
			if (action === "run") {
				const { error } = await client.POST("/api/v1/sessions/{sessionId}/reviews/trigger", { params, body: { local: true, rerun: Boolean(local?.status) } });
				if (error) throw apiErrorException(error);
			} else if (action === "cancel") {
				const { error } = await client.POST("/api/v1/sessions/{sessionId}/reviews/cancel", { params });
				if (error) throw apiErrorException(error);
			} else if (action === "auto") {
				const { error } = await client.PUT("/api/v1/sessions/{sessionId}/reviews/local/auto", { params, body: { enabled: enabled ?? false } });
				if (error) throw apiErrorException(error);
			} else {
				const { error } = await client.POST("/api/v1/sessions/{sessionId}/reviews/local/decision", { params, body: { deliveryId: local!.delivery.id, decision: action, note } });
				if (error) throw apiErrorException(error);
			}
			return { id, host: hostId };
		},
		onSuccess: ({ id, host }) => {
			void queryClient.invalidateQueries({ queryKey: ["local-delivery-review", host ?? "local", id] });
			void queryClient.invalidateQueries({ queryKey: sessionReviewsQueryKey(id, host) });
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(host) });
		},
	});
	const disabled = mutation.isPending || session.isTerminated === true;
	const valid = Boolean(local && !local.stale && !local.delivery.inputError);
	const undecided = valid && (!local?.delivery.decision || local.delivery.decision === "needs_input");
	const statusKey = local?.stale ? "localReview.stale" : local?.delivery.decision === "accepted" ? "localReview.accepted" : local?.delivery.decision === "changes_requested" ? "localReview.returned" : local?.delivery.decision === "needs_input" ? "localReview.needs_input" : running ? "localReview.running" : local?.status === "failed" ? "localReview.failed" : local?.status === "cancelled" ? "localReview.cancelled" : local?.verdict === "approved" ? "localReview.approved" : local?.verdict === "changes_requested" ? "localReview.changes_requested" : "localReview.pending";
	const error = mutation.error ?? localQuery.error ?? reviews.error;
	return <section className="flex min-w-0 flex-col gap-3 p-3" aria-label={t("localReview.title")}>
		<h3 className="text-sm font-medium">{t("localReview.title")}</h3>
		<label className="flex items-center justify-between gap-3 text-xs">
			<span>{t("localReview.auto")}</span>
			<Switch aria-label={t("localReview.auto")} checked={session.autoLocalReviewEnabled ?? false} disabled={disabled} onCheckedChange={(enabled) => mutation.mutate({ action: "auto", enabled })} />
		</label>
		{error ? <p role="alert" className="text-xs text-error">{error instanceof Error ? error.message : String(error)}</p> : null}
		{!local ? <p className="text-xs text-muted-foreground">{t("localReview.empty")}</p> : <>
			<p className="text-xs font-medium" role="status">{t(statusKey)}</p>
			<p className="break-all text-xs text-muted-foreground">{t("localReview.scope", { base: local.delivery.baseSha.slice(0, 12), head: local.delivery.targetSha.slice(0, 12) })}</p>
			<p className="break-all text-xs text-muted-foreground">{t("localReview.delivery", { id: local.delivery.id })}</p>
			{local.delivery.inputError ? <p className="text-xs text-error">{local.delivery.inputError}</p> : null}
			{local.delivery.decisionNote ? <p className="whitespace-pre-wrap text-xs">{local.delivery.decisionNote}</p> : null}
			<div className="flex flex-wrap gap-2">
				<Button size="sm" disabled={disabled || !undecided || anyRunning} onClick={() => mutation.mutate({ action: "run" })}>{t(local.status ? "localReview.rerun" : "localReview.run")}</Button>
				{anyRunning ? <Button size="sm" variant="secondary" disabled={disabled} onClick={() => mutation.mutate({ action: "cancel" })}>{t("localReview.cancel")}</Button> : null}
				{reviews.data?.reviewerSurface ? <Button size="sm" variant="secondary" onClick={() => onOpenReviewer?.(reviews.data!.reviewerSurface!)}>{t("localReview.open")}</Button> : null}
			</div>
			{undecided && !running ? <>
				<textarea className="min-h-20 rounded-md border bg-background p-2 text-xs" aria-label={t("localReview.note")} placeholder={t("localReview.note")} value={note} maxLength={800} disabled={disabled} onChange={(event) => setNote(event.target.value)} />
				<div className="flex flex-wrap gap-2">
					<Button size="sm" disabled={disabled || local.status !== "complete" || local.verdict !== "approved"} onClick={() => mutation.mutate({ action: "accepted" })}>{t("localReview.accept")}</Button>
					<Button size="sm" variant="secondary" disabled={disabled || !note.trim() || local.reworkCount >= 1 || local.status !== "complete" || local.verdict !== "changes_requested"} onClick={() => mutation.mutate({ action: "changes_requested" })}>{t("localReview.return")}</Button>
					<Button size="sm" variant="secondary" disabled={disabled || !note.trim()} onClick={() => mutation.mutate({ action: "needs_input" })}>{t("localReview.input")}</Button>
				</div>
			</> : null}
			{local.reworkCount >= 1 ? <p className="text-xs text-muted-foreground">{t("localReview.reworkLimit")}</p> : null}
		</>}
		<p className="text-xs text-muted-foreground">{t("localReview.noMerge")}</p>
		{(reviews.data?.runs ?? []).filter((run) => run.targetKind === "local").map((run) => <details key={run.id} className="rounded-md border p-2 text-xs" open={run.deliveryId === local?.delivery.id}>
			<summary>{run.harness} · {run.targetSha.slice(0, 12)} · {run.verdict || run.status}</summary>
			<div className="prose prose-sm max-w-none break-words"><ReactMarkdown remarkPlugins={[remarkGfm]}>{run.body || t("localReview.running")}</ReactMarkdown></div>
		</details>)}
	</section>;
}
