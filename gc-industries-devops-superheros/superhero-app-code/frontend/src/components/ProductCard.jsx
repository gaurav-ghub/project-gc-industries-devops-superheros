import React from "react";

export default function ProductCard({ product, onOrder }) {
  return (
    <div
      className="product-card"
      style={{ animationDelay: `${product.animDelay || 0}ms` }}
    >
      <div className="product-image-wrap">
        <img
          src={product.image}
          alt={product.name}
          className="product-img"
        />
      </div>

      <h3 className="product-name">{product.name}</h3>

      {/* ⭐ Canary-visible change (ONLY v2 sends rating) */}
      {product.rating && (
        <div className="product-rating">
          ⭐ {product.rating}
        </div>
      )}

      <div className="product-price">
        ${product.price.toLocaleString()}
      </div>

      <button
        className="primary-btn"
        onClick={() => onOrder && onOrder(product)}
        aria-label={`Order ${product.name}`}
      >
        Order Now
      </button>
    </div>
  );
}
