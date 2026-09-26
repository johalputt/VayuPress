# ADR-0164 — Outside services in a site's Content-Security-Policy

- **Status:** Accepted
- **Date:** 2026-09-26
- **Extends** ADR-0070 (the strict baseline) and ADR-0141 (the Tor world).

## 0. The question

Every public page is sent one strict policy: scripts, styles, fonts and
fetches from the site itself only, with narrow widenings the product foresaw
(vetted video embeds, AdSense on ad pages, eval for an opted-in custom site).
An operator who wants a service the product did not foresee — Google Fonts,
Stripe, a booking widget, analytics — has no way to allow it. The operator
asked for the policy to be configurable without lowering its standard.

## 1. Decision

**Passive content from anywhere the operator names; code only from services
the product has vetted.**

- Each site has its own policy (`csp.policy`, a site setting): the primary,
  and each hosted site on its own scope. The modes are:
  - `strict`, the default, which is the baseline byte for byte;
  - `custom`, which adds the site's allowances;
  - `report`, which sends the policy Report-Only.
- **Typed sources** are for styles, fonts, images, audio and video, embeds
  and form destinations. The grammar is closed:
  - accepted: `https://host`, `https://host:port`, `https://*.host`;
  - refused: keywords, schemes, `http://`, `*`, paths, IP addresses, and any
    quote, space, comma or semicolon.
- **Script and connect origins come only from the service catalogue**
  (`render.cspServices`), whose origins are fixed in code and reviewed. A
  typed script origin is refused. A public CDN is the sharpest case: it serves
  anyone's code, so allowing it lets any injection load a script of its choice.
  The catalogue carries no CDN script entry, and a test holds that.
- **Report-only ends by itself** after seven days, and the allowances are
  enforced again. While it lasts it is a "Needs action" item in the bell.
- **Inline styles** can be allowed. Inline script and eval cannot be allowed
  from here.

## 2. Where it is enforced

- **One merge point.** `siteCSPMiddleware` wraps the response and, at the
  first write, merges the policy into whatever header the handler set:
  baseline, embed, ad or eval. So no page type is missed.
- **Always strict.** It never runs under `strictPathPrefixes`: the console,
  API, OAuth, MCP, member, checkout, sign-up and mail pages. A service runs on
  public pages, never where a session can be read.
- **Tor.** The result still passes `applyOnionCSP`, so the Tor world strips
  every outside origin whatever is chosen.
- **Read-side checks.** A stored policy in an unknown mode is strict. A stored
  source the grammar refuses never reaches the header.

## 3. The blocked list

The report handler records each violation against the site whose page sent
it, and counts distinct visitors (hashed, never stored raw).

- Reports are unauthenticated, so an origin is listed only once two visitors
  have reported it. One forged report cannot put an address of an attacker's
  choosing beside an Allow button.
- Allow asks for confirmation and names the origin.
- A blocked script is answered with the service that covers it, never with a
  typed allowance.

## 4. Not decided here

Code injection (head and footer snippets on the templated blog). Without it,
a templated site uses allowances for what it already loads; a custom site can
use them for anything.
