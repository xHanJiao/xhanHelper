package controller

import (
	"testing"
	"time"
)

func TestVoiceTicketSingleUse(t *testing.T) {
	store := newVoiceTicketStore(time.Minute)
	ticket, ttl := store.issue("u1")
	if ticket == "" || ttl <= 0 {
		t.Fatalf("issue returned empty ticket/ttl")
	}
	user, ok := store.consume(ticket)
	if !ok || user != "u1" {
		t.Fatalf("consume = %q, %v", user, ok)
	}
	if _, ok := store.consume(ticket); ok {
		t.Fatal("ticket should be single-use")
	}
}

func TestVoiceTicketExpires(t *testing.T) {
	store := newVoiceTicketStore(10 * time.Millisecond)
	ticket, _ := store.issue("u1")
	time.Sleep(20 * time.Millisecond)
	if _, ok := store.consume(ticket); ok {
		t.Fatal("ticket should expire")
	}
}
