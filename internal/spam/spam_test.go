package spam

import "testing"

func TestClassify_Clean(t *testing.T) {
	s := Classify("Alice", "I really enjoyed this article, thank you for the detailed write-up!")
	if s.IsSpam(0.5) {
		t.Errorf("clean comment flagged as spam: score=%.2f signals=%v", s.Value, s.Signals)
	}
}

func TestClassify_SpamKeywords(t *testing.T) {
	s := Classify("Bot", "BUY CHEAP VIAGRA NOW!!! click here https://bit.ly/abc https://bit.ly/def https://bit.ly/ghi")
	if !s.IsSpam(0.5) {
		t.Errorf("obvious spam not detected: score=%.2f signals=%v", s.Value, s.Signals)
	}
}

func TestClassify_ExcessiveLinks(t *testing.T) {
	body := "Check https://a.com and https://b.com and https://c.com for deals"
	s := Classify("user", body)
	// We don't assert on the overall spam verdict here (3 links may sit below
	// the threshold once content quality is factored in); the signal check below
	// is the real assertion.
	found := false
	for _, sig := range s.Signals {
		if sig == "excessive-links" {
			found = true
		}
	}
	if !found {
		t.Error("excessive-links signal not raised")
	}
}

func TestClassify_Empty(t *testing.T) {
	s := Classify("Alice", "")
	if !s.IsSpam(0.5) {
		t.Error("empty body should be spam")
	}
}

func TestClassify_URLsOnly(t *testing.T) {
	s := Classify("Bob", "https://casino.example.com/win-now")
	if !s.IsSpam(0.5) {
		t.Errorf("url-only body not flagged: score=%.2f", s.Value)
	}
}
