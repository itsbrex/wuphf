import { useOfficeStats } from "../../hooks/useOfficeStats";
import { needsYouCount, officeIsQuiet } from "../../lib/needsYou";

/**
 * Thin strip under the channel header with pills for "N active",
 * "M blocked", "K need you". Mirrors the legacy runtime-strip.
 *
 * "K need you" and the "all quiet" state both come from lib/needsYou, which
 * is the single definition every surface shares. Do not compute either from
 * the stats payload here — that is what let this strip print "all quiet"
 * while the board's Needs-human lane showed 1.
 */
export function RuntimeStrip() {
  const { data: stats } = useOfficeStats();

  // No stats yet (first load or broker unreachable): render nothing
  // rather than claiming "all quiet" about a state we don't know.
  if (!stats) {
    return <div className="runtime-strip" />;
  }

  const active = stats.agents_active;
  const blocked = stats.tasks.blocked;
  const needYou = needsYouCount(stats);

  if (officeIsQuiet(stats)) {
    return (
      <div className="runtime-strip">
        <span
          className="runtime-pill runtime-pill-idle"
          title="Nobody is doing anything. Everybody is watching."
        >
          all quiet
        </span>
      </div>
    );
  }

  return (
    <div className="runtime-strip">
      {needYou > 0 && (
        <span className="runtime-pill runtime-pill-needyou">
          {needYou} need you
        </span>
      )}
      {active > 0 && (
        <span className="runtime-pill runtime-pill-active">
          {active} active
        </span>
      )}
      {blocked > 0 && (
        <span className="runtime-pill runtime-pill-blocked">
          {blocked} blocked
        </span>
      )}
    </div>
  );
}
