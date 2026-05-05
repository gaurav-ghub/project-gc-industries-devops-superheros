import React, { useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

export default function PaymentPage() {
  const location = useLocation();
  const navigate = useNavigate();
  const order = location.state?.order;
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);

  if (!order) {
    return (
      <div className="payment-wrapper">
        <div className="payment-card">
          <div className="payment-icon">💳</div>
          <div className="payment-title">Please Complete Your Payment</div>
          <div className="payment-label">No order selected.</div>
          <button className="payment-btn" onClick={() => navigate("/catalog")}>Go to Catalog</button>
        </div>
      </div>
    );
  }

  async function handlePayment() {
    setLoading(true);
    setResult(null);
    try {
      const res = await fetch("/api/pay", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          orderId: order.id,
          amount: order.price
        })
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || "Payment failed");
      }
      const data = await res.json();
      setResult({ ok: true, message: data.message });
      setTimeout(() => navigate("/order-success"), 900);
    } catch (err) {
      setResult({ ok: false, message: err.message });
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="payment-wrapper">
      <div className="payment-card">
        <div className="payment-icon">💳</div>
        <div className="payment-title">Please Complete Your Payment</div>
        <div className="payment-label">Product:</div>
        <div className="payment-value"><b>{order.name}</b></div>
        <div className="payment-label">Amount:</div>
        <div className="payment-amount">${order.price.toLocaleString()}</div>
        <button className="payment-btn" onClick={handlePayment} disabled={loading}>
          {loading ? "Processing..." : "Pay Now"}
        </button>
        {result && (
          <div className={result.ok ? "payment-result ok" : "payment-result err"}>
            {result.message}
          </div>
        )}
      </div>
    </div>
  );
}
