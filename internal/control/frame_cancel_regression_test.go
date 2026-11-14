package control

import (
	"context"
	"crypto/sha256"
	"testing"
)

func TestFrameBackendContextErrorIsNotUserCancellation(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{})
	b.hooks(func(Step) error { return context.Canceled }, nil, nil)
	accepted := submitFrameIntegration(t, e, frameIntegrationProgram(), receiptTicket(e, 101))
	job := awaitFrameIntegration(t, e, accepted.Job.ID, "failed")
	if job.CompletedFrames != 0 || job.CompletedTargetWrites != 0 {
		t.Fatalf("native error was treated as a successful write: %#v", job)
	}
}

func TestFrameIntegrationKeepsLegacyReceiptDigestBytes(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	ticket := receiptTicket(e, 102)
	_, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#112233", "off"), "reject", ticket)
	if err != nil {
		t.Fatal(err)
	}
	// The exact original v1 admission encoding, without a new kind or domain.
	legacy := `{"Version":1,"Serial":"a","Program":{"steps":[{"led":0,"color":"#112233","fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"off"},"Policy":"reject"}`
	want := sha256.Sum256([]byte(legacy))
	e.mu.Lock()
	entry := e.receipts.entries[receiptKey{"a", ticket.RequestID}]
	got := entry.digest
	e.mu.Unlock()
	if got != want {
		t.Fatalf("legacy receipt digest bytes changed: %x, want %x", got, want)
	}
	gate.open()
}
