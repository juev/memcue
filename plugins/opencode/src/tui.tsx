/**
 * The TUI part of the memcue plugin for OpenCode 2 (#757). It shows a toast for what `/memcue` answers, for a
 * configuration error, an older OpenCode and a failed binary probe, and keeps a compact memory indicator in the
 * prompt footer. Every decision stays in the server part: this file reads `status` over the plugin RPC and renders
 * it, and never runs memcue itself. A TUI part that fails to load leaves the server part, its tools and the
 * instruction exactly as they are.
 *
 * OpenCode 2.3.0 renders a plugin's toast on its home screen only: the same call made while a session is open
 * returns without an error and draws nothing. Toasts are therefore best-effort, and the transcript message the
 * server part writes plus the indicator carry the same outcome inside a session.
 */
/** @jsxImportSource @opentui/solid */
import { createEffect, createSignal, on } from 'solid-js';
import { Plugin } from '@opencode/plugin/tui';
import { memcueRpc, type NoticeVariant } from './rpc.ts';
import {
  asNotice, asProblem, asStatus, indicatorText, offNotice, problemNotice, TOAST_TITLE,
  type SessionStatus,
} from './status.ts';

/** Long enough to read a toast, short enough not to sit over the TUI. */
const TOAST_MS = 8_000;

/** The indicator before the first answer, and when the server part is not there to answer at all. */
const WAITING = 'memcue …';
const UNAVAILABLE = 'memcue ?';

/** The TUI can start before the server part registers; a few retries cover that start, then the indicator gives up. */
const RETRIES = 5;
const RETRY_MS = 1_000;

export default Plugin.define({
  id: 'memcue',
  setup(ctx) {
    const rpc = ctx.client.rpc(memcueRpc);
    const [views, setViews] = createSignal<Record<string, string>>({});
    // A standing problem is toasted once per session: the indicator keeps showing it, a repeated toast would not.
    const announced = new Set<string>();
    const attempts = new Map<string, number>();

    function setView(sessionID: string, text: string): void {
      setViews(current => ({ ...current, [sessionID]: text }));
    }

    /** Without a session the toast carries no `sessionID`, which is what the host draws on its home screen. */
    function show(options: { message: string; variant: NoticeVariant; sessionID?: string }): void {
      ctx.ui.toast.show({
        title: TOAST_TITLE,
        message: options.message,
        variant: options.variant,
        duration: TOAST_MS,
        sessionID: options.sessionID,
      });
    }

    // A configuration error or an older OpenCode is known before any session exists, and the home screen is where
    // a toast still renders: this is the one place that reason reaches the user without opening the log.
    void (async () => {
      for (let attempt = 0; attempt <= RETRIES; attempt += 1) {
        try {
          const problem = asProblem(await rpc.health({}));
          if (problem) show({ message: offNotice(problem), variant: 'warning' });
          return;
        } catch {
          // The first call can race the server part's registration, as the indicator's first read does.
          await new Promise(resolve => setTimeout(resolve, RETRY_MS));
        }
      }
    })();

    async function refresh(sessionID: string): Promise<void> {
      let status: SessionStatus | undefined;
      try {
        status = asStatus(await rpc.status({ sessionID }));
      } catch {
        // The TUI can start before the server part registers: retry a few times, then say it is unavailable.
        setView(sessionID, UNAVAILABLE);
        const attempt = attempts.get(sessionID) ?? 0;
        if (attempt < RETRIES) {
          attempts.set(sessionID, attempt + 1);
          setTimeout(() => { void refresh(sessionID); }, RETRY_MS);
        }
        return;
      }
      attempts.delete(sessionID);
      if (!status) {
        setView(sessionID, UNAVAILABLE);
        return;
      }
      setView(sessionID, indicatorText(status));
      const notice = problemNotice(status);
      if (notice && !announced.has(sessionID)) {
        announced.add(sessionID);
        show({ sessionID, message: notice, variant: 'warning' });
      }
    }

    const unsubscribe = rpc.events.on('notice', (event) => {
      const notice = asNotice(event.data);
      if (!notice) return;
      show(notice);
      void refresh(notice.sessionID);
    });

    ctx.ui.slot({
      append: 'prompt.footer.status',
      render: input => <Indicator sessionID={input.sessionID} views={views} refresh={refresh} />,
    });

    return () => {
      unsubscribe();
    };
  },
});

function Indicator(props: {
  sessionID?: string;
  views: () => Record<string, string>;
  refresh: (sessionID: string) => Promise<void>;
}) {
  // The server part learns the session's role and directory lazily; the first read is therefore after the mount.
  createEffect(on(() => props.sessionID, (sessionID) => {
    if (sessionID) void props.refresh(sessionID);
  }));
  const text = () => {
    const sessionID = props.sessionID;
    return sessionID ? props.views()[sessionID] ?? WAITING : '';
  };
  return <text>{text()}</text>;
}
