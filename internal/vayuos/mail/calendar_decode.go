// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"fmt"

	"github.com/emersion/go-ical"
)

// decodeCalendar reads one iCalendar object. Every calendar this package
// reads goes through it or calendarsIn, never through ical's decoder directly
// (TestEveryCalendarIsDecodedSafely).
func decodeCalendar(data []byte) (*ical.Calendar, error) { return calendarsIn(data).next() }

// calendarStream reads the iCalendar objects in data one after another.
type calendarStream struct{ dec *ical.Decoder }

func calendarsIn(data []byte) calendarStream {
	return calendarStream{dec: ical.NewDecoder(bytes.NewReader(data))}
}

// next is the next object. The decoder panics on some malformed input:
// "0;0=", a parameter with nothing after its "=" at the end of the data,
// indexes an empty slice. A calendar arrives from outside in a subscribed
// feed, an invitation, an import and a CalDAV upload, and a feed is read on a
// background goroutine, where a panic ends the whole process. So a panic is
// an unreadable calendar, like any other.
func (s calendarStream) next() (cal *ical.Calendar, err error) {
	defer func() {
		if r := recover(); r != nil {
			cal, err = nil, fmt.Errorf("an unreadable calendar: %v", r)
		}
	}()
	return s.dec.Decode()
}
