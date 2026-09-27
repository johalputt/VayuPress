// SPDX-License-Identifier: Apache-2.0

package main

// mcp_site_files.go — a hosted static site, managed whole through the
// connector: read it file by file, change some files, or have it built from a
// repository on every push.
//
// build_site replaces a site with files written into one call, as text. That
// covers a page someone asks an assistant to draft. It cannot carry a font or a
// picture (JSON text is not bytes), a change to one page means resending every
// other, and a site of a few megabytes does not fit in a call at all: the
// vayupress.com deploy of 27 September had to go by hand for all three
// reasons. These tools close each one.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/mcp"
)

const (
	// mcpSiteFileListMax bounds one listing; a larger site is listed by folder.
	mcpSiteFileListMax = 2000
	// mcpSiteFileReadMax bounds one read. Base64 grows a file by a third, and
	// the answer has to fit the conversation that asked for it.
	mcpSiteFileReadMax = 4 << 20
	// mcpSiteSyncWait is how long sync_site waits for the build to finish
	// before answering that it is still running. A push of a few pages builds
	// well inside it; a first sync of a large site does not.
	mcpSiteSyncWait = 25 * time.Second
)

func (a *App) registerSiteFileTools(srv *mcp.Server) {
	srv.Register(mcp.Tool{
		Name: "list_site_files",
		Description: "List the files of a hosted domain's live uploaded site, with their sizes. Use it before " +
			"read_site_file or edit_site_files. Narrow a large site with prefix (a folder such as \"assets/\").",
		InputSchema: objSchema([]string{"host"}, map[string]any{
			"host":   strProp("The hosted domain, exactly as list_sites reports it."),
			"prefix": strProp("Only files whose path starts with this."),
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Host, Prefix string }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			all, err := customsite.Files(scopedBundleDir(d))
			if err != nil {
				return "", err
			}
			files := []customsite.File{}
			var total int64
			for _, f := range all {
				if strings.HasPrefix(f.Path, in.Prefix) {
					files = append(files, f)
					total += f.Size
				}
			}
			resp := map[string]any{"host": d.Host, "count": len(files), "bytes": total}
			if len(files) > mcpSiteFileListMax {
				files = files[:mcpSiteFileListMax]
				resp["truncated"] = "only the first " + itoaSafe(mcpSiteFileListMax) + " are listed; narrow with prefix"
			}
			resp["files"] = files
			if len(all) == 0 {
				resp["note"] = d.Host + " has no uploaded site yet; edit_site_files with an index.html starts one"
			} else {
				resp["deployed_at"] = customsite.ReadManifest(scopedBundleDir(d)).DeployedAt
			}
			if f := a.siteFollowReport(d); f != nil {
				resp["follows"] = f
			}
			return jsonStr(resp), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "read_site_file",
		Description: "Read one file of a hosted domain's live uploaded site. Text comes back as it is; anything " +
			"else (an image, a font) as base64, with encoding saying which.",
		InputSchema: objSchema([]string{"host", "path"}, map[string]any{
			"host": strProp("The hosted domain, exactly as list_sites reports it."),
			"path": strProp("The file's path in the site, as list_site_files gives it: \"index.html\", \"assets/site.css\"."),
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Host, Path string }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			data, err := customsite.ReadFile(scopedBundleDir(d), in.Path)
			if err != nil {
				return "", err
			}
			if len(data) > mcpSiteFileReadMax {
				return "", errors.New("the file is " + humanBytes(int64(len(data))) + ", more than one read carries; change it with edit_site_files, or build the site from a repository")
			}
			resp := map[string]any{"host": d.Host, "path": in.Path, "size": len(data)}
			if utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
				resp["encoding"], resp["content"] = "text", string(data)
			} else {
				resp["encoding"], resp["content"] = "base64", base64.StdEncoding.EncodeToString(data)
			}
			return jsonStr(resp), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "edit_site_files",
		Description: "Change some files of a hosted domain's live uploaded site and publish the result at once: " +
			"files writes text files (new or replacing), files_base64 writes any file sent as base64 (an image, a " +
			"font), delete removes files. Every other file stays as it is. The site before the edit is kept and " +
			"restore_previous_site brings it back. On a domain with no site yet, files must include index.html.",
		InputSchema: objSchema([]string{"host"}, map[string]any{
			"host": strProp("The hosted domain, exactly as list_sites reports it."),
			"files": map[string]any{
				"type":                 "object",
				"description":          "Path to text contents, e.g. {\"index.html\": \"<!doctype html>…\"}.",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"files_base64": map[string]any{
				"type":                 "object",
				"description":          "Path to base64 contents, for files that are not text, e.g. {\"img/logo.png\": \"iVBORw0…\"}.",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"delete": arrProp("Paths to remove from the site."),
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Host        string            `json:"host"`
				Files       map[string]string `json:"files"`
				FilesBase64 map[string]string `json:"files_base64"`
				Delete      []string          `json:"delete"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			if err := mcpSiteWritable(d); err != nil {
				return "", err
			}
			if f, ok := d.Follow(); ok {
				return "", errFollowedSite(d, f)
			}
			put := make(map[string][]byte, len(in.Files)+len(in.FilesBase64))
			for p, text := range in.Files {
				put[p] = []byte(text)
			}
			for p, b64 := range in.FilesBase64 {
				if _, twice := put[p]; twice {
					return "", bundleError(p + " is in both files and files_base64")
				}
				data, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					return "", bundleError(p + " is not valid base64: " + err.Error())
				}
				put[p] = data
			}
			m, err := customsite.Edit(scopedBundleDir(d), put, in.Delete, bundleBudget())
			if errors.Is(err, customsite.ErrNoSpace) {
				return "", bundleError("the edited site is larger than the free space on the server")
			}
			if err != nil {
				return "", err
			}
			if err := a.servePublishedBundle(ctx, d, mcpActor(ctx),
				"edited: "+itoaSafe(len(put))+" written, "+itoaSafe(len(in.Delete))+" deleted, "+itoaSafe(m.Files)+" live via=mcp"); err != nil {
				return "", err
			}
			resp := map[string]any{
				"status": "published", "host": d.Host, "written": len(put), "deleted": len(in.Delete),
				"files": m.Files, "bytes": m.Bytes, "url": "https://" + d.Host + "/",
			}
			if w := cspBundleWarnings(in.Files); len(w) > 0 {
				resp["csp_warnings"] = w
			}
			return jsonStr(resp), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "follow_repository",
		Description: "Build a hosted domain's website from a public GitHub repository, and rebuild it whenever the " +
			"branch moves (the install looks every five minutes; sync_site publishes at once). With dir, that folder " +
			"of the repository is the site, served as it is. Without dir, the repository's docs/ and CHANGELOG.md " +
			"are rendered as a documentation site (VayuPress's own: site \"updates\" renders the release mirror's " +
			"page instead). Use this for a site of any size: push it, then call sync_site. An empty repo stops " +
			"following and leaves the live site as it is.",
		InputSchema: objSchema([]string{"host", "repo"}, map[string]any{
			"host":   strProp("The hosted domain, exactly as list_sites reports it."),
			"repo":   strProp("owner/name on GitHub, e.g. \"johalputt/vayupress\". Empty to stop following."),
			"branch": strProp("The branch to follow. Default main."),
			"dir":    strProp("The folder that is the site, e.g. \"site\" or \"docs/site/public\". Omit to render the docs."),
			"site":   strProp("Without dir only: empty for the product site, \"updates\" for the release mirror's page."),
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Host, Repo, Branch, Dir, Site string }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			if config.Cfg.OnionMode {
				return "", errors.New("a Tor Space makes no clearnet requests, so it cannot fetch a repository")
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			if err := mcpSiteWritable(d); err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Repo) == "" {
				if err := a.domains.SetFollow(ctx, d.ID, nil); err != nil {
					return "", err
				}
				auditSiteFollow(ctx, d, "stopped following")
				return jsonStr(map[string]any{"status": "stopped", "host": d.Host,
					"note": "The live site stays as the last build left it, and can now be edited by hand."}), nil
			}
			f := domain.Follow{Repo: strings.TrimSpace(in.Repo), Branch: strings.TrimSpace(in.Branch),
				Dir: strings.Trim(strings.TrimSpace(in.Dir), "/"), Site: strings.TrimSpace(in.Site)}
			if f.Branch == "" {
				f.Branch = "main"
			}
			if err := validateFollow(f); err != nil {
				return "", err
			}
			if err := a.domains.SetFollow(ctx, d.ID, &f); err != nil {
				return "", err
			}
			auditSiteFollow(ctx, d, "following "+f.Repo+"@"+f.Branch+" dir="+f.Dir+" site="+f.Site)
			d, err = a.domains.ByID(ctx, d.ID)
			if err != nil {
				return "", err
			}
			return a.mcpSiteSync(d, "following")
		},
	})

	srv.Register(mcp.Tool{
		Name: "sync_site",
		Description: "Rebuild a domain that follows a repository from its branch now, rather than at the next " +
			"five-minute look, and publish it. Waits for the build when it is quick; otherwise answers that it is " +
			"running, and get_site's follows reports the outcome.",
		InputSchema: objSchema([]string{"host"}, map[string]any{
			"host": strProp("The hosted domain, exactly as list_sites reports it."),
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Host string }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			if config.Cfg.OnionMode {
				return "", errors.New("a Tor Space makes no clearnet requests, so it cannot fetch a repository")
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			if err := mcpSiteWritable(d); err != nil {
				return "", err
			}
			if _, ok := d.Follow(); !ok {
				return "", siteLookupError(d.Host + " does not follow a repository; follow_repository sets one")
			}
			return a.mcpSiteSync(d, "synced")
		},
	})
}

// mcpSiteSync starts a build and waits a little for it, so the common case (a
// push of a few files) answers with the outcome rather than "started".
func (a *App) mcpSiteSync(d domain.Domain, status string) (string, error) {
	done := a.kickSiteSync(d)
	select {
	case <-done:
	case <-time.After(mcpSiteSyncWait):
		status = "building"
	}
	resp := map[string]any{"status": status, "host": d.Host, "follows": a.siteFollowReport(d)}
	if status == "building" {
		resp["note"] = "The build is still running; get_site reports it when it is done."
	}
	return jsonStr(resp), nil
}

func auditSiteFollow(ctx context.Context, d domain.Domain, what string) {
	dbpkg.AuditLog("vayudomains.website.follow", mcpActor(ctx), d.Host, what+" via=mcp")
}
