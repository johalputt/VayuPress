package main

// usercli.go — `vayupress user <add|list|passwd|delete>` CLI for bootstrapping
// and managing accounts without needing an existing logged-in session. This is
// the only way to create the first admin on a fresh install.

import (
	"context"
	"fmt"
	"io"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/users"
)

// runUserCLI dispatches the user subcommands. It assumes config + DB are
// already initialised by the caller.
func runUserCLI(args []string, out io.Writer) error {
	store := users.New(dbpkg.DB)
	ctx := context.Background()
	if len(args) == 0 {
		return userUsage()
	}
	switch args[0] {
	case "add":
		// vayupress user add <email> <password> [name...] [--admin]
		rest := args[1:]
		admin := false
		var filtered []string
		for _, a := range rest {
			if a == "--admin" {
				admin = true
				continue
			}
			filtered = append(filtered, a)
		}
		if len(filtered) < 2 {
			return fmt.Errorf("usage: vayupress user add <email> <password> [name...] [--admin]")
		}
		email, password := filtered[0], filtered[1]
		name := strings.Join(filtered[2:], " ")
		role := users.RoleAuthor
		if admin {
			role = users.RoleAdmin
		}
		u, err := store.Create(ctx, email, name, password, role)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "created %s (%s) id=%s\n", u.Email, u.Role, u.ID)
		return nil

	case "list":
		list, err := store.List(ctx)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintln(out, "no users")
			return nil
		}
		for _, u := range list {
			last := "never"
			if u.LastLogin != nil {
				last = u.LastLogin.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(out, "%-32s %-7s last-login=%s\n", u.Email, u.Role, last)
		}
		return nil

	case "passwd":
		// vayupress user passwd <email> <new-password>
		if len(args) < 3 {
			return fmt.Errorf("usage: vayupress user passwd <email> <new-password>")
		}
		if err := store.SetPassword(ctx, args[1], args[2]); err != nil {
			return err
		}
		fmt.Fprintf(out, "password updated for %s\n", args[1])
		return nil

	case "delete":
		// vayupress user delete <email>
		if len(args) < 2 {
			return fmt.Errorf("usage: vayupress user delete <email>")
		}
		if err := store.Delete(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintf(out, "deleted %s\n", args[1])
		return nil

	default:
		return userUsage()
	}
}

func userUsage() error {
	return fmt.Errorf("usage: vayupress user <add|list|passwd|delete> ...\n" +
		"  add <email> <password> [name...] [--admin]\n" +
		"  list\n" +
		"  passwd <email> <new-password>\n" +
		"  delete <email>")
}
