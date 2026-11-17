package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func TestCheckRefusal(t *testing.T) {
	t.Parallel()

	ok := &anthropic.Message{StopReason: anthropic.StopReasonEndTurn}
	if err := checkRefusal(ok); err != nil {
		t.Errorf("end_turn: got %v, want nil", err)
	}

	refused := &anthropic.Message{
		Model:      "claude-opus-5-5",
		StopReason: anthropic.StopReasonRefusal,
	}
	refused.StopDetails.Category = anthropic.RefusalStopDetailsCategoryBio
	err := checkRefusal(refused)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("refusal: got %v, want ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "bio") || !strings.Contains(err.Error(), "claude-opus-5-5") {
		t.Errorf("refusal error should name the category and model: %v", err)
	}

	// stop_details can be null on a refusal; branch on stop_reason alone.
	noCategory := &anthropic.Message{StopReason: anthropic.StopReasonRefusal}
	if err := checkRefusal(noCategory); !errors.Is(err, ErrRefused) {
		t.Errorf("refusal without category: got %v, want ErrRefused", err)
	}
}
