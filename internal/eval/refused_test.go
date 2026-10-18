package eval

import "testing"

// A row the model declined to judge is neither a label nor an outage: it is
// counted on its own, and it still fails the pass.
func TestScore_ARefusalIsNotNoAnswer(t *testing.T) {
	s := Score{Answers: []Answer{
		{Label: LabelDeny, Row: Row{Expect: []string{LabelDeny}}},
		{Refused: true, Err: "declined"},
		{Err: "503"},
	}}
	if s.Refused() != 1 || s.Unanswered() != 1 {
		t.Fatalf("refused = %d, unanswered = %d, want 1 and 1", s.Refused(), s.Unanswered())
	}
	if got := s.Answers[1].Got(); got != "refused" {
		t.Errorf("Got = %q", got)
	}
	if got := s.Answers[2].Got(); got != "no answer" {
		t.Errorf("Got = %q", got)
	}
}
