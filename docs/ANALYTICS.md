# VayuAnalytics: what each figure means

Every figure the console shows is defined here, once, and computed by one
function for every page that shows it (`overviewWindow` in
`internal/analytics/extended.go`): a site's own Analytics, every site's
together, Home, the period comparison and the export read the same
definition. `internal/analytics/accuracy_contract_test.go` holds a day of known
events and the figures below must come out of it exactly.

## Whose figures

Each site has its own Analytics, which counts its own visits alone: the
install's own site at Audience › Analytics (`/os/analytics`), which an editor
may read, and each hosted site's, opened from Site › Domains
(`/os/d/{id}/analytics`), an administrator's. Each page exports its own
reports. Home's visitors are the
install's own site's.

Every site added together is one page, System › Every site's analytics
(`/os/platform/analytics`), and only an administrator opens it: it holds
hosted customers' readers beside the install's own. It says so in its name,
and its first section is what each site adds, linking to each site's own
page. Its live panel, exports and alerts are the install-wide ones under
`/os/api/analytics`, an administrator's too. The access rule is
`osPathMinLevel` (`cmd/vayupress/handlers_auth.go`) and the test that holds
it is `analytics_platform_test.go`.

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

### Page view

One page shown to a person. The same page shown to the same
person twice within two seconds is one view delivered twice (a script retry,
or a page counted by the server and by a script it carries) and is counted
once. A custom event is not a page view.

### Visitor

A one-way hash of the day's secret salt, the address, the browser
and its language. It is the same for a person all day and different the next
day, so "visitors" over a week counts a person once for each day they came.
That is the price of storing nothing that follows a person across days, and
the console labels the figure accordingly. A site's visitors are counted from
that site's own events.

### Visit

A person's events on one site with no gap of 30 minutes or more. A
visit that crosses midnight is one visit, belonging to the day it began; its
page views count on the days they happened. The same person on two sites makes
two visits. A restart of VayuPress forgets which visits are open, so a person
reading across a restart starts a second visit.

### Bounce

A visit of exactly one event, a page view. A visit with a page
view and a custom event (a conversion, a signup) is not a bounce: the reader
did something. The bounce rate is bounces as a share of the visits begun in
the range.

### Average visit

The mean time from a visit's first event to its last, over
the visits with two or more events. A visit of one event has no measured
length (nothing reports when its reader left), so it is left out rather than
averaged in as zero seconds; those visits are the bounces.

### Landing page

The page of a visit's first event.

### Referrer

The host of the page that sent a page view, recorded without its
path. A site's own hosts are never referrers.

### Channel

How a page view arrived, from its referrer's host: Direct when
there is none or it is one of the install's own hosts (a reader moving
between a site's pages), else AI assistants, Email, Workplace apps, Social,
Organic search or Referral, tried in that order. The hosts each is known by
are listed once, in `internal/vayuanalytics/classifier`, which the
engagement store classifies by too, so a host is in one channel on every
report. A host is matched by its labels and domain, never as a substring:
`cnet.com` is not `t.co`.

### Conversion

A custom event, or a page view of a goal's path, counted
against the visitors of the same range. A goal or funnel belongs to a site: one
made on a hosted site's Analytics counts that site's traffic alone. The
install's own goals and segments, made on its Analytics or on every site's,
are listed on both: counted over the install's own site's visits on its page,
and over every site's on every site's page. The install's funnels are read
through the API, over every site's visits. A funnel counts visits,
each walked through its steps in order.

### Search

A search made from a site's search page, recorded by the server
as it answers: the query and how many results it found. It is an event, not a
page view, so a visit with a search is not a bounce. A query is lowercased and
trimmed, and one that looks like an email address or holds five or more digits
in a row is not kept at all. Searches in the instant search box are answered in
the reader's browser and are not counted.

### What followed a search

The next event in the search's visit that says
what came of it: a view of another page (the reader opened something), or
another search first (the reader tried again in other words, a refinement
from the first query to the second). The results page's own view is not it,
since the page counts itself as it is shown. A search with neither after it
in its visit was followed by nothing. A page opened next is usually a result
and not always one, so the figure is named "followed by a page", not "clicked
a result".

### Link out and download

A click on a link to another host is a link out;
a click on a link to a file (a document, an archive, a recording, or any link
marked `download`) is a download, wherever the file is. The counting script
reports each, and what is kept is where the link led, as its host and path:
never its query, fragment, port or user name, which is where a token or an
address travels. A file on the page's own host is kept by its path alone.
Neither is a custom event or a conversion; each is an event of its visit, so
a visit that ended in one is not a bounce.

### Exit page

