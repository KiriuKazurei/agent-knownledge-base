package storage

import (
	"context"
	"encoding/json"
	"testing"
)

func TestJobCheckpointPersistsStageCountersAndOutcome(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	job, err := store.CreateJob(ctx, "knowledge_summarize", map[string]any{"documentId": "document-1"})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := map[string]any{"version": 1, "nextBatch": 2, "candidates": []string{"first", "second"}}
	if err := store.UpdateJobCheckpoint(ctx, job.ID, "running", "enhancement", 0.4, 2, 5, "", "", "Completed summary model batch 2/5", checkpoint); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetJobCheckpoint(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Stage != "enhancement" || saved.CompletedUnits != 2 || saved.TotalUnits != 5 {
		t.Fatalf("unexpected checkpoint metadata: %+v", saved)
	}
	var savedData struct {
		Version    int      `json:"version"`
		NextBatch  int      `json:"nextBatch"`
		Candidates []string `json:"candidates"`
	}
	if err := json.Unmarshal(saved.Data, &savedData); err != nil || savedData.Version != 1 || savedData.NextBatch != 2 || len(savedData.Candidates) != 2 {
		t.Fatalf("unexpected checkpoint data: %s (%v)", saved.Data, err)
	}
	items, err := store.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Stage != "enhancement" || items[0].CompletedUnits != 2 || items[0].TotalUnits != 5 {
		t.Fatalf("job progress was not exposed: %+v", items)
	}
	if err := store.UpdateJobCheckpoint(ctx, job.ID, "completed", "completed", 1, 5, 5, "completed", "", "Summary completed", map[string]any{"nextBatch": 5}); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Outcome != "completed" || items[0].Status != "completed" {
		t.Fatalf("terminal job metadata was not persisted: %+v", items)
	}
	second, err := store.CreateJob(ctx, "knowledge_summarize", map[string]any{"documentId": "document-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateJob(ctx, second.ID, "running", 0.2, "running"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelJob(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateJobCheckpoint(ctx, second.ID, "completed", "completed", 1, 1, 1, "completed", "", "late result", map[string]any{"nextBatch": 1}); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == second.ID && (item.Status != "cancelled" || item.Stage != "cancelled" || item.Outcome != "cancelled") {
			t.Fatalf("late checkpoint resurrected cancelled job: %+v", item)
		}
	}
}
