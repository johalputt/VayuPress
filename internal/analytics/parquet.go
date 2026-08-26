// SPDX-License-Identifier: Apache-2.0

package analytics

// parquet.go — warehouse Parquet export (2025 plan Wave 4, item 9).
//
// Streams one day-ranged slice of raw pageviews as a Parquet file so an
// operator can land VayuPress data in a lakehouse without CSV round-trips.
// Column set mirrors what the cookieless beacon records; there is deliberately
// no IP, UA, or identifier column — the warehouse copy must obey the same
// no-PII contract as the live product. Rows stream out in page-size batches;
// nothing buffers the whole range in memory.

import (
	"context"
	"io"
	"time"

	"github.com/parquet-go/parquet-go"
)

// PageviewRecord is one exported row. Parquet tags fix the physical schema.
type PageviewRecord struct {
	CreatedAt   int64  `parquet:"created_at,timestamp(millisecond)" json:"created_at"`
	SessionID   string `parquet:"session_id,plain" json:"session_id"`
	DomainID    string `parquet:"domain_id,plain" json:"domain_id"`
	URLPath     string `parquet:"url_path,plain" json:"url_path"`
	Referrer    string `parquet:"referrer,plain" json:"referrer"`
	Hostname    string `parquet:"hostname,plain" json:"hostname"`
	UTMSource   string `parquet:"utm_source,plain" json:"utm_source"`
	UTMMedium   string `parquet:"utm_medium,plain" json:"utm_medium"`
	UTMCampaign string `parquet:"utm_campaign,plain" json:"utm_campaign"`
	Country     string `parquet:"country,plain" json:"country"`
	EventType   int32  `parquet:"event_type" json:"event_type"`
	EventName   string `parquet:"event_name,plain" json:"event_name"`
}

// ExportPageviewsParquet streams [fromDay,toDay] (inclusive UTC keys) to w.
func (s *Store) ExportPageviewsParquet(ctx context.Context, fromDay, toDay string, w io.Writer) error {
	rows, err := s.readDB().QueryContext(ctx, `
		SELECT created_at, session_id, COALESCE(domain_id,''), url_path,
		       referrer, hostname, utm_source, utm_medium, utm_campaign,
		       country, event_type, event_name
		FROM analytics_pageviews
		WHERE created_at>=? AND created_at<?
		ORDER BY created_at`, fromDay, toDay+"~")
	if err != nil {
		return err
	}
	defer rows.Close()

	bw := parquet.NewGenericWriter[PageviewRecord](w)
	defer bw.Close()

	batch := make([]PageviewRecord, 0, 1024)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := bw.Write(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		var r PageviewRecord
		var createdAt time.Time
		var et int
		if err := rows.Scan(&createdAt, &r.SessionID, &r.DomainID, &r.URLPath,
			&r.Referrer, &r.Hostname, &r.UTMSource, &r.UTMMedium, &r.UTMCampaign,
			&r.Country, &et, &r.EventName); err != nil {
			return err
		}
		r.CreatedAt = createdAt.UnixMilli()
		r.EventType = int32(et)
		batch = append(batch, r)
		if len(batch) >= 1024 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return bw.Close()
}
