import { useEffect, useState } from "react";
import { Settings as SettingsIcon, SidebarCollapse } from "iconoir-react";

import { useResizablePane } from "../../hooks/useResizablePane";
import { router } from "../../lib/router";
import { useCurrentApp } from "../../routes/useCurrentRoute";
import { useAppStore } from "../../stores/app";
import { TeamMemberBadge } from "../join/TeamMemberBadge";
import { SidebarPreviewOverlay } from "../onboarding/SidebarPreviewOverlay";
import { AgentList } from "../sidebar/AgentList";
import { AppList } from "../sidebar/AppList";
import { ChannelList } from "../sidebar/ChannelList";
import { SidebarSection } from "../sidebar/SidebarSection";
import { UsagePanel } from "../sidebar/UsagePanel";
import { CollapsedSidebar } from "./CollapsedSidebar";
import { PaneResizeHandle } from "./PaneResizeHandle";

export const SIDEBAR_DEFAULT_WIDTH = 280;
export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_MAX_WIDTH = 420;
export const SIDEBAR_WIDTH_STORAGE_KEY = "wuphf-sidebar-width";
const MOBILE_RAIL_QUERY = "(max-width: 768px)";

function useMobileRail(): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia(MOBILE_RAIL_QUERY).matches;
  });

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const query = window.matchMedia(MOBILE_RAIL_QUERY);
    const onChange = (event: MediaQueryListEvent) => setMatches(event.matches);
    setMatches(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);

  return matches;
}

export function Sidebar() {
  const sidebarCollapsed = useAppStore((s) => s.sidebarCollapsed);
  const toggleSidebarCollapsed = useAppStore((s) => s.toggleSidebarCollapsed);
  const sidebarAgentsOpen = useAppStore((s) => s.sidebarAgentsOpen);
  const sidebarChannelsOpen = useAppStore((s) => s.sidebarChannelsOpen);
  const toggleSidebarAgents = useAppStore((s) => s.toggleSidebarAgents);
  const toggleSidebarChannels = useAppStore((s) => s.toggleSidebarChannels);
  const currentApp = useCurrentApp();
  const mobileRail = useMobileRail();
  const [mobileExpanded, setMobileExpanded] = useState(false);

  useEffect(() => {
    if (!mobileRail) setMobileExpanded(false);
  }, [mobileRail]);

  const effectiveCollapsed = mobileRail ? !mobileExpanded : sidebarCollapsed;
  const collapseSidebar = mobileRail
    ? () => setMobileExpanded(false)
    : toggleSidebarCollapsed;

  const resize = useResizablePane({
    storageKey: SIDEBAR_WIDTH_STORAGE_KEY,
    defaultWidth: SIDEBAR_DEFAULT_WIDTH,
    minWidth: SIDEBAR_MIN_WIDTH,
    maxWidth: SIDEBAR_MAX_WIDTH,
    edge: "right",
  });

  // Collapsed rail keeps its fixed CSS width; only the expanded sidebar
  // honors the user's drag. We hand the dragged width to CSS as a custom
  // property rather than `width:` directly so the mobile media queries
  // (which clamp the sidebar to 240px / full overlay) can still win
  // — inline `width` would beat them with normal cascade rules.
  const asideStyle = (
    effectiveCollapsed
      ? null
      : { "--sidebar-resize-width": `${resize.width}px` }
  ) as React.CSSProperties | null;

  return (
    <aside
      className={`sidebar${effectiveCollapsed ? " sidebar-collapsed" : ""}`}
      style={asideStyle ?? undefined}
    >
      {effectiveCollapsed ? (
        <CollapsedSidebar
          onExpand={mobileRail ? () => setMobileExpanded(true) : undefined}
        />
      ) : (
        <>
          <div className="sidebar-header">
            <button
              type="button"
              className="sidebar-logo"
              onClick={() => router.navigate({ to: "/" })}
              title="Home"
              aria-label="gawkbot — go to home"
            >
              gawkbot
            </button>
            <TeamMemberBadge />
            <div className="sidebar-header-actions">
              <button
                type="button"
                className="sidebar-icon-btn"
                aria-label="Collapse sidebar"
                title="Collapse sidebar"
                onClick={collapseSidebar}
              >
                <SidebarCollapse />
              </button>
              <button
                type="button"
                className={`sidebar-icon-btn${currentApp === "settings" ? " active" : ""}`}
                aria-label="Open settings"
                title="Settings"
                onClick={() =>
                  router.navigate({
                    to: "/apps/$appId",
                    params: { appId: "settings" },
                  })
                }
              >
                <SettingsIcon />
              </button>
            </div>
          </div>

          <div className="sidebar-scroll">
            {/* The agent roster rail — CEO + specialists with avatars, live
                activity pills, and the peek affordance. Clicking a row opens
                that agent's subspace (/agents/$slug), so any agent is reachable
                at any time. Collapsible + persisted via the app store, exactly
                as before the Slack-style sidebar unify (#919). */}
            <SidebarSection
              label="Agents"
              variant="team"
              open={sidebarAgentsOpen}
              onToggle={toggleSidebarAgents}
              data-testid="sidebar-section-agents"
            >
              <AgentList />
            </SidebarSection>

            {/* Channels are first-class again: the office is one room, so
                #general is a place you go, not something you reach through a
                task. Task-scoped channels are gone (a task lives in the channel
                it was created from), so this list is the real conversation
                surface. */}
            <SidebarSection
              label="Channels"
              open={sidebarChannelsOpen}
              onToggle={toggleSidebarChannels}
              data-testid="sidebar-section-channels"
            >
              <ChannelList />
            </SidebarSection>

            {/* The sidebar nav is three labeled groups — Work / Knowledge /
                Config — rendered by AppList. Inbox lives in Work; there is no
                separate flat task list, and "Tasks" in Work opens the task
                surface. */}
            <AppList />

            {/* Phase 2 onboarding preview overlay — shows staged channels/agents
                forming as the user answers CEO questions. Hidden once onboarded. */}
            <SidebarPreviewOverlay />
          </div>
          {/* WorkspaceSummary intentionally not rendered here — the stats
              it shows (agents active, tasks open, tokens) are redundant
              with the Tasks nav and the Usage footer. The component file is
              preserved so it can be re-used inside a future Usage popover
              or Settings surface. */}
          <UsagePanel />
        </>
      )}
      {!(effectiveCollapsed || mobileRail) && (
        <PaneResizeHandle
          edge="right"
          ariaLabel="Resize sidebar"
          onPointerDown={resize.onPointerDown}
          isResizing={resize.isResizing}
          onReset={resize.reset}
          onStepResize={resize.stepResize}
          valueNow={resize.width}
          valueMin={SIDEBAR_MIN_WIDTH}
          valueMax={SIDEBAR_MAX_WIDTH}
        />
      )}
    </aside>
  );
}
