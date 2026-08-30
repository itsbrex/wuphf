/**
 * Rotating loading verbs, in the spirit of the Claude Code spinner's gerunds
 * ("Cogitating…", "Percolating…"). Kept short so they fit a typing bubble.
 *
 * These used to be The Office references — "Schruting", "Channeling Prison
 * Mike", "Rehearsing the Dunder Mifflin jingle". That gag belonged to a
 * product named after Ryan Howard's failed startup and does not survive the
 * rename: gawkbot is not a paper company and has no Scranton branch, so the
 * jokes were left pointing at nothing.
 *
 * The replacement leans on the one thing the product actually does and is
 * named for. Watching IS the job, so the loading state can say so plainly.
 * The voice is placid, literal, and faintly unhelpful — understatement rather
 * than punchlines.
 *
 * House style: no contractions, no em-dashes. They cycle decoratively, so they
 * are aria-hidden behind a stable status label.
 *
 * The export name keeps "office" because it is an identifier, not copy, and
 * renaming it would churn every import for no reader's benefit.
 */
export const OFFICE_LOADING_PHRASES = [
  "Gawking",
  "Staring",
  "Observing",
  "Looking over your shoulder",
  "Watching the screen",
  "Taking it all in",
  "Noticing things",
  "Keeping an eye on it",
  "Following along",
  "Reading the room",
  "Squinting",
  "Peering",
  "Looking closer",
  "Making a note of that",
  "Not blinking",
  "Still watching",
  "Waiting to be asked",
  "Forming an opinion",
  "Having a look",
  "Seeing what happens",
] as const;
