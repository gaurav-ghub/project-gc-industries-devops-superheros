package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type Product struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Image  string  `json:"image"`
	Price  int     `json:"price"`
	Rating float32 `json:"rating,omitempty"`
}

type CatalogResponse struct {
	Version       string    `json:"version"`
	Description   string    `json:"description"`
	TrafficWeight string    `json:"trafficWeight"`
	Pod           string    `json:"pod"`
	Namespace     string    `json:"namespace"`
	Products      []Product `json:"products"`
}

func catalogHandler(w http.ResponseWriter, r *http.Request) {
	version := os.Getenv("CATALOG_VERSION")
	pod := os.Getenv("POD_NAME")
	namespace := os.Getenv("POD_NAMESPACE")
	weight := os.Getenv("TRAFFIC_WEIGHT")

	products := getProducts(version)
	description := getVersionDescription(version)

	resp := CatalogResponse{
		Version:       version,
		Description:   description,
		TrafficWeight: weight,
		Pod:           pod,
		Namespace:     namespace,
		Products:      products,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func main() {
	http.HandleFunc("/api/catalog", catalogHandler)
	http.HandleFunc("/health", healthHandler)

	log.Println("Catalog service running on :8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}
