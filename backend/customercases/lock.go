package customercases

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
)

// lockWait bounds how long a report waits for another report of the same
// customer (one decision model call plus a write).
const lockWait = 45 * time.Second

// lockKeys are the locks a report takes: its chat, and its customer code when
// it has one, since candidates are matched on either. Any two reports that
// can see the same candidate cases share at least one key.
func lockKeys(r Report) []string {
	keys := []string{"s|" + r.TenantID + "|" + r.SubjectType + "|" + r.SubjectID}
	if r.CustomerCode != "" {
		keys = append(keys, "c|"+r.TenantID+"|"+r.CustomerCode)
	}
	sort.Strings(keys)
	return keys
}

// lockReport serialises reports that may match the same cases, across every
// app and worker process (MySQL named locks), so two reports arriving
// together cannot both open a case for one problem or overwrite each other's
// images. Without MySQL it falls back to an in-process lock.
func lockReport(ctx context.Context, r Report) (func(), error) {
	keys := lockKeys(r)
	if db.DB != nil && db.DB.Dialector.Name() == "mysql" {
		return mysqlLocks(ctx, keys)
	}
	return localLocks(ctx, keys)
}

func lockName(key string) string {
	sum := sha1.Sum([]byte(key))
	return "cqa_case_" + hex.EncodeToString(sum[:])[:40]
}

func mysqlLocks(ctx context.Context, keys []string) (func(), error) {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return nil, err
	}
	// Named locks belong to a connection: hold one for the whole report.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var held []string
	release := func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, name := range held {
			if _, err := conn.ExecContext(bg, "SELECT RELEASE_LOCK(?)", name); err != nil {
				log.Printf("[cases] release lock: %v", err)
			}
		}
		conn.Close()
	}
	for _, key := range keys {
		name := lockName(key)
		var got sql.NullInt64
		if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", name, int(lockWait.Seconds())).Scan(&got); err != nil {
			release()
			return nil, err
		}
		if !got.Valid || got.Int64 != 1 {
			release()
			return nil, fmt.Errorf("timed out waiting for another report of the same customer")
		}
		held = append(held, name)
	}
	return release, nil
}

var localLockMap sync.Map

func localLocks(ctx context.Context, keys []string) (func(), error) {
	var held []chan struct{}
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			<-held[i]
		}
	}
	for _, key := range keys {
		l, _ := localLockMap.LoadOrStore(key, make(chan struct{}, 1))
		ch := l.(chan struct{})
		select {
		case ch <- struct{}{}:
			held = append(held, ch)
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}

var errNoCase = errors.New("case no longer exists")
