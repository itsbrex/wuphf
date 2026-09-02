/**
 * StepComputer — wizard step "Give your bots a computer."
 *
 * Two honest paths, neither gating the wizard:
 *  - Local VM: free, on this machine, needs a container runtime. The step
 *    reads GET /computer/runtime and says exactly which of the three states
 *    the machine is in (ready, installed but stopped, nothing installed).
 *  - Cloud: an ascii.dev Box with the user's own key. The key form saves
 *    through the existing /config path, which checks the key with ascii.dev
 *    before persisting it, so a bad paste is refused here rather than
 *    surfacing as a 401 in a bot's Computer tab later.
 *
 * The stage visual is a real capture of the Computer tab while the Chief of
 * Staff opened example.com on its own desktop.
 */

import { useCallback, useEffect, useState } from "react";

import { ApiError, getConfig, updateConfig } from "../../../../api/client";
import {
  type ComputerRuntime,
  getComputerRuntime,
} from "../../../../api/computer";
import {
  ONBOARDING_COMPUTER_COPY as COPY,
  ONBOARDING_WIZARD_COPY,
  type OnboardingWizardStepProps,
} from "../wizardSteps";

const STEP_COPY = ONBOARDING_WIZARD_COPY.computer;

type LocalState = "loading" | "ready" | "stopped" | "missing";

function localStateOf(runtime: ComputerRuntime | null): LocalState {
  if (!runtime) return "loading";
  if (runtime.daemonUp) return "ready";
  if (runtime.available) return "stopped";
  return "missing";
}

export interface ComputerChoiceViewProps {
  local: LocalState;
  installHint: string;
  keySet: boolean;
  keyValue: string;
  onKeyChange: (value: string) => void;
  onSaveKey: () => void;
  saving: boolean;
  saveError: string | null;
}

/** Pure presentational surface; the container below feeds it. */
export function ComputerChoiceView({
  local,
  installHint,
  keySet,
  keyValue,
  onKeyChange,
  onSaveKey,
  saving,
  saveError,
}: ComputerChoiceViewProps) {
  const canSave = keyValue.trim().length > 0 && !saving;
  const onSubmit = (event: React.FormEvent) => {
    event.preventDefault();
    if (canSave) onSaveKey();
  };
  return (
    <section
      className="onboarding-computer"
      aria-labelledby="onboarding-computer-local-heading"
      data-testid="onboarding-computer"
    >
      <div className="onboarding-computer-path" data-state={local}>
        <h3
          id="onboarding-computer-local-heading"
          className="onboarding-embedding-heading"
        >
          {COPY.localHeading}
        </h3>
        <p
          className="onboarding-computer-status"
          data-testid="onboarding-computer-local"
        >
          {local === "loading" && "Checking this machine…"}
          {local === "ready" && COPY.localReady}
          {local === "stopped" && COPY.localStopped}
          {local === "missing" && COPY.localMissing}
        </p>
        {local === "missing" ? (
          <p className="onboarding-computer-hint">
            <a
              href={COPY.localInstallURL}
              target="_blank"
              rel="noreferrer"
              className="onboarding-computer-link"
            >
              {COPY.localInstallLabel}
            </a>
            {installHint ? <span> {installHint}</span> : null}
          </p>
        ) : null}
      </div>

      <div
        className="onboarding-computer-path"
        data-state={keySet ? "ready" : "open"}
      >
        <h3 className="onboarding-embedding-heading">{COPY.cloudHeading}</h3>
        {keySet ? (
          <p
            className="onboarding-embedding-success"
            data-testid="onboarding-computer-key-set"
          >
            {COPY.keySet}
          </p>
        ) : (
          <>
            <p className="onboarding-embedding-note">{COPY.cloudNote}</p>
            <form className="onboarding-embedding-key" onSubmit={onSubmit}>
              <label
                className="onboarding-team-label"
                htmlFor="onboarding-computer-box-key"
              >
                {COPY.keyLabel}
              </label>
              <div className="onboarding-embedding-key-row">
                <input
                  id="onboarding-computer-box-key"
                  className="onboarding-team-input"
                  type="password"
                  value={keyValue}
                  placeholder={COPY.keyPlaceholder}
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(event) => onKeyChange(event.target.value)}
                  data-testid="onboarding-computer-box-key"
                />
                <button
                  type="submit"
                  className="btn btn-primary onboarding-embedding-save"
                  disabled={!canSave}
                  data-testid="onboarding-computer-save"
                >
                  {saving ? COPY.savingKey : COPY.saveKey}
                </button>
              </div>
              <p className="onboarding-embedding-hint">{COPY.keyHint}</p>
              {saveError ? (
                <p
                  className="onboarding-embedding-error"
                  role="alert"
                  data-testid="onboarding-computer-error"
                >
                  {saveError}
                </p>
              ) : null}
            </form>
            <div className="onboarding-computer-howto">
              <p className="onboarding-computer-howto-heading">
                {COPY.howToHeading}
              </p>
              <ol className="onboarding-computer-steps">
                {COPY.howTo.map((step) => (
                  <li key={step}>{step}</li>
                ))}
              </ol>
              <p className="onboarding-computer-hint">
                <a
                  href={COPY.signupURL}
                  target="_blank"
                  rel="noreferrer"
                  className="onboarding-computer-link"
                >
                  {COPY.signupLabel}
                </a>
                <span> · </span>
                <a
                  href={COPY.docsURL}
                  target="_blank"
                  rel="noreferrer"
                  className="onboarding-computer-link"
                  data-testid="onboarding-computer-docs"
                >
                  {COPY.docsLabel}
                </a>
              </p>
            </div>
          </>
        )}
      </div>
      <p className="onboarding-computer-skip">{COPY.skipHint}</p>
    </section>
  );
}

