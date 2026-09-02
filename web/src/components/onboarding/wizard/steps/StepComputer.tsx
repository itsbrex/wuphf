/**
 * StepComputer — wizard step "Give your bots a computer."
 *
 * Two honest paths, neither gating the wizard:
 *  - Local VM: free, on this machine, needs a container runtime. The step
 *    reads GET /computer/runtime and says exactly which of the three states
 *    the machine is in (ready, installed but stopped, nothing installed).
 *  - Cloud: an ascii.dev Box on the person's own account. The primary path
 *    is "Sign in to ascii.dev": the broker installs the Box CLI, opens the
 *    browser sign-in, mints a key named gawkbot, verifies it, and stores it,
 *    so nothing is copied. Pasting a key stays available as the fallback and
 *    goes through the same verified /config path.
 *
 * Plain effects, no react-query: the wizard host mounts without a
 * QueryClientProvider in tests, and a 1.5 s poll needs nothing more.
 *
 * The stage visual is a real capture of the Computer tab while the Chief of
 * Staff opened example.com on its own desktop.
 */

import { useCallback, useEffect, useState } from "react";

import { ApiError, updateConfig } from "../../../../api/client";
import {
  type BoxAccount,
  type ComputerRuntime,
  getComputerRuntime,
} from "../../../../api/computer";
import { useBoxSignin } from "../../../../hooks/useBoxSignin";
import { BoxPlanNotice, boxAccountLine } from "../../../computer/BoxPlanNotice";
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

export type SigninPhase =
  | "idle"
  | "installing"
  | "cli_missing"
  | "awaiting_login"
  | "provisioning"
  | "signing_out"
  | "error";

export interface ComputerChoiceViewProps {
  local: LocalState;
  installHint: string;
  onCheckAgain: () => void;
  keySet: boolean;
  account: BoxAccount | null;
  onSignOut: () => void;
  signin: SigninPhase;
  authUrl: string;
  installCommand: string;
  signinError: string;
  onStartSignin: () => void;
  onCancelSignin: () => void;
  showPaste: boolean;
  onShowPaste: () => void;
  keyValue: string;
  onKeyChange: (value: string) => void;
  onSaveKey: () => void;
  saving: boolean;
  saveError: string | null;
}

interface SigninStatusProps {
  phase: SigninPhase;
  authUrl: string;
  installCommand: string;
  error: string;
  onCancel: () => void;
}

function SigninStatus({
  phase,
  authUrl,
  installCommand,
  error,
  onCancel,
}: SigninStatusProps) {
  switch (phase) {
    case "installing":
      return (
        <p
          className="onboarding-computer-status"
          data-testid="onboarding-computer-signin-status"
        >
          {COPY.signinInstalling}
        </p>
      );
    case "awaiting_login":
      return (
        <p
          className="onboarding-computer-status"
          data-testid="onboarding-computer-signin-status"
        >
          {COPY.signinAwaiting}{" "}
          {authUrl ? (
            <a
              href={authUrl}
              target="_blank"
              rel="noreferrer"
              className="onboarding-computer-link"
              data-testid="onboarding-computer-signin-link"
            >
              {COPY.signinOpenAgain}
            </a>
          ) : null}
          {" · "}
          <button
            type="button"
            className="btn btn-text btn-sm"
            onClick={onCancel}
            data-testid="onboarding-computer-signin-cancel"
          >
            {COPY.signinStartOver}
          </button>
        </p>
      );
    case "provisioning":
      return (
        <p
          className="onboarding-computer-status"
          data-testid="onboarding-computer-signin-status"
        >
          {COPY.signinProvisioning}
        </p>
      );
    case "cli_missing":
      return (
        <div
          className="onboarding-computer-status"
          data-testid="onboarding-computer-signin-status"
        >
          <p className="onboarding-embedding-error" role="alert">
            {error || COPY.signinCliMissing}
          </p>
          <code className="onboarding-embedding-code">{installCommand}</code>
        </div>
      );
    case "error":
      return (
        <p
          className="onboarding-embedding-error"
          role="alert"
          data-testid="onboarding-computer-signin-status"
        >
          {error}
        </p>
      );
    default:
      return null;
  }
}

type CloudPathProps = Pick<
  ComputerChoiceViewProps,
  | "keySet"
  | "account"
  | "onSignOut"
  | "signin"
  | "authUrl"
  | "installCommand"
  | "signinError"
  | "onStartSignin"
  | "onCancelSignin"
  | "showPaste"
  | "onShowPaste"
  | "keyValue"
  | "onKeyChange"
  | "saving"
  | "saveError"
> & { canSave: boolean; onSubmit: (event: React.FormEvent) => void };

