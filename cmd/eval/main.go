// Command eval measures retrieval quality against a golden query set.
//
// It posts each golden query to a running markdex /api/search, checks whether the
// expected section (by heading_path substring) is retrieved and how highly it ranks,
// and reports Hit@1 / Hit@3 / Hit@k / MRR. Use it to detect regressions and to compare
// configurations (reranker model, pool size, etc.).
//
//	go run ./cmd/eval -golden cmd/eval/golden/go-style-guide.json
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

type searchRequest struct {
	Collection string `json:"collection"`
	Query      string `json:"query"`
	TopK       int    `json:"top_k"`
}

type searchResponse struct {
	Results []struct {
		Metadata map[string]string `json:"metadata"`
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
	if k < 1 {
		k = 10
	}
	collection := set.Collection
	if *collectionFlag != "" {
		collection = *collectionFlag
	}

	client := &http.Client{Timeout: 60 * time.Second}

	if *seedFile != "" {
		if err := seed(client, *addr, collection, *seedFile); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	fmt.Printf("eval %q over %q (top_k=%d, %d queries)\n\n", *goldenPath, collection, k, len(set.Queries))

	ranks := make([]int, 0, len(set.Queries))
	for _, q := range set.Queries {
		paths, err := search(client, *addr, collection, q.Query, k)
		if err != nil {
			log.Fatalf("search %q: %v", q.Query, err)
		}
		rank := firstRelevantRank(paths, q.RelevantHeadingContains)
		ranks = append(ranks, rank)

		marker := fmt.Sprintf("rank %d", rank)
		if rank == 0 {
			marker = "MISS"
		}
		fmt.Printf("  [%-7s] %s\n", marker, q.Query)
	}

	m := aggregate(ranks, k)
	fmt.Printf("\n%-8s %.3f\n%-8s %.3f\n%-8s %.3f\n%-8s %.3f\n",
		"MRR", m.MRR, "Hit@1", m.HitAt1, "Hit@3", m.HitAt3, fmt.Sprintf("Hit@%d", k), m.HitAtK)
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

// seed ingests a markdown file into the collection (creating it if needed) and waits for
// the ingest job to finish, so the eval is reproducible from an empty Qdrant.
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

func search(client *http.Client, addr, collection, query string, k int) ([]string, error) {
	payload, err := json.Marshal(searchRequest{Collection: collection, Query: query, TopK: k})
	if err != nil {
		return nil, err
	}

	resp, err := client.Post(addr+"/api/search", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var out searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	paths := make([]string, len(out.Results))
	for i, r := range out.Results {
		paths[i] = r.Metadata["heading_path"]
	}
	return paths, nil
}