/** Container: reads the runtime and the key flag, saves the key. */
export function ComputerChoice() {
  const [runtime, setRuntime] = useState<ComputerRuntime | null>(null);
  const [keySet, setKeySet] = useState(false);
  const [keyValue, setKeyValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    getComputerRuntime()
      .then((value) => {
        if (!cancelled) setRuntime(value);
      })
      .catch(() => {
        if (!cancelled)
          setRuntime({
            available: false,
            runtime: "",
            daemonUp: false,
            image: false,
            imageRef: "",
            driverVersion: "",
            building: false,
            installHint: "",
            runtimeStartHint: "",
            problem: "",
          });
      });
    getConfig()
      .then((cfg) => {
        if (!cancelled) setKeySet(Boolean(cfg.box_key_set));
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  const onSaveKey = useCallback(async () => {
    const token = keyValue.trim();
    if (!token) return;
    setSaving(true);
    setSaveError(null);
    try {
      await updateConfig({ box_api_key: token });
      setKeySet(true);
      setKeyValue("");
    } catch (error) {
      setSaveError(
        error instanceof ApiError
          ? error.message
          : "We could not save that key. Check it and try again, or add it later in Settings.",
      );
    } finally {
      setSaving(false);
    }
  }, [keyValue]);

  return (
    <ComputerChoiceView
      local={localStateOf(runtime)}
      installHint={runtime?.installHint ?? ""}
      keySet={keySet}
      keyValue={keyValue}
      onKeyChange={setKeyValue}
      onSaveKey={onSaveKey}
      saving={saving}
      saveError={saveError}
    />
  );
}

export function StepComputer({ active }: OnboardingWizardStepProps) {
  return (
    <div
      className="office-tour-slide office-tour-slide-computer"
      data-active={active}
      data-testid="onboarding-step-computer"
    >
      <div className="office-tour-slide-copy">
        <p className="office-tour-slide-eyebrow">{STEP_COPY.eyebrow}</p>
        <h2 className="office-tour-slide-headline office-tour-slide-headline--serif">
          {STEP_COPY.headline}
        </h2>
        <p className="office-tour-slide-body">{STEP_COPY.body}</p>

        <ComputerChoice />
      </div>

      <div className="office-tour-slide-stage office-tour-slide-stage--computer">
        <picture>
          <source
            srcSet="/media/onboarding/bot-computer-still.png"
            media="(prefers-reduced-motion: reduce)"
          />
          <img
            className="onboarding-wizard-clip"
            src="/media/onboarding/bot-computer.gif"
            width={640}
            height={402}
            alt="The Computer tab of a gawkbot: its own Linux desktop, where Firefox opens and loads example.com while the bot works."
            loading="lazy"
            decoding="async"
          />
        </picture>
      </div>
    </div>
  );
}
