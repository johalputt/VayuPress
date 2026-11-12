// SPDX-License-Identifier: Apache-2.0

package mail

// davfiles.go — a mailbox's Drive over WebDAV, so the file apps of a
// computer or a phone open it as a folder:
//
//	/dav/files/<folder>/<file>
//
// signed in as CardDAV and CalDAV are (DAVHandler), by the mailbox's own
// holder; the mailbox is the one the sign-in opens, never one a path names.
// It is the same store as the Drive page (drive.go), reached by names
// instead of numbers: every rule there (the allowance, the sizes, sealing,
// versions on a file saved over) holds here because the same functions do
// the work.
//
// What an app deletes goes to Trash, as on the page, so a sync that goes
// wrong is undone for thirty days. A file is always handed back as bytes
// to keep (octet-stream, attachment, nosniff, a policy that runs nothing):
// /dav/files is on the site's own origin, and a browser led to an uploaded
// HTML file there must not be shown a page.
//
// Locks are not offered (WebDAV class 1), which the apps that only read and
// write files do not need.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/emersion/go-webdav"
)

const (
	davFilesRoot = "/dav/files"
	// maxDAVListing bounds one PROPFIND that asks for everything below a
	// folder.
	maxDAVListing = 5000
)

type davFiles struct{ e *Engine }

// errDAVFolderMissing is a folder on a path that is not there: a 404 to a
// read (davRead) and a 409 to a write (davNoFolder).
var errDAVFolderMissing = webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)

// davFileErr gives a store refusal the status an app understands.
func davFileErr(err error) error {
	code := 0
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrDriveNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ErrDriveName):
		code = http.StatusBadRequest
	case errors.Is(err, ErrDriveExists):
		code = http.StatusMethodNotAllowed
	case errors.Is(err, ErrDriveFull):
		code = http.StatusInsufficientStorage
	case errors.Is(err, ErrDriveTooLarge):
		code = http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrDriveLocked):
		code = http.StatusLocked
	case errors.Is(err, ErrDriveBusy):
		code = http.StatusServiceUnavailable
	case errors.Is(err, ErrDriveMove), errors.Is(err, ErrDriveHolderOnly):
		code = http.StatusForbidden
	default:
		return err
	}
	return webdav.NewHTTPError(code, err)
}

// davNames is a path under /dav/files as the names it walks, or false for
// a path that is not under it.
func davNames(p string) ([]string, bool) {
	if p != davFilesRoot && !strings.HasPrefix(p, davFilesRoot+"/") {
		return nil, false
	}
	var names []string
	for _, part := range strings.Split(strings.TrimPrefix(p, davFilesRoot), "/") {
		if part != "" {
			names = append(names, part)
		}
	}
	return names, true
}

// davNoFolder is a path whose folder is not there. Read, it is not found;
// written to, it is a conflict, the protocol's word for a write into a
// folder that does not exist.
func davNoFolder(err error) error {
	if errors.Is(err, ErrDriveNotFound) {
		return webdav.NewHTTPError(http.StatusConflict, ErrDriveNotFound)
	}
	return err
}

// find walks a path from the top of the requester's own Drive: the folder
// its last name is in, and the entry of that name if there is one.
func (f davFiles) find(ctx context.Context, p string) (rd Reader, mbox string, parent int64, last string, entry DriveEntry, found bool, err error) {
	names, ok := davNames(p)
	mbox = davFrom(ctx).mailbox
	if !ok || mbox == "" || len(names) > maxDriveDepth+1 {
		return rd, mbox, 0, "", entry, false, webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)
	}
	rd = ReadAsOwner(mbox)
	if _, err := f.e.driveOf(rd); err != nil {
		return rd, mbox, 0, "", entry, false, davFileErr(err)
	}
	if len(names) == 0 {
		return rd, mbox, 0, "", DriveEntry{IsDir: true}, true, nil
	}
	for _, name := range names[:len(names)-1] {
		d, ok := f.e.driveNamed(ctx, mbox, parent, name)
		if !ok || !d.IsDir {
			return rd, mbox, 0, "", entry, false, errDAVFolderMissing
		}
		parent = d.ID
	}
	last = names[len(names)-1]
	entry, found = f.e.driveNamed(ctx, mbox, parent, last)
	return rd, mbox, parent, last, entry, found, nil
}

