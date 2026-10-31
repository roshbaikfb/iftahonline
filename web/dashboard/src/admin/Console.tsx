import { useCallback, useEffect, useState } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router';

import { api } from '../api/client';
import { Login } from './Login';
import { Overview } from './Overview';
import { AdminSessionContext, fetchAdmin, type Admin, type AdminState } from './session';
import { StorePage } from './StorePage';

// ConsoleRoutes are the console's screens once an admin is signed in.
export function ConsoleRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Overview />} />
      <Route path="/stores/:storeId" element={<StorePage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

// Gate shows the login to visitors and the console to admins.
export function Gate({ state }: { state: AdminState }) {
  switch (state.kind) {
    case 'loading':
      return <p role="status">جارٍ التحميل…</p>;
    case 'error':
      return <p role="alert">{state.message}</p>;
    case 'signed-out':
      return <Login />;
    case 'signed-in':
      return <ConsoleRoutes />;
  }
}

// Console is the super-admin SPA (7f-3b, the approved mockups), served on
// the console's host under /console.
export function Console() {
  const [state, setState] = useState<AdminState>({ kind: 'loading' });

  useEffect(() => {
    let live = true;
    void fetchAdmin().then((s) => {
      if (live) setState(s);
    });
    return () => {
      live = false;
    };
  }, []);

  const signedIn = useCallback((admin: Admin) => {
    setState({ kind: 'signed-in', admin });
  }, []);
  const signOut = useCallback(async () => {
    await api.POST('/admin/v1/auth/logout');
    setState({ kind: 'signed-out' });
  }, []);

  return (
    <AdminSessionContext.Provider value={{ state, signedIn, signOut }}>
      <BrowserRouter basename="/console">
        <Gate state={state} />
      </BrowserRouter>
    </AdminSessionContext.Provider>
  );
}
