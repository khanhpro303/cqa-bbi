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

type CloudifyCustomerProfile struct {
	CustomerCode string `json:"customer_code"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	Region       string `json:"region"`
	PhoneNumber  string `json:"phone_number"`
}

// GetCloudifyCustomerProfiles connects to the external PostgreSQL database, queries customer profiles, and returns them.
func GetCloudifyCustomerProfiles(postgresURL string) ([]CloudifyCustomerProfile, error) {
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

	rows, err := db.Query(`
		SELECT ma_khach_hang, ten_khach_hang, COALESCE(dia_chi, ''), COALESCE(region, ''), COALESCE(dien_thoai, '') 
		FROM cloudify.cloudify_customers 
		WHERE ma_khach_hang IS NOT NULL AND ma_khach_hang != '' 
		ORDER BY ma_khach_hang ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query cloudify customer profiles: %w", err)
	}
	defer rows.Close()

	var profiles []CloudifyCustomerProfile
	for rows.Next() {
		var p CloudifyCustomerProfile
		if err := rows.Scan(&p.CustomerCode, &p.Name, &p.Address, &p.Region, &p.PhoneNumber); err == nil {
			profiles = append(profiles, p)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	return profiles, nil
}

// GetCustomerRegionMap connects to the external (reference-only) PostgreSQL
// database and returns a map of ma_khach_hang -> region (Miền). It is read-only:
// the daily customer cache job uses it to enrich cached_customers with region.
// This database belongs to another app — we never write to it.
func GetCustomerRegionMap(postgresURL string) (map[string]string, error) {
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

	rows, err := db.Query(`
		SELECT ma_khach_hang, COALESCE(region, '')
		FROM cloudify.cloudify_customers
		WHERE ma_khach_hang IS NOT NULL AND ma_khach_hang != ''
	`)
	if err != nil {
		return nil, fmt.Errorf("query customer regions: %w", err)
	}
	defer rows.Close()

	regionByCode := make(map[string]string)
	for rows.Next() {
		var code, region string
		if err := rows.Scan(&code, &region); err == nil {
			regionByCode[strings.TrimSpace(code)] = region
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	return regionByCode, nil
}

// GetCloudifyCustomerNameByCode connects to the external PostgreSQL database, queries the customer name for a given code, and returns it.
func GetCloudifyCustomerNameByCode(postgresURL string, customerCode string) (string, error) {
	if postgresURL == "" {
		return "", fmt.Errorf("postgres url is empty")
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
		return "", fmt.Errorf("open postgres connection: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return "", fmt.Errorf("ping postgres: %w", err)
	}

	var name string
	err = db.QueryRow(`
		SELECT ten_khach_hang 
		FROM cloudify.cloudify_customers 
		WHERE ma_khach_hang = $1 
		LIMIT 1
	`, customerCode).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("query customer name by code: %w", err)
	}

	return name, nil
}
