package main

import (
	"fmt"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func main() {
	cfg := models.NewModelsConfig()
	path := models.GetConfigPath()
	fmt.Println("Config path:", path)
	err := cfg.Load(path)
	fmt.Printf("Error during load: %v\n", err)
	fmt.Printf("Loaded models count: %d\n", len(cfg.Models))
}
