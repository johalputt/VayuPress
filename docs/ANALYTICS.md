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

**Channel.** How a page view arrived, from its referrer's host: Direct when
there is none or it is one of the install's own hosts (a reader moving
between a site's pages), else AI assistants, Email, Workplace apps, Social,
Organic search or Referral, tried in that order. The hosts each is known by
are listed once, in `internal/vayuanalytics/classifier`, which the
engagement store classifies by too, so a host is in one channel on every
report. A host is matched by its labels and domain, never as a substring:
`cnet.com` is not `t.co`.

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

**What followed a search.** The next event in the search's visit that says
what came of it: a view of another page (the reader opened something), or
another search first (the reader tried again in other words, a refinement
from the first query to the second). The results page's own view is not it,
since the page counts itself as it is shown. A search with neither after it
in its visit was followed by nothing. A page opened next is usually a result
and not always one, so the figure is named "followed by a page", not "clicked
a result".

**Link out and download.** A click on a link to another host is a link out;
a click on a link to a file (a document, an archive, a recording, or any link
marked `download`) is a download, wherever the file is. The counting script
reports each, and what is kept is where the link led, as its host and path:
never its query, fragment, port or user name, which is where a token or an
address travels. A file on the page's own host is kept by its path alone.
Neither is a custom event or a conversion; each is an event of its visit, so
a visit that ended in one is not a bounce.

**Exit page.** The page of a visit's last page view.

**Reading depth.** How far down a page its reader scrolled, as the
engagement beacon reports it when the reader leaves: the furthest point seen,
measured when the page has laid out as well as on every scroll, so a page
shorter than the window counts as read to its end. A reading whose reader
left before the beacon could be sent has no depth and is not counted. The
Analytics page gives, for the most read pages, the share of readings that
passed half the page and nine tenths of it. Bots' readings are not counted.

**Retention.** Not measured. A visitor is the same only within a day, so
nobody can be seen to come back on a later day, and a week-over-week cohort
would be empty by construction. The API's `retention` route says so rather
than drawing cohorts.

## Ranges

A range is a set of UTC days. "Last 14 days" is today and the 13 days before
it. A period comparison reads the range of equal length just before, by the
same definitions.

## Explorer

The Explorer counts one figure (page views, visitors, visits, bounce rate,
average visit or custom events) for a range of days, split by one dimension
and kept to the events that pass up to five filters, each one that a
dimension is, is not, or starts with a value. "Starts with" is matched
exactly, with its case, and `%` or `_` in the value is that character. Each
figure is counted as the overview counts it, from the same rows, and its total
is counted over the filtered events, not added up from the split: a visitor
who read two pages is one visitor, though one in each page's row. A visit has
one landing page, place, device and browser but no one page, referrer,
campaign or event, so visits, bounce rate and average visit split and filter
by the visit's own columns alone, and the Explorer refuses the rest with the
reason. Beside the range before, each value is compared with its own count
there, a day with the day as far into that range. A split shows its 100
largest values and says when there are more; a count (page views in the
visit, its events, its length in seconds) is listed in order instead, as a
distribution.

A visit's own facts split and filter every figure, an event's through its
visit: its channel and referrer are its first page view's, the one it arrived
by; "returning the same day" is a visit after the same visitor's earlier one
to the same site, which can only be that day, as a visitor is recognised for
a day; "a visit with a custom event" is one with any; and its page views,
events and length are the whole visit's, counted as at least or at most a
number.

A segment is a name for up to five filters, saved from the Explorer for one
site or for the install, as goals are. Applied in the Explorer, its filters
hold with the Explorer's own. The Analytics page lists each segment with the
visitors in it over the period and their share of all of them. The CSV file is the same answer
with every value it shows and the total.

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
