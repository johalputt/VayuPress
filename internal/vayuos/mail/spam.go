package mail

// spam.go — VayuMail's built-in heuristic junk filter.
//
// This is a deliberately small, fully-local scorer: it inspects only the
// message bytes already on disk and makes no network calls and uses no external
// services (privacy by default, single binary). It is intentionally
// conservative — the goal is to catch obvious junk while keeping false
// positives low, since a misfiled legitimate message is worse than a junk
// message reaching the inbox. Operators can disable it via config.

import (
	"bytes"
	"net/mail"
	"regexp"
	"strings"
)

// SpamVerdict is the outcome of scoring a message.
type SpamVerdict struct {
	Score   int      `json:"score"`
	IsSpam  bool     `json:"is_spam"`
	Reasons []string `json:"reasons,omitempty"`
}

// SpamThreshold is the score at or above which a message is filed as Junk.
// Tuned to require several independent signals before acting.
const SpamThreshold = 6

var (
	spamURLRe   = regexp.MustCompile(`(?i)https?://`)
	spamShoutRe = regexp.MustCompile(`[A-Z]{6,}`)

	// Phrases strongly associated with junk/marketing/scam mail. Lowercase;
	// matched case-insensitively against subject + body.
	spamPhrases = []string{
		"viagra", "cialis", "lottery", "you have won", "you won", "winner",
		"click here", "free money", "act now", "limited time offer",
		"risk free", "100% free", "cash bonus", "wire transfer",
		"nigerian prince", "bitcoin investment", "crypto investment",
		"work from home", "earn extra cash", "get rich", "weight loss",
		"refinance", "pre-approved", "this is not spam", "make money fast",
		"double your", "satisfaction guaranteed", "no credit check",
		"miracle", "enlargement", "dear friend", "verify your account",
		"suspended account", "confirm your password", "unclaimed funds",
	}
)

// ScoreSpam applies the heuristic filter to a raw RFC 5322 message and returns
// a verdict. It never errors: malformed messages simply score on the bytes
// available.
func ScoreSpam(raw []byte) SpamVerdict {
	v := SpamVerdict{}
	add := func(n int, reason string) {
		v.Score += n
		v.Reasons = append(v.Reasons, reason)
	}

	var subject, from, body string
	if msg, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		subject = msg.Header.Get("Subject")
		from = msg.Header.Get("From")
		if msg.Header.Get("Date") == "" {
			add(2, "missing Date header")
		}
		if from == "" {
			add(2, "missing From header")
		}
		// Inbound authentication outcome (set by the SMTP receiver). A DMARC
		// failure under an enforcing policy forces the message to Junk; a plain
		// DMARC alignment failure contributes a smaller signal.
		if strings.EqualFold(strings.TrimSpace(msg.Header.Get("X-VayuMail-Auth-Quarantine")), "yes") {
			add(SpamThreshold, "failed DMARC under an enforcing policy")
		} else if ar := strings.ToLower(msg.Header.Get("Authentication-Results")); strings.Contains(ar, "dmarc=fail") {
			add(2, "DMARC alignment failure")
		}
		if b, berr := readAll(msg); berr == nil {
			body = b
		}
	} else {
		// Unparseable message — treat whole payload as the body.
		body = string(raw)
		add(1, "unparseable message structure")
	}

	lowSub := strings.ToLower(subject)
	lowBody := strings.ToLower(body)
	combined := lowSub + "\n" + lowBody

	// Phrase hits (capped so a single message can't run away).
	phraseHits := 0
	for _, p := range spamPhrases {
		if strings.Contains(combined, p) {
			phraseHits++
		}
	}
	if phraseHits > 0 {
		score := phraseHits * 2
		if score > 6 {
			score = 6
		}
		add(score, pluralReason(phraseHits, "junk phrase"))
	}

	// ALL-CAPS shouting in the subject.
	if spamShoutRe.MatchString(subject) && len(subject) >= 10 {
		add(2, "shouting subject")
	}

	// Excessive exclamation marks.
	if strings.Count(subject, "!") >= 3 || strings.Count(body, "!!!") >= 1 {
		add(1, "excessive exclamation marks")
	}

	// Lots of links in the body.
	if links := len(spamURLRe.FindAllString(body, -1)); links >= 10 {
		add(2, "many links")
	}

	// Currency-bait markers.
	if strings.Contains(body, "$$$") || strings.Contains(combined, "100% guaranteed") {
		add(2, "money-bait markers")
	}

	v.IsSpam = v.Score >= SpamThreshold
	return v
}

// readAll returns the message body as a string, bounded to a sane size so a
// huge message can't blow up scoring memory.
func readAll(msg *mail.Message) (string, error) {
	const maxBody = 1 << 20 // 1 MiB is plenty for heuristic scoring
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for len(buf) < maxBody {
		n, err := msg.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf), nil
}

func pluralReason(n int, base string) string {
	if n == 1 {
		return base
	}
	return base + "s"
}
