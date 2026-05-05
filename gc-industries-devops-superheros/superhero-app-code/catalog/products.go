package main

func getVersionDescription(version string) string {
	switch version {
	case "v1":
		return "Catalog v1️⃣ with no rating."
	case "v2":
		return "Catalog v2️⃣ with ⭐ rating."
	case "v3":
		return "Catalog v3️⃣ with new products and ⭐ rating."
	default:
		return "Unknown catalog version."
	}
}

func getProducts(version string) []Product {

	products := []Product{
		{
			ID:    "1",
			Name:  "Wonder Woman – Does Wonders with Terraform",
			Image: "/images/WonderWoman_Terraform.jpg",
			Price: 5000000,
		},
		{
			ID:    "2",
			Name:  "Doctor Strange – Master of the Mystic Arch Diagrams",
			Image: "/images/DrStrange_Arch.jpg",
			Price: 4500000,
		},
		{
			ID:    "3",
			Name:  "Aqua Man – Docker Containerization Expert",
			Image: "/images/AquaMan_Docker.jpg",
			Price: 3000000,
		},
		{
			ID:    "4",
			Name:  "Thor – Lightning-Fast GitHub Actions Hero",
			Image: "/images/Thor_GitHub_Actions_Hero.jpg",
			Price: 4000000,
		},
		{
			ID:    "5",
			Name:  "Logan – Advanced Kubernetes Superhero with Healing Powers",
			Image: "/images/Logan_K8s.jpg",
			Price: 6000000,
		},
		{
			ID:    "6",
			Name:  "Flash – Lightning-Fast Build and Release Hero",
			Image: "/images/Flash_Build_Release.jpg",
			Price: 2000000,
		},
	}

	// ⭐ Ratings for v2 and v3
	if version == "v2" || version == "v3" {
		ratings := []float32{4.8, 4.5, 4.7, 4.6, 4.9, 4.4}
		for i := range products {
			products[i].Rating = ratings[i]
		}
	}

	// 🦇 v3 changes
	if version == "v3" {
		products = []Product{
			{
				ID:     "1",
				Name:   "Batman – Dark Knight Cloud Cost Optimizer",
				Image:  "/images/Batman_Cost_Optimizer.jpg",
				Price:  5500000,
				Rating: 4.8,
			},
			{
				ID:     "2",
				Name:   "SuperMan – All-in-One DevOps Hero",
				Image:  "/images/SuperMan_All_in_one.jpg",
				Price:  5000000,
				Rating: 4.8,
			},
			{
				ID:     "3",
				Name:   "Iron Man – AI-Powered AWS Cloud Guru",
				Image:  "/images/IronMan_AWS_AI.jpg",
				Price:  2200000,
				Rating: 4.7,
			},
			{
				ID:     "4",
				Name:   "Scarlet Witch - Terraform Enchantress",
				Image:  "/images/Scarlet_Witch_Terraform.jpg",
				Price:  3000000,
				Rating: 4.6,
			},
			{
				ID:     "5",
				Name:   "Black Widow - Expert Project Leader",
				Image:  "/images/BlackWidow_Project_Leader.jpg",
				Price:  1800000,
				Rating: 4.5,
			},
			{
				ID:     "6",
				Name:   "SpiderMan - Youngest yet Talented Ansible Prodigy",
				Image:  "/images/SpiderMan_Ansible.jpg",
				Price:  6500000,
				Rating: 4.9,
			},
		}
	}

	return products
}

// test comment
// another test comment
// more comments
