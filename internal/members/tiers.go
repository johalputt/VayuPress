package members

// tiers.go — priced subscription tiers.
//
// A tier is a named membership plan with optional monthly/yearly pricing and a
// list of human-readable benefits. The built-in "free" and "paid" tiers are
// seeded by migration 037; operators can add, edit, hide, or archive tiers from
// the Members console. Tiers drive the public pricing page, the member portal,
// and Monthly Recurring Revenue (MRR) reporting.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Visibility values for a tier.
const (
	VisibilityPublic = "public" // listed on the public pricing page
	VisibilityHidden = "hidden" // assignable by operators, not listed publicly
)

// Tier is a priced membership plan.
type Tier struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	MonthlyCents int       `json:"monthly_cents"`
	YearlyCents  int       `json:"yearly_cents"`
	Currency     string    `json:"currency"`
	Benefits     []string  `json:"benefits"`
	Visibility   string    `json:"visibility"`
	Active       bool      `json:"active"`
	Sort         int       `json:"sort"`
	CreatedAt    time.Time `json:"created_at"`
}

// IsFree reports whether the tier carries no recurring price.
func (t *Tier) IsFree() bool { return t != nil && t.MonthlyCents == 0 && t.YearlyCents == 0 }

// MonthlyPrice formats the monthly price as a major-unit string (e.g. "5.00").
func (t *Tier) MonthlyPrice() string { return formatCents(t.MonthlyCents) }

// YearlyPrice formats the yearly price as a major-unit string.
func (t *Tier) YearlyPrice() string { return formatCents(t.YearlyCents) }

func formatCents(c int) string { return fmt.Sprintf("%.2f", float64(c)/100) }

// scanTier reads one tier row.
func scanTier(sc scanner) (*Tier, error) {
	var t Tier
	var benefits string
	var active int
	if err := sc.Scan(&t.ID, &t.Slug, &t.Name, &t.Description, &t.MonthlyCents, &t.YearlyCents,
		&t.Currency, &benefits, &t.Visibility, &active, &t.Sort, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.Active = active != 0
	if benefits != "" {
		_ = json.Unmarshal([]byte(benefits), &t.Benefits)
	}
	return &t, nil
}

const tierCols = `id,slug,name,description,monthly_cents,yearly_cents,currency,benefits,visibility,active,sort,created_at`

// TierInput carries the editable fields of a tier.
type TierInput struct {
	Name         string
	Description  string
	MonthlyCents int
	YearlyCents  int
	Currency     string
	Benefits     []string
	Visibility   string
	Sort         int
}

// CreateTier inserts a new tier, deriving a unique slug from the name.
func (s *Store) CreateTier(ctx context.Context, in TierInput) (*Tier, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("tier name is required")
	}
	slug := s.uniqueTierSlug(ctx, slugify(name))
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	visibility := in.Visibility
	if visibility != VisibilityHidden {
		visibility = VisibilityPublic
	}
	benefits, _ := json.Marshal(cleanBenefits(in.Benefits))
	id := "tier_" + randHex(8)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_tiers(id,slug,name,description,monthly_cents,yearly_cents,currency,benefits,visibility,active,sort)
		 VALUES(?,?,?,?,?,?,?,?,?,1,?)`,
		id, slug, name, strings.TrimSpace(in.Description), maxInt(0, in.MonthlyCents), maxInt(0, in.YearlyCents),
		currency, string(benefits), visibility, in.Sort); err != nil {
		return nil, fmt.Errorf("create tier: %w", err)
	}
	return s.GetTierByID(ctx, id)
}

// UpdateTier updates an existing tier's editable fields by id. The slug of the
// built-in free/paid tiers is preserved; only the display fields change.
func (s *Store) UpdateTier(ctx context.Context, id string, in TierInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return fmt.Errorf("tier name is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	visibility := in.Visibility
	if visibility != VisibilityHidden {
		visibility = VisibilityPublic
	}
	benefits, _ := json.Marshal(cleanBenefits(in.Benefits))
	res, err := s.db.ExecContext(ctx,
		`UPDATE member_tiers SET name=?,description=?,monthly_cents=?,yearly_cents=?,currency=?,benefits=?,visibility=?,sort=? WHERE id=?`,
		name, strings.TrimSpace(in.Description), maxInt(0, in.MonthlyCents), maxInt(0, in.YearlyCents),
		currency, string(benefits), visibility, in.Sort, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("tier not found")
	}
	return nil
}

// GetTier returns the tier with the given slug.
func (s *Store) GetTier(ctx context.Context, slug string) (*Tier, error) {
	return scanTier(s.db.QueryRowContext(ctx, `SELECT `+tierCols+` FROM member_tiers WHERE slug=?`, slug))
}

// GetTierByID returns the tier with the given id.
func (s *Store) GetTierByID(ctx context.Context, id string) (*Tier, error) {
	return scanTier(s.db.QueryRowContext(ctx, `SELECT `+tierCols+` FROM member_tiers WHERE id=?`, id))
}

// ListTiers returns tiers ordered for display. When includeHidden is false only
// active, public tiers are returned (suitable for the public pricing page).
func (s *Store) ListTiers(ctx context.Context, includeHidden bool) ([]Tier, error) {
	q := `SELECT ` + tierCols + ` FROM member_tiers`
	if !includeHidden {
		q += ` WHERE active=1 AND visibility='public'`
	}
	q += ` ORDER BY sort ASC, monthly_cents ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tier
	for rows.Next() {
		t, err := scanTier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ArchiveTier deactivates a tier so it stops appearing publicly. Existing
// members on the tier keep it. The built-in free/paid tiers cannot be archived.
func (s *Store) ArchiveTier(ctx context.Context, id string) error {
	t, err := s.GetTierByID(ctx, id)
	if err != nil {
		return fmt.Errorf("tier not found")
	}
	if t.Slug == TierFree || t.Slug == TierPaid {
		return fmt.Errorf("the built-in %s tier cannot be removed", t.Slug)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE member_tiers SET active=0,visibility='hidden' WHERE id=?`, id)
	return err
}

// uniqueTierSlug returns base, or base-2, base-3… until it is unused.
func (s *Store) uniqueTierSlug(ctx context.Context, base string) string {
	if base == "" {
		base = "tier"
	}
	slug := base
	for i := 2; ; i++ {
		var n int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM member_tiers WHERE slug=?`, slug).Scan(&n)
		if err != nil || n == 0 {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

func cleanBenefits(in []string) []string {
	out := make([]string, 0, len(in))
	for _, b := range in {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
