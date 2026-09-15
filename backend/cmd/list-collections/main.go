package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	token := os.Getenv("ASTRA_DB_TOKEN")
	if token == "" {
		return fmt.Errorf("ASTRA_DB_TOKEN is required")
	}

	targets := []struct {
		dbID     string
		keyspace string
	}{
		{"38ed3530-bbdb-4f20-bb15-5828c4301f7d", "bbi_rag"},
		{"38ed3530-bbdb-4f20-bb15-5828c4301f7d", "default_keyspace"},
		{"83009fa4-ef08-4a98-846f-1d6ee11aada1", "cqa_bbi"},
		{"83009fa4-ef08-4a98-846f-1d6ee11aada1", "default_keyspace"},
	}

	var failures []error
	for _, target := range targets {
		fmt.Printf("DB: %s, Keyspace: %s\n", target.dbID, target.keyspace)
		url := fmt.Sprintf("https://%s-us-east-2.apps.astra.datastax.com/api/json/v1/%s", target.dbID, target.keyspace)

		payload := map[string]interface{}{
			"listCollections": map[string]interface{}{},
		}

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s/%s marshal request: %w", target.dbID, target.keyspace, err))
			continue
		}

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s/%s create request: %w", target.dbID, target.keyspace, err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Token", token)

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s/%s send request: %w", target.dbID, target.keyspace, err))
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s/%s read response: %w", target.dbID, target.keyspace, err))
			continue
		}
		if closeErr != nil {
			failures = append(failures, fmt.Errorf("%s/%s close response: %w", target.dbID, target.keyspace, closeErr))
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			failures = append(failures, fmt.Errorf("%s/%s request failed with status %d: %s", target.dbID, target.keyspace, resp.StatusCode, string(respBody)))
			continue
		}

		fmt.Printf("  Status: %d\n  Response: %s\n", resp.StatusCode, string(respBody))
		fmt.Println("----------------------------------------")
	}

	return errors.Join(failures...)
}
