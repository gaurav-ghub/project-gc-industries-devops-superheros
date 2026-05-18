import React from "react";
import { Link, useLocation } from "react-router-dom";
import { useTheme } from "../ThemeContext";

export default function Layout({ children }) {
  const { mode, toggle } = useTheme();
  const loc = useLocation();

  return (
    <div className="app-root">
      <header className="topbar">
        <div className="topbar-inner">
          <div className="brand">GC Industries — <span className="ver">🤖🤖 Shipping SuperHeros 🤖🤖 </span></div>
          {/* NEW SOCIAL ICONS */}
          <div className="social-icons">
      <a
        href="https://github.com/gc-ghub"
        target="_blank"
        rel="noopener noreferrer"
      >
        <img
          src="https://cdn.jsdelivr.net/npm/simple-icons@v9/icons/github.svg"
          alt="GitHub"
        />
      </a>

      <a
        href="https://www.linkedin.com/in/gc-devops-world"
        target="_blank"
        rel="noopener noreferrer"
      >
        <img
          src="https://cdn.jsdelivr.net/npm/simple-icons@v9/icons/linkedin.svg"
          alt="LinkedIn"
        />
      </a>
    </div>
          <nav className="nav">
            <Link to="/" className={`nav-btn ${loc.pathname === '/' ? 'active' : ''}`}> 🛖 Home</Link>
            <Link to="/catalog" className={`nav-btn ${loc.pathname.startsWith('/catalog') ? 'active' : ''}`}> 🛒 Catalog</Link>
            <button className="theme-toggle" onClick={toggle}>
              {mode === "dark" ? "🌙 Dark" : "☀️ Light"}
            </button>
          </nav>
        </div>
      </header>

      <main className="page-container">
        {children}
      </main>

      <footer className="footer">
        <div>SuperHeros tested in fictional worlds. Ready to save the day! </div>
      </footer>
    </div>
  );
}
