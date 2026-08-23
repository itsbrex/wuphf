// knowledgeClient — reads the app's REAL synthesized knowledge pages from the
// broker. The broker gathers the app's real artifacts (spec, data model, source,
// roster) and synthesizes cited Wikipedia-style pages grounded in them; the tab
// renders them exactly like the mock. Cached server-side after first synthesis.

import { get, getText } from "../../api/client";
import type { KnowledgePage } from "../mock/data";

export interface AppKnowledgeResult {
  pages: KnowledgePage[];
  /** "ai_unavailable" (no provider / nothing to synthesize) or "rate_limited". */
  error?: string;
}

// First open triggers a grounded LLM synthesis of several cited pages. The
// broker bounds one synthesis at 90s server-side (knowledgeSynthTimeout in
// internal/team/broker_apps_knowledge.go) and, because its context derives from
// the HTTP request, cancels synthesis the instant the client disconnects. The
// default 20s GET timeout therefore aborted every first read mid-synthesis — no
// single request ever survived long enough to warm the cache, and the abort
// surfaced as a spurious "provider unreachable" error. A patient window that
// outlasts the server bound (plus headroom for the proxy) lets one read finish
// and cache, so every later read is instant. KnowledgeSurface pairs this with a
// poll so a rare miss still recovers.
export const KNOWLEDGE_SYNTH_TIMEOUT_MS = 120_000;

/**
 * getAppKnowledge fetches the app's cited knowledge pages. First open triggers a
 * grounded synthesis (up to ~90s); it is cached after, so later reads are
 * instant. Pass refresh to force a re-synthesis.
 */
export async function getAppKnowledge(
  appId: string,
  refresh = false,
): Promise<AppKnowledgeResult> {
  const res = await get<{ pages?: KnowledgePage[]; error?: string }>(
    `/apps/${encodeURIComponent(appId)}/knowledge${refresh ? "?refresh=1" : ""}`,
    undefined,
    { timeoutMs: KNOWLEDGE_SYNTH_TIMEOUT_MS },
  );
  return { pages: res.pages ?? [], error: res.error };
}

/**
 * getKnowledgeArtifactHTML fetches a page artifact's HTML through the authed
 * client (an iframe src cannot carry the auth header). The caller renders it in
 * a FULLY sandboxed iframe via srcDoc — never as live DOM; the broker also
 * serves it with `CSP: sandbox` as defense in depth.
 */
export function getKnowledgeArtifactHTML(url: string): Promise<string> {
  return getText(url);
}