/** The cloud half: sign-in first, paste as the fallback. */
function CloudPath({
  keySet,
  account,
  onSignOut,
  signin,
  authUrl,
  installCommand,
  signinError,
  onStartSignin,
  onCancelSignin,
  showPaste,
  onShowPaste,
  keyValue,
  onKeyChange,
  saving,
  saveError,
  canSave,
  onSubmit,
}: CloudPathProps) {
  const signinBusy =
    signin === "installing" ||
    signin === "awaiting_login" ||
    signin === "provisioning";
  return (
    <>
      {keySet ? (
        <div className="box-account-row">
          <p
            className="onboarding-embedding-success"
            data-testid="onboarding-computer-key-set"
          >
            {COPY.keySet}
          </p>
          {boxAccountLine(account) ? (
            <p
              className="box-account-line"
              data-testid="onboarding-computer-account"
            >
              {boxAccountLine(account)}
            </p>
          ) : null}
          <BoxPlanNotice account={account} compact={true} />
          <button
            type="button"
            className="onboarding-embedding-expand"
            onClick={onSignOut}
            disabled={signin === "signing_out"}
            data-testid="onboarding-computer-signout"
          >
            {signin === "signing_out" ? COPY.signingOut : COPY.signOut}
          </button>
        </div>
      ) : (
        <>
          <p className="onboarding-embedding-note">{COPY.cloudNote}</p>
          <div className="onboarding-computer-signin">
            <button
              type="button"
              className="btn btn-primary"
              onClick={onStartSignin}
              disabled={signinBusy}
              data-testid="onboarding-computer-signin"
            >
              {COPY.signinCta}
            </button>
            <SigninStatus
              phase={signin}
              authUrl={authUrl}
              installCommand={installCommand}
              error={signinError}
              onCancel={onCancelSignin}
            />
          </div>
          {showPaste ? (
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
            </form>
          ) : (
            <button
              type="button"
              className="onboarding-embedding-expand"
              onClick={onShowPaste}
              data-testid="onboarding-computer-paste"
            >
              {COPY.signinPasteInstead}
            </button>
          )}
        </>
      )}
    </>
  );
}
/** Pure presentational surface; the container below feeds it. */
export function ComputerChoiceView({
  local,
  installHint,
  onCheckAgain,
  keySet,
  account,
  onSignOut,
  signin,
  authUrl,
  installCommand,
  signinError,
  onStartSignin,
  onCancelSignin,
  showPaste,
  onShowPaste,
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
        {local === "missing" || local === "stopped" ? (
          <div className="box-account-actions">
            {local === "missing" ? (
              <>
                <a
                  href={COPY.localInstallURL}
                  target="_blank"
                  rel="noreferrer"
                  className="btn btn-primary btn-sm"
                >
                  {COPY.localInstallLabel}
                </a>
                <a
                  href={COPY.localDockerURL}
                  target="_blank"
                  rel="noreferrer"
                  className="btn btn-secondary btn-sm"
                  data-testid="onboarding-computer-docker"
                >
                  {COPY.localDockerLabel}
                </a>
              </>
            ) : null}
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={onCheckAgain}
              data-testid="onboarding-computer-check"
            >
              {COPY.localCheckAgain}
            </button>
            {installHint && local === "missing" ? (
              <span className="onboarding-computer-hint">{installHint}</span>
            ) : null}
          </div>
        ) : null}
      </div>

      <div
        className="onboarding-computer-path"
        data-state={keySet ? "ready" : "open"}
      >
        <h3 className="onboarding-embedding-heading">{COPY.cloudHeading}</h3>
        <CloudPath
          keySet={keySet}
          account={account}
          onSignOut={onSignOut}
          signin={signin}
          authUrl={authUrl}
          installCommand={installCommand}
          signinError={signinError}
          onStartSignin={onStartSignin}
          onCancelSignin={onCancelSignin}
          showPaste={showPaste}
          onShowPaste={onShowPaste}
          keyValue={keyValue}
          onKeyChange={onKeyChange}
          saving={saving}
          saveError={saveError}
          canSave={canSave}
          onSubmit={onSubmit}
        />
      </div>
      <p className="onboarding-computer-skip">{COPY.skipHint}</p>
    </section>
  );
}

const EMPTY_RUNTIME: ComputerRuntime = {
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
};

/** Container: reads the runtime, runs sign-in through the shared hook, saves a pasted key. */
export function ComputerChoice() {
  const [runtime, setRuntime] = useState<ComputerRuntime | null>(null);
  const [showPaste, setShowPaste] = useState(false);
  const [keyValue, setKeyValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const signin = useBoxSignin();

  const [runtimeCheck, setRuntimeCheck] = useState(0);
  useEffect(() => {
    let cancelled = false;
    getComputerRuntime()
      .then((value) => {
        if (!cancelled) setRuntime(value);
      })
      .catch(() => {
        if (!cancelled) setRuntime(EMPTY_RUNTIME);
      });
    return () => {
      cancelled = true;
    };
  }, [runtimeCheck]);

  const onSaveKey = useCallback(async () => {
    const token = keyValue.trim();
    if (!token) return;
    setSaving(true);
    setSaveError(null);
    try {
      await updateConfig({ box_api_key: token });
      setKeyValue("");
      await signin.refreshAccount();
    } catch (error) {
      setSaveError(
        error instanceof ApiError
          ? error.message
          : "We could not save that key. Check it and try again, or add it later in Settings.",
      );
    } finally {
      setSaving(false);
    }
  }, [keyValue, signin.refreshAccount]);

  return (
    <ComputerChoiceView
      local={localStateOf(runtime)}
      installHint={runtime?.installHint ?? ""}
      onCheckAgain={() => {
        setRuntime(null);
        setRuntimeCheck((n) => n + 1);
      }}
      keySet={signin.keySet}
      account={signin.account}
      onSignOut={() => void signin.signOut()}
      signin={signin.phase}
      authUrl={signin.authUrl}
      installCommand={signin.installCommand}
      signinError={signin.error}
      onStartSignin={signin.start}
      onCancelSignin={() => void signin.cancel()}
      showPaste={showPaste}
      onShowPaste={() => setShowPaste(true)}
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
