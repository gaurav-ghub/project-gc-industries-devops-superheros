import { Link } from "react-router-dom";

const navStyle = {
  display: "flex",
  justifyContent: "center",
  gap: "20px",
  padding: "20px",
};

const linkStyle = {
  color: "white",
  fontSize: "20px",
  textDecoration: "none",
  background: "#00bcd4",
  padding: "10px 18px",
  borderRadius: "10px",
  fontWeight: "bold",
};

export default function Navbar() {
  return (
    <nav style={navStyle}>
      <Link to="/" style={linkStyle}>🏠 Home</Link>
      <Link to="/catalog" style={linkStyle}>📦 Catalog</Link>
    </nav>
  );
}
