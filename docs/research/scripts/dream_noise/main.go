// Report the two components of the capture filter on synthetic experiment rows.
package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/vet"
)

func main() {
	var facts []store.Fact
	if err := json.NewDecoder(os.Stdin).Decode(&facts); err != nil {
		log.Fatal(err)
	}
	type checks struct {
		Metadata bool `json:"metadata"`
		Pattern  bool `json:"pattern"`
		Both     bool `json:"both"`
	}
	results := make(map[string]checks, len(facts))
	for _, fact := range facts {
		// A known match lets us measure metadata independently of the fact text.
		metadata := fact
		metadata.Content = "The issue was fixed in main."
		results[fact.ID] = checks{
			Metadata: vet.CaptureNoise(metadata) != "",
			Pattern:  vet.CaptureNoise(store.Fact{Content: fact.Content}) != "",
			Both:     vet.CaptureNoise(fact) != "",
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		log.Fatal(err)
	}
}
