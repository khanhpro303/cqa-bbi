package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dbID := "38ed3530-bbdb-4f20-bb15-5828c4301f7d"
	token := os.Getenv("ASTRA_DB_TOKEN")
	if token == "" {
		return fmt.Errorf("ASTRA_DB_TOKEN is required")
	}

	url := fmt.Sprintf("https://api.astra.datastax.com/v2/databases/%s", dbID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	fmt.Printf("Status: %d\nResponse: %s\n", resp.StatusCode, string(body))
	return nil
}
