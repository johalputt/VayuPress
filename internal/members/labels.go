package members

// labels.go — member segmentation labels.
//
// Labels are free-form tags an operator attaches to members (e.g. "founding",
// "vip", "trial") for filtering and targeted communication. Labels are created
// on first use and shared across members via a join table.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Label is a member segmentation tag.
type Label struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// AddLabel attaches a label (by name) to a member, creating the label if it
// does not yet exist. Label names are normalised to lowercase.
func (s *Store) AddLabel(ctx context.Context, memberID, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("label name is required")
	}
	if memberID == "" {
		return fmt.Errorf("member id is required")
	}
	labelID, err := s.ensureLabel(ctx, name)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO member_label_map(member_id,label_id) VALUES(?,?)`, memberID, labelID)
	return err
}

// RemoveLabel detaches a label (by name) from a member.
func (s *Store) RemoveLabel(ctx context.Context, memberID, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM member_label_map
		   WHERE member_id=? AND label_id=(SELECT id FROM member_labels WHERE name=?)`,
		memberID, name)
	return err
}

// LabelsForMember returns the label names attached to a member, sorted.
func (s *Store) LabelsForMember(ctx context.Context, memberID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT l.name FROM member_labels l
		   JOIN member_label_map m ON m.label_id=l.id
		  WHERE m.member_id=? ORDER BY l.name`, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ListLabels returns every label with the number of members carrying it.
func (s *Store) ListLabels(ctx context.Context) ([]struct {
	Label
	Members int `json:"members"`
}, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT l.id,l.name,l.created_at,COUNT(m.member_id)
		   FROM member_labels l
		   LEFT JOIN member_label_map m ON m.label_id=l.id
		  GROUP BY l.id ORDER BY l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Label
		Members int `json:"members"`
	}
	for rows.Next() {
		var row struct {
			Label
			Members int `json:"members"`
		}
		if err := rows.Scan(&row.ID, &row.Name, &row.CreatedAt, &row.Members); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ensureLabel returns the id of the label with the given name, creating it when
// absent.
func (s *Store) ensureLabel(ctx context.Context, name string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM member_labels WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	id = "lbl_" + randHex(8)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_labels(id,name) VALUES(?,?)`, id, name); err != nil {
		return "", err
	}
	return id, nil
}