// davInfo is an entry as an app is told of it. Every file is octet-stream:
// what a sender or an upload called it is not what this origin serves it as.
func davInfo(p string, d DriveEntry) webdav.FileInfo {
	p = "/" + strings.Trim(p, "/")
	if d.IsDir {
		return webdav.FileInfo{Path: p + "/", IsDir: true, ModTime: d.Modified}
	}
	return webdav.FileInfo{Path: p, Size: d.Size, ModTime: d.Modified, MIMEType: "application/octet-stream",
		ETag: davETagOf(d)}
}

func (f davFiles) Stat(ctx context.Context, name string) (*webdav.FileInfo, error) {
	_, _, _, _, d, found, err := f.find(ctx, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)
	}
	fi := davInfo(name, d)
	return &fi, nil
}

func (f davFiles) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	rd, _, _, _, d, found, err := f.find(ctx, name)
	if err != nil {
		return nil, err
	}
	if !found || d.IsDir {
		return nil, webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)
	}
	_, file, err := f.e.DriveOpen(rd, d.ID)
	return file, davFileErr(err)
}

func (f davFiles) ReadDir(ctx context.Context, name string, recursive bool) ([]webdav.FileInfo, error) {
	rd, _, _, _, d, found, err := f.find(ctx, name)
	if err != nil {
		return nil, err
	}
	if !found || !d.IsDir {
		return nil, webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)
	}
	var out []webdav.FileInfo
	var walk func(id int64, at string) error
	walk = func(id int64, at string) error {
		list, err := f.e.DriveList(rd, id)
		if err != nil {
			return davFileErr(err)
		}
		for _, c := range list {
			if len(out) >= maxDAVListing {
				return webdav.NewHTTPError(http.StatusForbidden, errors.New("vayumail: too many files to list at once; ask for one folder at a time"))
			}
			p := strings.TrimRight(at, "/") + "/" + c.Name
			out = append(out, davInfo(p, c))
			if recursive && c.IsDir {
				if err := walk(c.ID, p); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(d.ID, name); err != nil {
		return nil, err
	}
	return out, nil
}

func (f davFiles) Create(ctx context.Context, name string, body io.ReadCloser, opts *webdav.CreateOptions) (*webdav.FileInfo, bool, error) {
	rd, mbox, parent, last, d, found, err := f.find(ctx, name)
	if err != nil {
		return nil, false, davNoFolder(err)
	}
	if last == "" || (found && d.IsDir) {
		return nil, false, webdav.NewHTTPError(http.StatusMethodNotAllowed, ErrDriveExists)
	}
	if opts != nil {
		// The two conditions an app that syncs sends: only if it is new, and
		// only if it is still the version the app last saw.
		// A tag that does not parse matches nothing, so a malformed If-Match
		// refuses the write rather than letting it through (as davConditionOf).
		if opts.IfNoneMatch.IsSet() && found {
			if same, _ := opts.IfNoneMatch.MatchETag(davInfo(name, d).ETag); same {
				return nil, false, webdav.NewHTTPError(http.StatusPreconditionFailed, ErrDriveExists)
			}
		}
		if opts.IfMatch.IsSet() {
			same := false
			if found {
				same, _ = opts.IfMatch.MatchETag(davInfo(name, d).ETag)
			}
			if !same {
				return nil, false, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("vayumail: the file has changed"))
			}
		}
	}
	id, err := f.e.DrivePut(rd, parent, last, "", body)
	if err != nil {
		// A folder on the way that is not there is a conflict, by the
		// protocol's own word for it.
		if errors.Is(err, ErrDriveNotFound) {
			return nil, false, webdav.NewHTTPError(http.StatusConflict, err)
		}
		return nil, false, davFileErr(err)
	}
	kept, err := f.e.driveEntry(ctx, mbox, id)
	if err != nil {
		return nil, false, davFileErr(err)
	}
	fi := davInfo(name, kept)
	return &fi, !found, nil
}

// RemoveAll puts the entry in Trash: an app's delete is the page's.
func (f davFiles) RemoveAll(ctx context.Context, name string, _ *webdav.RemoveAllOptions) error {
	rd, _, _, last, d, found, err := f.find(ctx, name)
	if err != nil {
		return err
	}
	if last == "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("vayumail: the top of the Drive is not deleted"))
	}
	if !found {
		return webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)
	}
	return davFileErr(f.e.DriveTrash(rd, d.ID, false))
}

