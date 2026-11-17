package agent

import (
	"math"
	"slices"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func message(input, output, cacheWrite, cacheRead int64) *anthropic.Message {
	return &anthropic.Message{Usage: anthropic.Usage{
		InputTokens:              input,
		OutputTokens:             output,
		CacheCreationInputTokens: cacheWrite,
		CacheReadInputTokens:     cacheRead,
	}}
}

func TestUsageCostPricesEachCallByItsModel(t *testing.T) {
	var u Usage
	// Agent loop on Sonnet 5: $2 in / $10 out per MTok.
	u.add("claude-sonnet-5", message(1_000_000, 100_000, 0, 0))
	// Cleanup on Opus 4.8: $5 in / $25 out / $6.25 cache write / $0.50 cache read.
	u.add("claude-opus-4-8", message(200_000, 50_000, 20_000, 10_000))

	cost, unpriced := u.Cost()
	want := (2.0 + 1.0) + (1.0 + 1.25 + 0.125 + 0.005)
	if math.Abs(cost-want) > 1e-9 {
		t.Errorf("cost = %.6f, want %.6f", cost, want)
	}
	if len(unpriced) != 0 {
		t.Errorf("unpriced = %v, want none", unpriced)
	}
	if u.InputTokens != 1_200_000 || u.OutputTokens != 150_000 ||
		u.CacheCreationInputTokens != 20_000 || u.CacheReadInputTokens != 10_000 {
		t.Errorf("token totals = %+v", u)
	}
}

func TestUsageCostReportsUnpricedModels(t *testing.T) {
	var u Usage
	u.add("claude-sonnet-5", message(1_000_000, 0, 0, 0))
	u.add("claude-unknown-1", message(500, 500, 0, 0))
	u.add("claude-unknown-1", message(500, 500, 0, 0))

	cost, unpriced := u.Cost()
	if math.Abs(cost-2.0) > 1e-9 {
		t.Errorf("cost = %.6f, want 2.0 (unpriced calls excluded)", cost)
	}
	if !slices.Equal(unpriced, []string{"claude-unknown-1"}) {
		t.Errorf("unpriced = %v, want [claude-unknown-1]", unpriced)
	}
	if u.InputTokens != 1_001_000 {
		t.Errorf("InputTokens = %d, want unpriced calls still counted", u.InputTokens)
	}
}

func TestUsageCostCurrentDefaults(t *testing.T) {
	var u Usage
	// Agent loop on Sonnet 5.5: $2 in / $10 out / $0.10 cache read per MTok.
	u.add("claude-sonnet-5-5", message(1_000_000, 100_000, 0, 1_000_000))
	// Cleanup on Opus 5.5: $4 in / $20 out / $5 cache write per MTok.
	u.add("claude-opus-5-5", message(200_000, 50_000, 20_000, 0))

	cost, unpriced := u.Cost()
	want := (2.0 + 1.0 + 0.10) + (0.8 + 1.0 + 0.1)
	if math.Abs(cost-want) > 1e-9 {
		t.Errorf("cost = %.6f, want %.6f", cost, want)
	}
	if len(unpriced) != 0 {
		t.Errorf("unpriced = %v, want none", unpriced)
	}
}
