// AgentPurpose — "what this agent is for", from the app's build description.
// Lives at the top of the agent detail, under the header.
//
// It used to append the workflows taught to the app in its own chat ("from N
// conversations"). Apps no longer author tools — that capability was removed
// with the Tools tab — so the line is just the agent's stated purpose again.

interface AgentPurposeProps {
  /** The build-time description of the agent (may be empty). */
  summary?: string;
}

function sentenceCase(s: string): string {
  const t = s.trim().replace(/\.+$/, "");
  return t ? t[0].toUpperCase() + t.slice(1) : t;
}

export function AgentPurpose({ summary }: AgentPurposeProps) {
  const base = summary?.trim() ? sentenceCase(summary) : null;
  if (!base) return null;

  return (
    <div className="opr-agent-purpose">
      <p className="opr-agent-purpose-text">{base}.</p>
    </div>
  );
}
