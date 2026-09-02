/**
 * useBoxSignin — the "Sign in to ascii.dev" flow, shared by the onboarding
 * computer step, the Settings keys section, and the Computer tab.
 *
 * Plain effects, no react-query: the onboarding wizard mounts without a
 * QueryClientProvider, and a 1.5 s poll needs nothing more. The broker owns
 * the state machine (broker_box_signin.go); this hook mirrors it, opens the
 * sign-in tab exactly once per flow, and refreshes the account view when the
 * flow lands on done or after a sign-out.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import {
  type BoxAccount,
  type BoxSigninState,
  getBoxAccount,
  getBoxSigninStatus,
  signOutBox,
  startBoxSignin,
} from "../api/computer";

export type BoxSigninPhase =
  | "idle"
  | "installing"
  | "cli_missing"
  | "awaiting_login"
  | "provisioning"
  | "signing_out"
  | "error";

export interface BoxSignin {
  phase: BoxSigninPhase;
  authUrl: string;
  installCommand: string;
  error: string;
  account: BoxAccount | null;
  /** True once the account view has been read at least once. */
  accountLoaded: boolean;
  keySet: boolean;
  start: () => void;
  signOut: () => Promise<void>;
  refreshAccount: () => Promise<void>;
}

const POLL_MS = 1500;

export function useBoxSignin(): BoxSignin {
  const [phase, setPhase] = useState<BoxSigninPhase>("idle");
  const [authUrl, setAuthUrl] = useState("");
  const [installCommand, setInstallCommand] = useState("");
  const [error, setError] = useState("");
  const [account, setAccount] = useState<BoxAccount | null>(null);
  const [accountLoaded, setAccountLoaded] = useState(false);
  const openedRef = useRef(false);
  const aliveRef = useRef(true);

  const refreshAccount = useCallback(async () => {
    try {
      const view = await getBoxAccount(true);
      if (aliveRef.current) setAccount(view);
    } catch {
      // The broker may be unreachable for a moment; keep the last view.
    } finally {
      if (aliveRef.current) setAccountLoaded(true);
    }
  }, []);

  useEffect(() => {
    aliveRef.current = true;
    getBoxAccount()
      .then((view) => {
        if (aliveRef.current) setAccount(view);
      })
      .catch(() => undefined)
      .finally(() => {
        if (aliveRef.current) setAccountLoaded(true);
      });
    return () => {
      aliveRef.current = false;
    };
  }, []);

  const apply = useCallback(
    (state: BoxSigninState) => {
      switch (state.status) {
        case "installing":
          setPhase("installing");
          break;
        case "cli_missing":
          setPhase("cli_missing");
          setInstallCommand(state.installCommand);
          setError(state.reason);
          break;
        case "awaiting_login":
          setPhase("awaiting_login");
          setAuthUrl(state.authUrl);
          if (state.authUrl && !openedRef.current) {
            openedRef.current = true;
            window.open(state.authUrl, "_blank", "noopener");
          }
          break;
        case "provisioning":
          setPhase("provisioning");
          break;
        case "done":
          setPhase("idle");
          void refreshAccount();
          break;
        case "error":
          setPhase("error");
          setError(
            state.reason || "Sign-in failed. Try again, or paste a key.",
          );
          break;
        default:
          break;
      }
    },
    [refreshAccount],
  );

  const start = useCallback(() => {
    openedRef.current = false;
    setError("");
    setPhase("installing");
    startBoxSignin()
      .then(apply)
      .catch((err: unknown) => {
        setPhase("error");
        setError(
          err instanceof Error ? err.message : "Could not start sign-in.",
        );
      });
  }, [apply]);

  const signOut = useCallback(async () => {
    setError("");
    setPhase("signing_out");
    try {
      const result = await signOutBox();
      if (aliveRef.current) {
        setAccount(result.account);
        setPhase("idle");
      }
    } catch (err: unknown) {
      if (aliveRef.current) {
        setPhase("error");
        setError(err instanceof Error ? err.message : "Could not sign out.");
      }
    }
  }, []);

  const polling =
    phase === "installing" ||
    phase === "awaiting_login" ||
    phase === "provisioning";
  useEffect(() => {
    if (!polling) return;
    let cancelled = false;
    const timer = setInterval(() => {
      getBoxSigninStatus()
        .then((state) => {
          if (!cancelled) apply(state);
        })
        .catch(() => undefined);
    }, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [polling, apply]);

  return {
    phase,
    authUrl,
    installCommand,
    error,
    account,
    accountLoaded,
    keySet: account?.keySet === true,
    start,
    signOut,
    refreshAccount,
  };
}
