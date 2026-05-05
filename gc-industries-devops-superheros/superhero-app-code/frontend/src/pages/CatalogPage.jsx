import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import ProductCard from "../components/ProductCard";

export default function CatalogPage() {
  const navigate = useNavigate();
  const [products, setProducts] = useState([]);
  const [catalogMeta, setCatalogMeta] = useState({});
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(true);

  function handleOrder(item) {
    navigate("/payment", { state: { order: item } });
  }

  useEffect(() => {
    let retries = 3;

    const fetchCatalog = () => {
      fetch("/api/catalog")
        .then((res) => {
          if (!res.ok) throw new Error("Failed to fetch catalog");
          return res.json();
        })
        .then((data) => {
          setProducts(data.products);
          setCatalogMeta({
            version: data.version,
            description: data.description,
            trafficWeight: data.trafficWeight,
            pod: data.pod,
            namespace: data.namespace,
          });
          setError(null);
          setLoading(false);
        })
        .catch((err) => {
          if (retries > 0) {
            retries--;
            setTimeout(fetchCatalog, 700);
          } else {
            console.error(err);
            setError(err.message);
            setLoading(false);
          }
        });
    };

    fetchCatalog();
  }, []);

  if (loading) return <p style={{ color: "#8fd" }}>Loading catalog…</p>;
  if (error) return <p style={{ color: "red" }}>{error}</p>;

  return (
    <section className="centered-page catalog">

      {/* 🔥 Runtime Metadata Bar */}
      <div className="catalog-runtime-bar">
        {/*<span>📦 <strong>Catalog Runtime</strong></span> */}
        {catalogMeta.description && (
          <span><strong>{catalogMeta.description}</strong></span>
        )}
        {catalogMeta.version && (
          <span>Version: <strong>{catalogMeta.version}</strong></span>
        )}
        {catalogMeta.trafficWeight !== undefined && catalogMeta.trafficWeight !== null && catalogMeta.trafficWeight !== "" && (
          <span>Configured Traffic Weight: <strong>{catalogMeta.trafficWeight}%</strong></span>
        )}
        {catalogMeta.pod && (
          <span>Pod: <strong>{catalogMeta.pod}</strong></span>
        )}
        {catalogMeta.namespace && (
          <span>Namespace: <strong>{catalogMeta.namespace}</strong></span>
        )}
      </div>

      <div className="product-grid cinematic-grid">
        {products.map((p, index) => (
          <ProductCard
            key={p.id}
            product={{ ...p, animDelay: index * 100 }}
            onOrder={handleOrder}
          />
        ))}
      </div>
    </section>
  );
}
