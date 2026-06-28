// Command eval measures retrieval quality against a golden query set by calling markdex's
// POST /api/eval (the server runs the searches and scores them — one source of truth).
//
//	go run ./cmd/eval -golden cmd/eval/golden/go-style-guide.json
//	go run ./cmd/eval -seed   cmd/eval/golden/go-style-guide.md   # ingest first, then eval
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type goldenQuery struct {
	Query                   string   `json:"query"`
	RelevantHeadingContains []string `json:"relevant_heading_contains"`
}

type goldenSet struct {
	Collection string        `json:"collection"`
	TopK       int           `json:"top_k"`
	Queries    []goldenQuery `json:"queries"`
}

type evalRequest struct {
	Collection string        `json:"collection"`
	TopK       int           `json:"top_k"`
	Queries    []goldenQuery `json:"queries"`
}

type evalResponse struct {
	TopK    int `json:"top_k"`
	Metrics struct {
		Queries int     `json:"queries"`
		MRR     float64 `json:"mrr"`
		HitAt1  float64 `json:"hit_at_1"`
		HitAt3  float64 `json:"hit_at_3"`
		HitAtK  float64 `json:"hit_at_k"`
	} `json:"metrics"`
	Results []struct {
		Query string `json:"query"`
		Rank  int    `json:"rank"`
	} `json:"results"`
}

func main() {
	addr := flag.String("addr", "http://localhost:4334", "markdex base URL")
	goldenPath := flag.String("golden", "cmd/eval/golden/go-style-guide.json", "golden set JSON")
	topK := flag.Int("k", 0, "override top_k (0 = use the golden set's value)")
	seedFile := flag.String("seed", "", "markdown file to ingest into the collection before evaluating")
	collectionFlag := flag.String("collection", "", "override the golden set's collection")
	flag.Parse()

	set, err := loadGolden(*goldenPath)
	if err != nil {
		log.Fatalf("load golden set: %v", err)
	}
	k := set.TopK
	if *topK > 0 {
		k = *topK
	}
	collection := set.Collection
	if *collectionFlag != "" {
		collection = *collectionFlag
	}

	client := &http.Client{Timeout: 5 * time.Minute}

	if *seedFile != "" {
		if err := seed(client, *addr, collection, *seedFile); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	var report evalResponse
	if err := postJSON(client, *addr+"/api/eval",
		evalRequest{Collection: collection, TopK: k, Queries: set.Queries}, &report); err != nil {
		log.Fatalf("eval: %v", err)
	}

	fmt.Printf("eval %q over %q (top_k=%d, %d queries)\n\n", *goldenPath, collection, report.TopK, report.Metrics.Queries)
	for _, res := range report.Results {
		marker := fmt.Sprintf("rank %d", res.Rank)
		if res.Rank == 0 {
			marker = "MISS"
		}
		fmt.Printf("  [%-7s] %s\n", marker, res.Query)
	}
	m := report.Metrics
	fmt.Printf("\n%-8s %.3f\n%-8s %.3f\n%-8s %.3f\n%-8s %.3f\n",
		"MRR", m.MRR, "Hit@1", m.HitAt1, "Hit@3", m.HitAt3, fmt.Sprintf("Hit@%d", report.TopK), m.HitAtK)
}

func loadGolden(path string) (goldenSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return goldenSet{}, err
	}
	var set goldenSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return goldenSet{}, err
	}
	return set, nil
}

// seed ingests a markdown file into the collection (creating it if needed) and waits for the
// ingest job to finish, so the eval is reproducible from an empty Qdrant.
func seed(client *http.Client, addr, collection, mdPath string) error {
	content, err := os.ReadFile(mdPath)
	if err != nil {
		return err
	}
	if err := postJSON(client, addr+"/api/collections", map[string]any{"name": collection}, nil); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	var started struct {
		JobID string `json:"job_id"`
	}
	body := map[string]any{
		"source":     map[string]any{"type": "upload", "name": filepath.Base(mdPath), "content": string(content)},
		"collection": collection,
	}
	if err := postJSON(client, addr+"/api/ingest", body, &started); err != nil {
		return fmt.Errorf("ingest: %w", err)
	}

	deadline := time.Now().Add(10 * time.Minute)
	for {
		var job struct {
			State    string `json:"state"`
			Ingested int    `json:"ingested"`
			Error    string `json:"error"`
		}
		if err := getJSON(client, addr+"/api/jobs/"+started.JobID, &job); err != nil {
			return err
		}
		switch job.State {
		case "succeeded":
			fmt.Printf("seeded %d chunks into %q from %s\n\n", job.Ingested, collection, mdPath)
			return nil
		case "failed":
			return fmt.Errorf("ingest job failed: %s", job.Error)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ingest job timed out")
		}
		time.Sleep(time.Second)
	}
}

func postJSON(client *http.Client, url string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func getJSON(client *http.Client, url string, out any) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
