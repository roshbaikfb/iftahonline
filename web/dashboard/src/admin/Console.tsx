import { useCallback, useEffect, useState } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router';

import { api } from '../api/client';
import { AdsPage } from './AdsPage';
import { AgenciesPage } from './AgenciesPage';
import { AgencyPage } from './AgencyPage';
import { BillingPage } from './BillingPage';
import { Need } from './Can';
import { FoundingPage } from './FoundingPage';
import { GuideEditor, GuidesPage } from './GuidesPage';
import { InvoicePage } from './InvoicePage';
import { AuditPage } from './AuditPage';
import { Login } from './Login';
import { Overview } from './Overview';
import { PlanEditorPage } from './PlanEditor';
import { PlansPage } from './PlansPage';
import { SitePage } from './SitePage';
import { AdminSessionContext, fetchAdmin, type Admin, type AdminState } from './session';
import { StorePage } from './StorePage';
import { StoresPage } from './StoresPage';

// ConsoleRoutes are the console's screens once an admin is signed in.
export function ConsoleRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Overview />} />
      <Route path="/stores" element={<Need p="stores.read"><StoresPage /></Need>} />
      <Route path="/stores/:storeId" element={<Need p="stores.read"><StorePage /></Need>} />
      <Route path="/billing" element={<Need p="billing.read"><BillingPage /></Need>} />
      <Route path="/billing/invoices/:invoiceId" element={<Need p="billing.read"><InvoicePage /></Need>} />
      <Route path="/plans" element={<Need p="catalog.read"><PlansPage /></Need>} />
      <Route path="/plans/:code" element={<Need p="catalog.read"><PlanEditorPage /></Need>} />
      <Route path="/agencies" element={<Need p="catalog.read"><AgenciesPage /></Need>} />
      <Route path="/agencies/:agencyId" element={<Need p="catalog.read"><AgencyPage /></Need>} />
      <Route path="/ads" element={<Need p="content.read"><AdsPage /></Need>} />
      <Route path="/site" element={<Need p="content.read"><SitePage /></Need>} />
      <Route path="/founding" element={<Need p="catalog.read"><FoundingPage /></Need>} />
      <Route path="/guides" element={<Need p="content.read"><GuidesPage /></Need>} />
      <Route path="/guides/:id" element={<Need p="content.read"><GuideEditor /></Need>} />
      <Route path="/audit" element={<Need p="audit.read"><AuditPage /></Need>} />
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
