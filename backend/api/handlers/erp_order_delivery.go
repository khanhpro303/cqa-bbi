package handlers

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/pkg"
)

type orderAppointmentKey struct {
	order    string
	customer string
}

// addOrderAppointments enriches the authorized orders the chatbot can show.
// ERP statuses and summary counts remain usable when a date is blank or the
// supplementary lookup fails.
func addOrderAppointments(ctx context.Context, client *pkg.CloudifyClient, orders []map[string]interface{}) {
	visible := make(map[orderAppointmentKey][]map[string]interface{}, len(orders))
	var orderCodes, customerCodes []string
	for _, order := range orders {
		code := strings.TrimSpace(getMapString(order, "order_id"))
		customer := strings.TrimSpace(getMapString(order, "customer_code"))
		if code == "" || customer == "" {
			continue
		}
		key := orderAppointmentKey{foldDebtSearch(code), strings.ToUpper(customer)}
		visible[key] = append(visible[key], order)
		orderCodes = append(orderCodes, code)
		customerCodes = append(customerCodes, customer)
	}
	if len(visible) == 0 {
		return
	}

	rows, err := client.SearchOrderAppointments(ctx, orderCodes, customerCodes)
	if err != nil {
		log.Printf("[orders_query] optional Cloudify appointment lookup failed: %v", err)
		return
	}
	dates := make(map[orderAppointmentKey]string, len(rows))
	for _, row := range rows {
		partner, _ := row["KHACH_HANG_ID"].([]any)
		if len(partner) != 2 {
			continue
		}
		label, _ := partner[1].(string)
		key := orderAppointmentKey{
			foldDebtSearch(getMapString(row, "SO_DON_HANG")),
			strings.ToUpper(leadingCustomerCode(label)),
		}
		if len(visible[key]) != 1 {
			continue
		}
		date := getMapString(row, "NGAY_GIAO_HANG")
		if previous, exists := dates[key]; exists && previous != date {
			date = ""
		}
		dates[key] = date
	}
	for key, value := range dates {
		if date, err := time.Parse(time.DateOnly, value); err == nil {
			visible[key][0]["scheduled_delivery_date"] = formatDate(date)
		}
	}
}
