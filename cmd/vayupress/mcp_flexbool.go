// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// flexBool is a boolean tool argument that survives a client which sends it as
// a string.
//
// WHY IT EXISTS, from a live call that failed. The tool schema declares
// `{"type": "boolean"}` and the assistant passed `true`, and what arrived on the
// wire was the STRING "true":
//
//	invalid arguments: json: cannot unmarshal string into Go struct field
//	.allow_eval of type bool
//
// The schema is right and the client is wrong, and that is exactly why the
// server cannot rely on it. An MCP server is talking to whatever client somebody
// points at it; a strict decode turns a client's type coercion into a feature the
// operator simply cannot use, with an error message about Go structs. Every
// boolean argument here had the same latent fault — show_blog would have failed
// identically the first time anyone set it.
//
// Deliberately narrow: it accepts a real boolean, or the exact strings a
// coercing client produces. It does NOT treat arbitrary text as true, because
// silently reading "no" as true is a worse failure than refusing it.
type flexBool bool

var errNotABool = errors.New("expected true or false")

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case bool:
		*b = flexBool(v)
		return nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(strings.ToLower(v)))
		if err != nil {
			return errNotABool
		}
		*b = flexBool(parsed)
		return nil
	default:
		return errNotABool
	}
}

// Bool returns the decoded value, or false when the pointer is nil — so a caller
// can treat "omitted" and "false" distinctly by checking the pointer first.
func (b *flexBool) Bool() bool { return b != nil && bool(*b) }
