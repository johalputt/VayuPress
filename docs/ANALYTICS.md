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
which carry no script. Either way only a person is counted, by one rule
every path shares: VayuShield's classification decides. An automated client
is never counted, whether or not it ran the page's script: a verified search
crawler, an AI agent, a headless browser or a bad bot. A client VayuShield
places between a person and a bot is counted only when the page's own script
ran, which a monitor or a script fetching pages does not do. A prefetch, a
refused country and a reader who asked not to be tracked (Do Not Track,
Global Privacy Control) are never recorded.

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
against the visitors of the same range. A goal or funnel belongs to a site: one
made on a site's own Analytics counts that site's traffic alone, and the
install's goals and funnels count every site's. A funnel counts visits, each
walked through its steps in order.

**Search.** A search made from a site's search page, recorded by the server
as it answers: the query and how many results it found. It is an event, not a
page view, so a visit with a search is not a bounce. A query is lowercased and
trimmed, and one that looks like an email address or holds five or more digits
in a row is not kept at all. Searches in the instant search box are answered in
the reader's browser and are not counted.

**Retention.** Not measured. A visitor is the same only within a day, so
nobody can be seen to come back on a later day, and a week-over-week cohort
would be empty by construction. The API's `retention` route says so rather
than drawing cohorts.

## Ranges

A range is a set of UTC days. "Last 14 days" is today and the 13 days before
it. A period comparison reads the range of equal length just before, by the
same definitions.

## Alerts

An alert counts one figure over the last 5 minutes to 1 day, once a minute,
for one hosted site or for the whole install. It fires when the count is
above its threshold, or below it, and then waits out its cooldown before it
may fire again, however long the count stays past the threshold. A firing is
a line in the console's bell, in the alert's severity, until it is marked
seen, and it is mailed to the alert's address when it has one and the install
can send mail. Each firing stays in the alert's history with the count, the
rule as it read then, and what became of its mail. Only an administrator adds
or deletes an alert, because an alert mails the address it is given. The
code is `internal/alerts`.

The figures are counted from the same rows the reports read:

- **Page views**: page views in the window.
- **Visitors**: people with any event in the window, as the live panel counts
  its readers, once however many tabs or visits.
- **Searches that found nothing**: searches whose result count was 0.
- **Requests the shield refused**: blocks and operator limits' 429s, the
  refusals the Security page counts.
- **Challenges issued**: challenges the shield served, solved or not.

A window ends at the minute the check runs, and each count reads a range of
an index, so a check costs what its window holds. A site that is removed stops
being checked.
