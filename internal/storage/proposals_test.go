package storage

import "testing"

// TestProposalHash_SurvivesCaseSpacingAndAFullStop holds the key to the
// differences a re-proposal makes without saying anything new, and no
// further: a sentence that says something else is asked about.
func TestProposalHash_SurvivesCaseSpacingAndAFullStop(t *testing.T) {
	base := ProposalHash("prefers table-driven tests")
	for _, same := range []string{
		"Prefers table-driven tests.",
		"  prefers   table-driven\ttests  ",
		"PREFERS TABLE-DRIVEN TESTS!",
	} {
		if got := ProposalHash(same); got != base {
			t.Errorf("%q hashed apart from the same proposal", same)
		}
	}
	for _, other := range []string{
		"prefers table tests",
		"never prefers table-driven tests",
		"prefers table-driven tests in Go",
	} {
		if ProposalHash(other) == base {
			t.Errorf("%q hashed as the declined proposal", other)
		}
	}
}

// TestDeclinedProposal_IsPerCheckoutAndKindAndCanBeTakenBack holds the
// recorded no to what it is for: one checkout's answer to one proposal of one
// kind, answering twice is the same answer, and a no can be taken back.
func TestDeclinedProposal_IsPerCheckoutAndKindAndCanBeTakenBack(t *testing.T) {
	db := openTestDB(t)
	const root, text = "/src/shhh", "Prefers table-driven tests."
	if db.ProposalDeclined(root, ProposalMemory, text) {
		t.Fatal("a proposal was declined before it was made")
	}
	if err := db.DeclineProposal(root, ProposalMemory, text); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if err := db.DeclineProposal(root, ProposalMemory, "prefers table-driven tests"); err != nil {
		t.Fatalf("declining twice: %v", err)
	}
	if !db.ProposalDeclined(root, ProposalMemory, "PREFERS table-driven tests") {
		t.Fatal("the decline was not remembered")
	}
	if db.ProposalDeclined("/src/other", ProposalMemory, text) {
		t.Fatal("one checkout's decline answered for another")
	}
	if db.ProposalDeclined(root, "skill", text) {
		t.Fatal("one kind's decline answered for another")
	}
	list, err := db.DeclinedProposals(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Text != text || list[0].Kind != ProposalMemory || list[0].DeclinedAt.IsZero() {
		t.Fatalf("want the one first-declined row, got %+v", list)
	}
	if other, _ := db.DeclinedProposals("/src/other"); len(other) != 0 {
		t.Fatalf("another checkout listed this one's declines: %+v", other)
	}
	if err := db.UndeclineProposal("/src/other", list[0].ID); err == nil {
		t.Fatal("another checkout took back this one's decline")
	}
	if !db.ProposalDeclined(root, ProposalMemory, text) {
		t.Fatal("a refused take-back removed the decline anyway")
	}
	if err := db.UndeclineProposal(root, list[0].ID); err != nil {
		t.Fatalf("undecline: %v", err)
	}
	if db.ProposalDeclined(root, ProposalMemory, text) {
		t.Fatal("a decline taken back still refuses")
	}
	if err := db.UndeclineProposal(root, list[0].ID); err == nil {
		t.Fatal("taking back a decline that is not there should say so")
	}
}
