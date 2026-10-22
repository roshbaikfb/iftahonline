import { BrowserRouter, Navigate, Route, Routes } from 'react-router';

import { Login } from './pages/Login';
import { OrderDetail } from './pages/OrderDetail';
import { Orders } from './pages/Orders';
import { useSession } from './session';
import { SessionProvider } from './SessionProvider';

function Home() {
  const { state } = useSession();
  if (state.kind !== 'signed-in') return null;
  const first = state.me.memberships[0];
  if (!first) return <p className="empty">لست عضوًا في أي متجر بعد.</p>;
  return <Navigate to={`/stores/${first.store_id}/orders`} replace />;
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
      return <Login />;
    case 'signed-in':
      return (
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/stores/:storeId/orders" element={<Orders />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<OrderDetail />} />
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
