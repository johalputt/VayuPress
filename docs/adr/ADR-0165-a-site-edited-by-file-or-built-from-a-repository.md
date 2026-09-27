# ADR-0165 — A site edited file by file, or built from a repository

- **Status:** Accepted
- **Date:** 2026-09-27
- **Extends** ADR-0154 D12 (a hand-built website for a hosted domain).

## 0. The question

A hosted domain can serve an uploaded static site, and the connector could
publish one with `build_site`: every file written into one call, as text, and
the whole site replaced. On 27 September the new vayupress.com could not be
published that way, for three separate reasons:

- **Pictures and fonts cannot pass.** JSON text is not bytes.
- **One changed page means resending every other one.**
- **A site of a few megabytes does not fit in a call.** vayupress.com is
  10.5 MB in 815 pages.

The operator asked that any update or deployment of a static site be possible
through the connector, with nothing done by hand.

## 1. Decision

**Two ways in, both through the one deploy path.**

### Edit the live site file by file

The connector gains three tools:

- `list_site_files` lists the live site.
- `read_site_file` returns a file: text as it is, anything else as base64.
- `edit_site_files` writes some files (text, or base64 for pictures and
  fonts) and deletes others, leaving every other file as it is.

`customsite.Edit` makes the edited site into a new bundle and deploys it
through the same `deployLocked` an upload takes. The bundle it replaces joins
the history, so `restore_previous_site` undoes the edit. The deploy lock is
held from reading the live site to replacing it, so an upload that finishes
meanwhile is not silently reverted.

Rejected: writing the changed files into the live directory. It is quicker,
but it is a second deploy path, and a half-applied edit would be live between
one file and the next.

### Build the site from a repository

A domain can **follow** a public GitHub repository at a branch
(`follow_repository`). The install looks at the branch every five minutes, and
at once on `sync_site`. When the branch has moved, the install:

1. fetches what changed and verifies each file against the SHA-1 the commit
   names;
2. builds the site;
3. deploys it through `customsite.Deploy`.

What gets built depends on the follow:

- **With a folder:** that folder is the site, served as it is. A file a site
  cannot carry (a README beside the pages) is left out and named in `get_site`,
  rather than stopping every deploy.
- **Without a folder:** the repository's `docs/` and `CHANGELOG.md` are
  rendered by `internal/docsite`. This is the product site, or with
  `site: "updates"` the release mirror's page.

A docs site is drawn with the repository's own design when the repository
keeps one in `docs/site/theme/` (its templates, stylesheet and scripts), so a
new look is a push, not a release. The theme replaces the built-in design
whole. A theme that lacks a file is refused with the file's name; the build
never draws half of one design and half of the other.

The two docs sites find each other on the install itself:

- The product site's Download opens the domain serving the mirror.
- The mirror's page links back to the domain that follows the docs as the
  product site, and is not built while there is none.

**The install pulls; nothing pushes to it.** No key that can write to
production is kept in GitHub, and a site of any size is deployed by a push,
which no connector call could carry. The same reasoning made the release
mirror pull (updates.vayupress.com).

A site that follows a repository refuses `build_site` and `edit_site_files`.
The error names the repository and says how to stop following: the next push
would rebuild the site without the edit, which would then be gone with nothing
to say so.

## 2. What enforces it

| Rule | Mechanism | Test |
|---|---|---|
| An edit changes only the files it names, and can be undone | `customsite.Edit` → `deployLocked` | `TestEditChangesOnlyWhatItNames` |
| An edit is refused, with its reason, for a bad path, a non-web file, system junk, a missing delete, or no front page | `customsite.Edit`, `safeRel`, `ExtAllowed`, `ignorableJunk` | `TestEditRefusesAndLeavesTheSiteAsItWas` (one seed per rule) |
| A file whose contents are not what the commit names is not published | `Syncer.fetch` SHA-1 check | `TestAFileThatIsNotWhatTheCommitNamesIsRefused` |
| A changed source starts from an empty working copy | `Syncer.Sync` | `TestASiteThatIsAFolderOfTheRepository` |
| A followed site cannot be edited by hand | `errFollowedSite` in both write tools | `TestASiteFollowsAFolderOfARepository` |
| No publish to a disabled or held domain | `mcpSiteWritable` in `edit_site_files`, `follow_repository`, `sync_site` | `TestNoSiteToolPublishesToADisabledDomain` |
| No clearnet fetch in a Tor Space (ADR-0141) | `config.Cfg.OnionMode` in the loop and both tools | `TestFollowingRefusesWhatCannotBeFollowed` |
| A repository's theme draws the site whole, and a theme missing a file is refused by name | `docsite.theme`, `nameAsset` | `TestARepositoryThemeDrawsTheSite` |
| The footer links only pages the repository has | `site.Pages` in `layout.html` | `TestTheSitesOwnPagesKeepTheirAddressesAndFigures`, `TestTheRealSiteHasNoBrokenLinkAndNothingFromElsewhere` |
| A read-only key reaches none of the three writers | `mcpVisible(SectionDomains, ActionWrite)` | `TestTheMutatingToolListCoversEveryToolNeedingMoreThanRead` |

## 3. Limits

- **Public repositories only.** The sync reads GitHub without credentials.
- **A tree of more than 100,000 entries is refused.** GitHub truncates the
  listing past that, and a partial site is never published.
- **One file is bounded at 32 MiB.** A mistaken commit of a disk image cannot
  fill the server.
