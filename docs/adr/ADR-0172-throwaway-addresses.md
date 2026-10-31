# ADR-0172 — Throwaway addresses, turned off by any road

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/vayuos/mail/throwaway.go`, `cmd/vayupress/vayuos_mail_throwaway.go`.

## 0. The question

A holder who gives their address to a website cannot take it back when the
website sells it on. An alias helps only if it can be turned off completely:
an alias that still delivers through the domain's catch-all, or that is
filed under its own name, is not off.

## 1. Decision

**A mailbox's holder makes an address for one website in a click** from
Settings (`shop-x7k2qa@` the domain: the website's name cut to sixteen
letters and digits, a dash, and six random characters from an alphabet
without look-alikes), sees which addresses take
mail and how much, turns one off, and removes it. A mailbox holds at most a
hundred (`MaxThrowaways`).

**An address turned off takes nothing by any road.** It is refused at the
door (`ErrAddressOff`, before the message is accepted), it is not filed
under its own name, and the domain's catch-all does not catch it. The same
check (`AliasOff`) is read at every one of those three places.

**Only the mailbox's own throwaway addresses** can be turned off or removed
from it, never an operator's alias.

## 2. Consequences

- A website that sold the address learns only that it no longer exists.
- Throwaway addresses are aliases with a kind; the operator's alias list
  shows them, and the operator can still remove them.
