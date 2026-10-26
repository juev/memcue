//go:build research

// Command dreamtime sends one dream packet to a model the way memcue dream -run does and says what the call took
// (#471): the packet is a file -prepare printed, the answer goes to a file -apply reads, and one JSON line with the
// wall time, the API time, the time to the first token, the tokens and the cost goes to stdout. A saved packet can be
// sent again on another model or alongside other packets. Experiment only, not part of memcue.
//
// usage: go run -tags research ./docs/research/scripts/dreamtime [-model M] PACKET.md ANSWER.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/juev/memcue/internal/extract"
)

type row struct {
	Packet  string         `json:"packet"`
	Model   string         `json:"model"`
	Bytes   int            `json:"bytes"`  // size of the packet
	Answer  int            `json:"answer"` // size of the answer
	WallMS  int64          `json:"wall_ms"`
	APIMS   int64          `json:"api_ms"`
	TTFTMS  int64          `json:"ttft_ms"`
	Tokens  extract.Tokens `json:"tokens"`
	CostUSD float64        `json:"cost_usd"`
	Error   string         `json:"error,omitempty"`
}

func main() {
	model := flag.String("model", "claude-opus-5-5", "model of the claude -p call")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: dreamtime [-model M] PACKET.md ANSWER.json")
		os.Exit(2)
	}
	packet, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	extract.Model = *model
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	start := time.Now()
	r, err := extract.DreamAnswer(ctx, string(packet))
	out := row{Packet: flag.Arg(0), Model: *model, Bytes: len(packet), Answer: len(r.Answer),
		WallMS: time.Since(start).Milliseconds(), APIMS: r.API.Milliseconds(), TTFTMS: r.TTFT.Milliseconds(),
		Tokens: r.Tokens, CostUSD: r.CostUSD}
	if err != nil {
		out.Error = err.Error()
	} else if err := os.WriteFile(flag.Arg(1), r.Answer, 0o600); err != nil {
		out.Error = err.Error()
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	if out.Error != "" {
		os.Exit(1)
	}
}
