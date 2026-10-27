import { useEffect, useState } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router';

import { api } from './api/client';

import { Carriers } from './pages/Carriers';
import { COD } from './pages/COD';
import { Gateways } from './pages/Gateways';
import { Login } from './pages/Login';
import { OrderDetail } from './pages/OrderDetail';
import { Orders } from './pages/Orders';
import { Payments } from './pages/Payments';
import { Setup } from './pages/Setup';
import { ShipmentDetail } from './pages/ShipmentDetail';
import { Shipments } from './pages/Shipments';
import { Shipping } from './pages/Shipping';
import { Signup } from './pages/Signup';
import { useSession } from './session';
import { SessionProvider } from './SessionProvider';

// Home opens the first store: its setup wizard while that is open
// (ADR-0034), its orders otherwise.
function Home() {
  const { state } = useSession();
  const first = state.kind === 'signed-in' ? state.me.memberships[0] : undefined;
  const [to, setTo] = useState('');
  useEffect(() => {
    if (!first) return;
    let live = true;
    const id = first.store_id;
    void api.GET('/v1/stores/{store_id}/onboarding', { params: { path: { store_id: id } } }).then(({ data }) => {
      if (live) setTo(data?.status === 'in_progress' ? `/stores/${id}/setup` : `/stores/${id}/orders`);
    });
    return () => {
      live = false;
    };
  }, [first]);
  if (state.kind !== 'signed-in') return null;
  if (!first) return <p className="empty">لست عضوًا في أي متجر بعد.</p>;
  if (!to) return <p role="status">جارٍ التحميل…</p>;
  return <Navigate to={to} replace />;
}

// Routes is the dashboard once the session is known: the login page for
// visitors, the store's pages for members.
function Gate() {
  const { state } = useSession();
  switch (state.kind) {
    case 'loading':
      return <p role="status">جارٍ التحميل…</p>;
    case 'error':
      return <p role="alert">{state.message}</p>;
    case 'signed-out':
      return (
        <Routes>
          <Route path="/signup" element={<Signup />} />
          <Route path="*" element={<Login />} />
        </Routes>
      );
    case 'signed-in':
      return (
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/stores/:storeId/setup" element={<Setup />} />
          <Route path="/stores/:storeId/orders" element={<Orders />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<OrderDetail />} />
          <Route path="/stores/:storeId/shipments" element={<Shipments />} />
          <Route path="/stores/:storeId/shipments/:shipmentId" element={<ShipmentDetail />} />
          <Route path="/stores/:storeId/shipping" element={<Shipping />} />
          <Route path="/stores/:storeId/carriers" element={<Carriers />} />
          <Route path="/stores/:storeId/cod" element={<COD />} />
          <Route path="/stores/:storeId/payments" element={<Payments />} />
          <Route path="/stores/:storeId/gateways" element={<Gateways />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      );
  }
}

export function App() {
  return (
    <SessionProvider>
      <BrowserRouter>
        <Gate />
      </BrowserRouter>
    </SessionProvider>
  );
}
