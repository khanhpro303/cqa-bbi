package db

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

// GetCustomerCodes connects to the external PostgreSQL database, queries the ma_khach_hang from CloudifyCustomer table, and returns them.
func GetCustomerCodes(postgresURL string) ([]string, error) {
	if postgresURL == "" {
		return nil, fmt.Errorf("postgres url is empty")
	}

	db, err := sql.Open("postgres", postgresURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// Query ma_khach_hang
	// Standard PostgreSQL table names are case-sensitive if created with quotes. We try case-sensitive first.
	rows, err := db.Query(`SELECT ma_khach_hang FROM "CloudifyCustomer" WHERE ma_khach_hang IS NOT NULL AND ma_khach_hang != '' ORDER BY ma_khach_hang ASC`)
	if err != nil {
		// Fallback: Try without quotes in case it is case-insensitive or all lowercase
		rows, err = db.Query(`SELECT ma_khach_hang FROM CloudifyCustomer WHERE ma_khach_hang IS NOT NULL AND ma_khach_hang != '' ORDER BY ma_khach_hang ASC`)
		if err != nil {
			return nil, fmt.Errorf("query cloudify customer: %w", err)
		}
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
