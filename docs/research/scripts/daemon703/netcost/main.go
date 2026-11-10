//go:build research

// Command netcost splits the cost of one PostgreSQL call over the embedded tailnet into its parts (#703): the
// connection (helper dial, startup, SCRAM), one round trip, the change aggregate a daemon would poll, and the full
// load of the active facts that a memory-only daemon pays on every start.
//
//	MEMCUE_POSTGRES_DSN=... MEMCUE_TAILSCALE_MODE=embedded ... go run -tags research ./docs/research/scripts/daemon703/netcost
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tailnet"
)

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func median(ds []time.Duration) float64 {
	slices.Sort(ds)
	return ms(ds[len(ds)/2])
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg, err := store.PostgresConfig(os.Getenv("MEMCUE_POSTGRES_DSN"))
	if err != nil {
		fail(err)
	}
	if err := tailnet.Configure(ctx, cfg); err != nil {
		fail(err)
	}
	report := map[string]any{}
	var connects []time.Duration
	for range 5 {
		start := time.Now()
		c, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			fail(err)
		}
		connects = append(connects, time.Since(start))
		_ = c.Close(ctx)
	}
	report["connect_ms_median"] = median(connects)

	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		fail(err)
	}
	defer func() { _ = c.Close(ctx) }()
	var rtts []time.Duration
	for range 20 {
		start := time.Now()
		if _, err := c.Exec(ctx, "SELECT 1"); err != nil {
			fail(err)
		}
		rtts = append(rtts, time.Since(start))
	}
	report["select1_ms_median"] = median(rtts)
	report["select1_ms_min"] = ms(slices.Min(rtts))

	var aggs []time.Duration
	var count, sum int64
	for range 10 {
		start := time.Now()
		if err := c.QueryRow(ctx, "SELECT count(*), coalesce(sum(version), 0) FROM memcue.facts").Scan(&count, &sum); err != nil {
			fail(err)
		}
		aggs = append(aggs, time.Since(start))
	}
	report["change_aggregate_ms_median"] = median(aggs)
	report["facts"] = count

	var loads []time.Duration
	var bytes int
	for range 3 {
		start := time.Now()
		rows, err := c.Query(ctx, "SELECT version, payload FROM memcue.facts WHERE active ORDER BY id")
		if err != nil {
			fail(err)
		}
		n := 0
		bytes = 0
		for rows.Next() {
			var v int
			var payload []byte
			if err := rows.Scan(&v, &payload); err != nil {
				fail(err)
			}
			bytes += len(payload)
			n++
		}
		if rows.Err() != nil {
			fail(rows.Err())
		}
		loads = append(loads, time.Since(start))
		report["active"] = n
	}
	report["full_load_ms_median"] = median(loads)
	report["full_load_payload_bytes"] = bytes
	out, _ := json.Marshal(report)
	fmt.Println(string(out))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "netcost:", err)
	os.Exit(1)
}