func (f davFiles) Mkdir(ctx context.Context, name string) error {
	rd, _, parent, last, _, found, err := f.find(ctx, name)
	if err != nil {
		return davNoFolder(err)
	}
	if last == "" || found {
		return webdav.NewHTTPError(http.StatusMethodNotAllowed, ErrDriveExists)
	}
	_, err = f.e.DriveMkdir(rd, parent, last)
	return davFileErr(err)
}

// Copy copies one file. A folder is not copied: an app that wants one
// copies its files, and a copy of everything below a folder in one request
// is work with no bound a holder set.
func (f davFiles) Copy(ctx context.Context, name, dest string, opts *webdav.CopyOptions) (bool, error) {
	rd, _, _, _, d, found, err := f.find(ctx, name)
	if err != nil {
		return false, err
	}
	if !found {
		return false, webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)
	}
	if d.IsDir {
		return false, webdav.NewHTTPError(http.StatusForbidden, errors.New("vayumail: a folder is not copied; copy its files"))
	}
	_, _, parent, last, at, taken, err := f.find(ctx, dest)
	if err != nil {
		return false, davNoFolder(err)
	}
	if last == "" || (taken && at.IsDir) {
		return false, webdav.NewHTTPError(http.StatusMethodNotAllowed, ErrDriveExists)
	}
	if taken && opts != nil && opts.NoOverwrite {
		return false, os.ErrExist
	}
	_, file, err := f.e.DriveOpen(rd, d.ID)
	if err != nil {
		return false, davFileErr(err)
	}
	defer file.Close()
	if _, err := f.e.DrivePut(rd, parent, last, d.Type, file); err != nil {
		if errors.Is(err, ErrDriveNotFound) {
			return false, webdav.NewHTTPError(http.StatusConflict, err)
		}
		return false, davFileErr(err)
	}
	return !taken, nil
}

// Move renames an entry, moves it to another folder, or both. What is at
// the destination, when the app says to write over it, goes to Trash first.
func (f davFiles) Move(ctx context.Context, name, dest string, opts *webdav.MoveOptions) (bool, error) {
	rd, _, _, from, d, found, err := f.find(ctx, name)
	if err != nil {
		return false, err
	}
	if from == "" {
		return false, webdav.NewHTTPError(http.StatusForbidden, errors.New("vayumail: the top of the Drive is not moved"))
	}
	if !found {
		return false, webdav.NewHTTPError(http.StatusNotFound, ErrDriveNotFound)
	}
	_, _, parent, last, at, taken, err := f.find(ctx, dest)
	if err != nil {
		return false, davNoFolder(err)
	}
	if last == "" {
		return false, webdav.NewHTTPError(http.StatusForbidden, ErrDriveExists)
	}
	if taken && at.ID != d.ID {
		if opts != nil && opts.NoOverwrite {
			return false, os.ErrExist
		}
		if err := f.e.DriveTrash(rd, at.ID, false); err != nil {
			return false, davFileErr(err)
		}
	}
	if err := f.e.DriveMoveAs(rd, d.ID, parent, last); err != nil {
		return false, davFileErr(err)
	}
	return !taken, nil
}

// davETagOf is a file's ETag: its content's SHA-256 when it has one, so an
// app that kept a copy can tell it is the same bytes; else its number, its
// time and its size, as for a file kept before sums were.
func davETagOf(d DriveEntry) string {
	if d.Sum != "" {
		return d.Sum
	}
	return fmt.Sprintf("%x-%x-%x", d.ID, d.Modified.UnixNano(), d.Size)
}
