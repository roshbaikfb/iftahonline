import { useEffect, useState } from 'react';

import { api, type components } from '../api/client';

type VersionInfo = components['schemas']['VersionInfo'];

type State = { kind: 'loading' } | { kind: 'ready'; info: VersionInfo } | { kind: 'error'; message: string };

// BuildInfo shows which API build answers (the P0 smoke check, kept on
// the login page's footer).
export function BuildInfo() {
  const [state, setState] = useState<State>({ kind: 'loading' });

  useEffect(() => {
    const controller = new AbortController();
    api
      .GET('/v1/meta/version', { signal: controller.signal })
      .then(({ data, error }) => {
        if (data) {
          setState({ kind: 'ready', info: data });
        } else {
          setState({ kind: 'error', message: error.detail ?? error.title });
        }
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setState({ kind: 'error', message: err instanceof Error ? err.message : 'request failed' });
        }
      });
    return () => {
      controller.abort();
    };
  }, []);

  return (
    <footer className="build">
      <p lang="en" dir="ltr">
        iftahonline merchant dashboard
      </p>
      {state.kind === 'loading' && <p role="status">جارٍ التحميل…</p>}
      {state.kind === 'error' && <p role="alert">{state.message}</p>}
      {state.kind === 'ready' && (
        <dl data-testid="build-info" lang="en" dir="ltr">
          <dt>API version</dt>
          <dd>{state.info.version}</dd>
          <dt>Go</dt>
          <dd>{state.info.go_version}</dd>
        </dl>
      )}
    </footer>
  );
}
