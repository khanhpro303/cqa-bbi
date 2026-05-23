package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
)

// GetCustomerCodes connects to the external PostgreSQL database, queries the ma_khach_hang from CloudifyCustomer table, and returns them.
func GetCustomerCodes(postgresURL string) ([]string, error) {
	if postgresURL == "" {
		return nil, fmt.Errorf("postgres url is empty")
	}

	// Auto-append sslmode=disable if not already present
	if !strings.Contains(postgresURL, "sslmode=") {
		if strings.Contains(postgresURL, "?") {
			postgresURL += "&sslmode=disable"
		} else {
			postgresURL += "?sslmode=disable"
		}
	}

	db, err := sql.Open("postgres", postgresURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// Query ma_khach_hang from the correct schema and table: cloudify.cloudify_customers
	rows, err := db.Query(`SELECT ma_khach_hang FROM cloudify.cloudify_customers WHERE ma_khach_hang IS NOT NULL AND ma_khach_hang != '' ORDER BY ma_khach_hang ASC`)
	if err != nil {
		return nil, fmt.Errorf("query cloudify customers: %w", err)
	}
	defer rows.Close()

	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err == nil {
			codes = append(codes, code)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	return codes, nil
}
