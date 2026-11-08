// messenger-journey-repair-time fixes Messenger referral times before year 2000
// (a Unix time read in the wrong unit). Default is a read-only dry-run; --apply
// writes all changes in one transaction. Output has counts and opaque IDs only.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
)

func main() {
	apply := flag.Bool("apply", false, "write the repair; default is read-only dry-run")
	flag.Parse()
	cfg, err := config.Load()
	if err == nil {
		err = db.Connect(cfg.DSN(), true)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Schema is deployed separately. Never invoke the application's broad AutoMigrate here.
	result, err := messengerjourney.RepairReferralTimes(context.Background(), db.DB, *apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "repair failed; nothing was written:", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
}
