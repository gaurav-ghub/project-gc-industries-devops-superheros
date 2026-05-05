import React from "react";
import { Link } from "react-router-dom";

export default function OrderSuccessPage() {
  return (
    <div className="order-wrapper">
      <div className="order-card">
        <div className="order-icon">🎉</div>
        <div className="order-title">Order Successful! Congratulations! </div>
        <div className="order-message">Your SuerHero will be at your doorsteps very soon.</div>
        <Link to="/catalog" className="order-btn">Back to Catalog</Link>
      </div>
    </div>
  );
}
