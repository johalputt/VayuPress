# VayuAnalytics: what each figure means

Every figure the console shows is defined here, once, and computed by one
function for every page that shows it (`overviewWindow` in
`internal/analytics/extended.go`): the install's Analytics, a site's own
Analytics, Home, the period comparison and the export read the same
definition. `internal/analytics/accuracy_contract_test.go` holds a day of known
events and the figures below must come out of it exactly.

## What is recorded

One row per event in `analytics_pageviews`: a page view or a custom event,
its time, its site, its path, the host that referred it, its visit and its
visitor. A page view is recorded by the counting script on pages VayuPress
draws, and by the server as it serves a site's own files or a template site,
which carry no script. Either way only a person is counted: VayuShield's
classification decides, and a crawler, a prefetch, a refused country and a
reader who asked not to be tracked (Do Not Track, Global Privacy Control) are
never recorded.

No IP address, no User-Agent string and no cookie is stored. The time on every
row is the server's: the script sends none.

## Definitions

**Page view.** One page shown to a person. The same page shown to the same
person twice within two seconds is one view delivered twice (a script retry,
or a page counted by the server and by a script it carries) and is counted
once. A custom event is not a page view.

**Visitor.** A one-way hash of the day's secret salt, the address, the browser
and its language. It is the same for a person all day and different the next
day, so "visitors" over a week counts a person once for each day they came.
That is the price of storing nothing that follows a person across days, and
the console labels the figure accordingly. A site's visitors are counted from
that site's own events.

**Visit.** A person's events on one site with no gap of 30 minutes or more. A
visit that crosses midnight is one visit, belonging to the day it began; its
page views count on the days they happened. The same person on two sites makes
two visits. A restart of VayuPress forgets which visits are open, so a person
reading across a restart starts a second visit.

**Bounce.** A visit of exactly one event, a page view. A visit with a page
view and a custom event (a conversion, a signup) is not a bounce: the reader
did something. The bounce rate is bounces as a share of the visits begun in
the range.

**Average visit.** The mean time from a visit's first event to its last, over
the visits with two or more events. A visit of one event has no measured
length (nothing reports when its reader left), so it is left out rather than
averaged in as zero seconds; those visits are the bounces.

**Landing page.** The page of a visit's first event.

**Referrer.** The host of the page that sent a page view, recorded without its
path. A site's own hosts are never referrers.

**Conversion.** A custom event, or a page view of a goal's path, counted
against the visitors of the same range.

## Ranges

A range is a set of UTC days. "Last 14 days" is today and the 13 days before
it. A period comparison reads the range of equal length just before, by the
same definitions.
