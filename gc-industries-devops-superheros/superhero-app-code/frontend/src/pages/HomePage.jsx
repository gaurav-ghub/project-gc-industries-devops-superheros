import React from "react";
import { Link } from "react-router-dom";

export default function HomePage() {
  return (
    <div className="home-wrapper fade-in">

      {/* 3-TILE GRID */}
      <div className="home-grid">

        {/* LEFT TILE – Marvel Group */}
        <div className="home-tile">
          <img 
            src="/images/marvel_group.jpg" 
            alt="Marvel Superheroes" 
            className="home-hero-img"
          />
        </div>

        {/* CENTER TILE – Your Welcome Message */}
        <div className="home-tile home-center-tile">
          <h1>DevOps SuperHero Shop</h1>
          <p className="home-description">
            Shop your SuperHeros today to build a strong DevOps Team.  
            Our SuperHeros are best in class Platform Engineers, SREs, DevOps Engineers, and Cloud Engineers.
            Specialized in building production-grade CI/CD pipelines, reusable Terraform modules, and automated cloud infrastructure using Docker, Kubernetes, Terraform, Ansible, Helm, Istio, Argo CD, GitHub Actions, Jenkins, and Python.
          </p>

          <div className="home-btn-row">
            <Link to="/" className="nav-btn active" style={{minWidth:120, display:'inline-flex', alignItems:'center', justifyContent:'center', gap:6}}>
              <span role="img" aria-label="home">🏠</span> Home
            </Link>
            <Link to="/catalog" className="nav-btn" style={{minWidth:120, display:'inline-flex', alignItems:'center', justifyContent:'center', gap:6}}>
              <span role="img" aria-label="catalog">🛒</span> Catalog
            </Link>
          </div>
        </div>

        {/* RIGHT TILE – DC Group */}
        <div className="home-tile">
          <img 
            src="/images/dc_group.jpg" 
            alt="DC Superheroes"
            className="home-hero-img"
          />
        </div>

      </div>

      {/* Footer */}
      <footer className="footer-text">
        SuperHeros tested in fictional worlds, Ready to do jobs in real world.
      </footer>

    </div>
  );
}
