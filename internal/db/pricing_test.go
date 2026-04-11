package db

import (
	"testing"
)

func TestMigrationCreatesModelPricingTable(t *testing.T) {
	d := testDB(t)

	var count int
	err := d.getReader().QueryRow(
		`SELECT count(*) FROM pragma_table_info('model_pricing')`,
	).Scan(&count)
	requireNoError(t, err, "pragma_table_info")

	if count == 0 {
		t.Fatal("model_pricing table not created by schema")
	}
}

func TestUpsertModelPricing(t *testing.T) {
	d := testDB(t)

	prices := []ModelPricing{
		{
			ModelPattern:         "claude-sonnet-4",
			InputPerMTok:         3.0,
			OutputPerMTok:        15.0,
			CacheCreationPerMTok: 3.75,
			CacheReadPerMTok:     0.30,
		},
	}

	err := d.UpsertModelPricing(prices)
	requireNoError(t, err, "UpsertModelPricing")

	got, err := d.GetModelPricing("claude-sonnet-4")
	requireNoError(t, err, "GetModelPricing")

	if got == nil {
		t.Fatal("expected pricing, got nil")
	}
	if got.ModelPattern != "claude-sonnet-4" {
		t.Errorf("ModelPattern = %q, want %q",
			got.ModelPattern, "claude-sonnet-4")
	}
	if got.InputPerMTok != 3.0 {
		t.Errorf("InputPerMTok = %v, want 3.0",
			got.InputPerMTok)
	}
	if got.OutputPerMTok != 15.0 {
		t.Errorf("OutputPerMTok = %v, want 15.0",
			got.OutputPerMTok)
	}
	if got.CacheCreationPerMTok != 3.75 {
		t.Errorf("CacheCreationPerMTok = %v, want 3.75",
			got.CacheCreationPerMTok)
	}
	if got.CacheReadPerMTok != 0.30 {
		t.Errorf("CacheReadPerMTok = %v, want 0.30",
			got.CacheReadPerMTok)
	}
	if got.UpdatedAt == "" {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestUpsertModelPricingOverwrites(t *testing.T) {
	d := testDB(t)

	initial := []ModelPricing{
		{
			ModelPattern:         "claude-opus-4",
			InputPerMTok:         15.0,
			OutputPerMTok:        75.0,
			CacheCreationPerMTok: 18.75,
			CacheReadPerMTok:     1.50,
		},
	}
	err := d.UpsertModelPricing(initial)
	requireNoError(t, err, "UpsertModelPricing initial")

	updated := []ModelPricing{
		{
			ModelPattern:         "claude-opus-4",
			InputPerMTok:         10.0,
			OutputPerMTok:        50.0,
			CacheCreationPerMTok: 12.50,
			CacheReadPerMTok:     1.00,
		},
	}
	err = d.UpsertModelPricing(updated)
	requireNoError(t, err, "UpsertModelPricing updated")

	got, err := d.GetModelPricing("claude-opus-4")
	requireNoError(t, err, "GetModelPricing after update")

	if got == nil {
		t.Fatal("expected pricing, got nil")
	}
	if got.InputPerMTok != 10.0 {
		t.Errorf("InputPerMTok = %v, want 10.0",
			got.InputPerMTok)
	}
	if got.OutputPerMTok != 50.0 {
		t.Errorf("OutputPerMTok = %v, want 50.0",
			got.OutputPerMTok)
	}
	if got.CacheCreationPerMTok != 12.50 {
		t.Errorf("CacheCreationPerMTok = %v, want 12.50",
			got.CacheCreationPerMTok)
	}
	if got.CacheReadPerMTok != 1.00 {
		t.Errorf("CacheReadPerMTok = %v, want 1.00",
			got.CacheReadPerMTok)
	}
}

func TestGetModelPricingNotFound(t *testing.T) {
	d := testDB(t)

	got, err := d.GetModelPricing("nonexistent-model")
	requireNoError(t, err, "GetModelPricing not found")

	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}
