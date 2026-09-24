# ADR-0163 — Translating the console

- **Status:** Accepted (design); implementation waits for its first language
- **Date:** 2026-09-24
- **Settles** item 3.7 of `docs/UX-AUDIT-2026-08-VAYUMAIL-VAYUTALK.md`
  (string extraction for VayuMail and VayuTalk).

## 0. Where it stands

`internal/i18n` translates the public site: about fifteen keys, chosen by the
visitor's `Accept-Language`, falling back to English. The console has no
language setting, and no console string goes through a catalog.

VayuMail and VayuTalk alone render about 540 distinct text strings from Go and
at least 70 from JavaScript, not counting labels, placeholders and titles.

## 1. Decision

The console is translated per surface, in one release per surface, and only
once there is a language to translate it into. Extracting strings with no
target language adds a lookup to every sentence and translates nothing, so the
work starts when a first language and a person to check it exist. This ADR fixes
how it is done then, so nothing built before then has to be undone.

### 1.1 Which language

The signed-in user's own choice, stored with the user. Without one, the
browser's `Accept-Language`; then English. Never the public site's language: the
person running the console and the people reading the site are different
audiences.

### 1.2 The catalog

The console gets its own catalog in `internal/i18n`, with the same fallback the
public one has (missing key → English → the key itself), under its own key
namespace (`mail.*`, `talk.*`, …). The two catalogs share the lookup code, not
their keys.

### 1.3 Rules for a string

- **Whole sentences are keys.** A sentence built by concatenation cannot be
  translated, because word order differs between languages. Values go in by
  name (`{mailbox}`), not by position.
- **Counts have plural forms.** `3 messages` becomes a key per plural category
  (`.one`, `.other`, and more where a language needs them), chosen by that
  language's rule. English and the first language's rules are added together.
- **A translation is data, never markup.** It is escaped where it is rendered,
  exactly as an English literal is today. A catalog entry that contains `<b>`
  shows `<b>`.

### 1.4 Strings used by scripts

The server renders the page's keys into a non-executing JSON block
(`<script type="application/json" id="vp-i18n">`), which the strict CSP
already allows, and the shell reads them through one helper. Scripts carry no
English of their own once a surface is extracted.

### 1.5 How it is verified

- A gate: every key a surface uses exists in English.
- A pseudo-language (accented and about 40% longer) used in the end-to-end
  run: any English left on the page is an unextracted string, and any overflow
  is a layout that will break in a real language.

## 2. What would change this

A first target language. Mail and Talk go first, because they are the
surfaces a mailbox holder uses who may never see the rest of the console.
