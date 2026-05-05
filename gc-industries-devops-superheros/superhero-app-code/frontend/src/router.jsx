import { createBrowserRouter } from "react-router-dom";
import HomePage from "./pages/HomePage";
import CatalogPage from "./pages/CatalogPage";
import OrderSuccessPage from "./pages/OrderSuccessPage";
import PaymentPage from "./pages/PaymentPage";


const router = createBrowserRouter([
  { path: "/", element: <HomePage /> },
  { path: "/catalog", element: <CatalogPage /> },
  { path: "/order-success", element: <OrderSuccessPage /> },
  { path: "/payment", element: <PaymentPage /> },
]);

export default router;