The page of a visit's last page view.

### Reading depth

How far down a page its reader scrolled, as the
engagement beacon reports it when the reader leaves: the furthest point seen,
measured when the page has laid out as well as on every scroll, so a page
shorter than the window counts as read to its end. A reading whose reader
left before the beacon could be sent has no depth and is not counted. The
Analytics page gives, for the most read pages, the share of readings that
passed half the page and nine tenths of it. Bots' readings are not counted.

### Readers' speed

The Core Web Vitals the readers' own browsers measured,
each at its 75th percentile by nearest rank (the reading ranked
ceil(0.75 × n)), over a person's page loads alone. Largest paint is a
reading when it is above 0, and response to input when a reader interacted;
a layout shift is one when the browser could measure it, a page that never
shifted reading 0. A measure with no readings is "not reported", never 0.

### What the server measured

Every request a site's visitors made of it,
the shield's refusals among them, counted by how it was answered: answered
(2xx), redirected (3xx), not found (404), refused or bad (the other 4xx) and
server errors (5xx); the console, the API and the server's own endpoints are
the install's, not a site's. Among them, its pages, the HTML answers to a GET
served in full, with their server time, from the request reaching the server
to its answer. Server times are kept as a histogram whose buckets are each at
most a tenth wider than the last, and a percentile is said as "at most" its
bucket's bound, which is within a tenth of it. A post, Home and a topic page
are also counted by the page cache: answered from the copy it keeps, or
rendered for the request because it held none, each
with its mean server time, which is the render time where the two differ.
Counts are kept in memory and written every minute; a stop writes them
first.

### What the content did

Each page sorted into what was measured of it
over the range, never called good or bad: each category is a rule, and each
page in it says its figures and the threshold they met.

- *Many views, little engagement*; *many views, much engagement*; *few
  views, much engagement*: against the site's other pages with 20 views and
  10 readings or more, "many" and "few" the top and bottom quarter of their
  views, "much" and "little" the top and bottom quarter of the share of
  their readings that were engaged (the reader stayed 30 seconds and
  scrolled a quarter of the page). A quarter is taken of 8 pages or more, and
  a page on the median is neither side, so pages all alike have none.
- *Falling*: down by a third or more against the range before, from 20 views
  or more. *Rising*: up by half or more, to 20 views or more.
- *Arrives from search*: half or more of its views, from 20, arrived from a
  search engine. How often a search engine showed the page is counted by the
  search engine, not here.
- *Converts*: the top quarter of the pages viewed in 20 visits or more, by
  the share of those visits with a custom event.

A category the site's pages are too few for says so instead of listing
none. A reading is matched to its page's views by the page's path, without a
trailing slash.

### Retention

Not measured. A visitor is the same only within a day, so
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
for one site (the install's own, or a hosted one, on that site's Analytics)
or for every site together (on every site's Analytics). It fires when the count is
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
- **Server errors** and **requests for pages not found**: the site's
  requests the server answered with a 5xx, or with 404.
- **Average server time for pages**: the mean of its pages' server times,
  in milliseconds. A window with no page in it has nothing to measure: the
  alert is checked and neither fires nor counts it as 0.

The server's figures are read from its minute rows, so a window counts the
minutes that began within it, each once it has ended and been written.

A window ends at the minute the check runs, and each count reads a range of
an index, so a check costs what its window holds. A site that is removed stops
being checked.

## When recording fails

Page views, collected events and reading engagement are counted in memory
and written in batches, never on the request, by bounded queues. What each
queue holds, and what it has dropped and lost since boot, is on
**System › Monitoring › Telemetry**.

- **The queue is full.** A new event is dropped and counted as dropped. The
  page is served as ever.
- **The database refuses a batch** (a lock held past the flush's deadline, a
  failing disk). The batch stays at the front of its queue and is tried again
  on the next flush. After three refusals in a row it is discarded and
  counted as lost, so one row the database will never take cannot hold every
  row behind it. While a batch waits, new events back up into the queue,
  where an overflow is dropped and counted.
- **Read-only mode** (entered when the disk is failing). The queues hold
  their events, up to their caps, and write them when the mode returns.
- **A restart.** Shutdown writes what the queues hold before the database
  closes. A crash loses what was waiting, at most a few seconds' events.
- **A row whose time is not a date** (a mangled restore, a hand edit; no
  writer stores one). Daily groupings leave it out, so the trend and the
  Explorer's split by day still draw, and the daily purge removes it with its
  properties: no retention cutoff would ever reach it.

What the reads cost at a million and three million events is in
[BENCHMARKS.md](BENCHMARKS.md#21-analytics-at-scale).
