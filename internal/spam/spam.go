// Package spam provides content-based spam detection for comments and submissions.
// It uses heuristic scoring — no ML, no external APIs, no vendor dependencies.
package spam

import (
	"regexp"
	"strings"
)

// Score is a spam probability from 0.0 (clean) to 1.0 (definite spam).
type Score struct {
	Value   float64  `json:"value"`
	Signals []string `json:"signals"`
}

// IsSpam returns true when the score exceeds the given threshold (0.5 recommended).
func (s Score) IsSpam(threshold float64) bool { return s.Value >= threshold }

var (
	// Common spam URL patterns.
	spamURLRe = regexp.MustCompile(`(?i)(buy|cheap|discount|free|win|casino|poker|viagra|cialis|loan|crypto|nft|forex|click here|bit\.ly|tinyurl)`)
	// Excessive links.
	linkRe = regexp.MustCompile(`https?://`)
	// All-caps words.
	capsRe = regexp.MustCompile(`\b[A-Z]{4,}\b`)
	// Repeated punctuation.
	repeatPunctRe = regexp.MustCompile(`[!?]{3,}`)
)

// Classify scores a comment or text submission for spam likelihood.
func Classify(author, body string) Score {
	var signals []string
	score := 0.0

	// Empty body.
	if strings.TrimSpace(body) == "" {
		return Score{Value: 1.0, Signals: []string{"empty-body"}}
	}

	// Very short body (less than 5 words is suspicious for a comment).
	if wordCount(body) < 5 {
		score += 0.2
		signals = append(signals, "very-short")
	}

	// Spam keywords.
	if spamURLRe.MatchString(body) || spamURLRe.MatchString(author) {
		score += 0.4
		signals = append(signals, "spam-keywords")
	}

	// Excessive links (>2 URLs = high spam signal).
	linkCount := len(linkRe.FindAllString(body, -1))
	if linkCount > 2 {
		score += 0.3
		signals = append(signals, "excessive-links")
	} else if linkCount > 0 {
		score += 0.1
		signals = append(signals, "contains-link")
	}

	// All caps words.
	if len(capsRe.FindAllString(body, -1)) > 2 {
		score += 0.2
		signals = append(signals, "all-caps")
	}

	// Repeated punctuation (!!!, ???).
	if repeatPunctRe.MatchString(body) {
		score += 0.15
		signals = append(signals, "repeated-punctuation")
	}

	// Suspiciously generic author names.
	genericAuthors := []string{"anonymous", "user", "admin", "test", "guest", "visitor"}
	for _, g := range genericAuthors {
		if strings.EqualFold(strings.TrimSpace(author), g) {
			score += 0.1
			signals = append(signals, "generic-author")
			break
		}
	}

	// Body contains only URLs (no real text).
	textOnly := linkRe.ReplaceAllString(body, "")
	if strings.TrimSpace(textOnly) == "" {
		score += 0.5
		signals = append(signals, "urls-only")
	}

	if score > 1.0 {
		score = 1.0
	}
	return Score{Value: score, Signals: signals}
}

func wordCount(s string) int {
	return len(strings.Fields(s))
}
