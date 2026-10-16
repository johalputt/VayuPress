// SPDX-License-Identifier: Apache-2.0

package render

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// A diagram an author draws in a post as inline <svg> is shown as a drawing.
//
// The article policy (bluemonday's UGC) knows no SVG, so it dropped every
// diagram, and on johal.in each of a series of guides lost its figure. A
// diagram is lifted out of the post before that policy runs, as highlighted
// code is (renderContentHTML), made fit for a page with a strict CSP, held to
// svgPolicy, and put back where it stood.
//
// "Fit for a page" is three things, each because of what the posts carry:
//   - their styling is in style="" (the root's max-width, background and
//     corner, a text's fill), which the CSP's style-src refuses; the
//     declarations that SVG also takes as attributes become attributes, and a
//     root's background becomes a backing rectangle, or light labels drawn for
//     a dark ground are lost on a light page;
//   - their ids (a marker "arrk", repeated in every diagram) would collide
//     with each other and with the page's anchors, so each diagram's ids are
//     prefixed with its place in the post, and its url(#…) follow;
//   - the root takes class vp-svg, which the themes size to the column.
//
// A diagram is found by parsing the post, never by a pattern over its text:
// "<svg" inside an attribute value is not an element, and markup put back
// there would close the attribute and write the rest into the tag.

// svgPresentation are the properties SVG accepts both in style and as
// attributes, and so the declarations kept from style="".
var svgPresentation = map[string]bool{
	"fill": true, "fill-opacity": true, "fill-rule": true, "stroke": true,
	"stroke-width": true, "stroke-opacity": true, "stroke-dasharray": true,
	"stroke-linecap": true, "stroke-linejoin": true, "opacity": true,
	"font-family": true, "font-size": true, "font-weight": true, "font-style": true,
	"text-anchor": true, "dominant-baseline": true, "letter-spacing": true,
	"stop-color": true, "stop-opacity": true,
}

var (
	// svgPaint is a colour, none, currentColor, or a reference to a gradient or
	// marker in the same drawing. Nothing else: no url() off the page, no
	// expression.
	svgPaint = regexp.MustCompile(`^(?:none|currentColor|transparent|#[0-9a-fA-F]{3,8}|[a-zA-Z]{3,20}|(?:rgb|hsl)a?\([0-9.,% ]{1,40}\)|url\(#[A-Za-z][\w.-]{0,63}\))$`)
	svgRef   = regexp.MustCompile(`^url\(#[A-Za-z][\w.-]{0,63}\)$`)
	svgNum   = regexp.MustCompile(`^-?[0-9.]{1,12}(?:%|px|em)?$`)
	svgNums  = regexp.MustCompile(`^[-0-9., ]+$`)
	svgPath  = regexp.MustCompile(`^[MmLlHhVvCcSsQqTtAaZz0-9.,\-+eE \n\t]+$`)
	svgXform = regexp.MustCompile(`^(?:(?:matrix|translate|scale|rotate|skewX|skewY)\([-0-9., e]{1,120}\)\s*){1,8}$`)
	svgFont  = regexp.MustCompile(`^[\w ,"'-]{1,120}$`)
	svgWord  = regexp.MustCompile(`^[a-zA-Z-]{1,30}$`)
	svgID    = regexp.MustCompile(`^[A-Za-z][\w.-]{0,80}$`)
	urlRefRe = regexp.MustCompile(`url\(#([A-Za-z][\w.-]{0,63})\)`)
	radiusRe = regexp.MustCompile(`^([0-9.]{1,8})(?:px)?$`)
)

// svgPolicy is the closed allowlist a post's diagram is held to: shapes,
// text, groups, markers, gradients and clip paths, with their geometry and
// paint. No script, style, foreignObject, image, use, a, animation or event
// handler can survive it, and every value that could name a resource is a
// reference inside the drawing.
var svgPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	// Script and style content bluemonday drops by itself; a foreignObject's
	// HTML would otherwise be left as loose text in the drawing.
	p.SkipElementsContent("foreignObject", "foreignobject")
	p.AllowElements("svg", "g", "defs", "marker", "path", "rect", "circle", "ellipse",
		"line", "polyline", "polygon", "text", "tspan", "title", "desc",
		"linearGradient", "radialGradient", "stop", "clipPath")
	p.AllowAttrs("xmlns").Matching(regexp.MustCompile(`^http://www\.w3\.org/2000/svg$`)).OnElements("svg")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^vp-svg$`)).OnElements("svg")
	p.AllowAttrs("role").Matching(regexp.MustCompile(`^(?:img|presentation|group)$`)).OnElements("svg", "g")
	p.AllowAttrs("aria-label").Matching(regexp.MustCompile(`^[^<>]{0,300}$`)).OnElements("svg", "g")
	p.AllowAttrs("aria-hidden").Matching(regexp.MustCompile(`^(?:true|false)$`)).Globally()
	p.AllowAttrs("viewBox").Matching(svgNums).OnElements("svg", "marker")
	p.AllowAttrs("preserveAspectRatio").Matching(regexp.MustCompile(`^[a-zA-Z ]{1,30}$`)).OnElements("svg", "marker")
	p.AllowAttrs("id").Matching(svgID).Globally()
	p.AllowAttrs("x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "width", "height",
		"dx", "dy", "refX", "refY", "markerWidth", "markerHeight", "fx", "fy", "offset",
		"font-size", "stroke-width", "opacity", "fill-opacity", "stroke-opacity", "stop-opacity", "letter-spacing").
		Matching(svgNum).Globally()
	p.AllowAttrs("points").Matching(svgNums).OnElements("polyline", "polygon")
	p.AllowAttrs("d").Matching(svgPath).OnElements("path")
	p.AllowAttrs("transform", "gradientTransform").Matching(svgXform).Globally()
	p.AllowAttrs("fill", "stroke", "stop-color").Matching(svgPaint).Globally()
	p.AllowAttrs("marker-start", "marker-mid", "marker-end", "clip-path").Matching(svgRef).Globally()
	p.AllowAttrs("stroke-dasharray").Matching(svgNums).Globally()
	p.AllowAttrs("font-family").Matching(svgFont).Globally()
	p.AllowAttrs("font-weight", "font-style", "text-anchor", "dominant-baseline", "stroke-linecap",
		"stroke-linejoin", "fill-rule", "orient", "markerUnits", "gradientUnits", "clipPathUnits").
		Matching(svgWord).Globally()
	return p
}()

// liftDiagrams replaces each top-level <svg> in raw with a token and returns
// the post and the sanitised drawing for each token. A post without "<svg"
// is returned as it came, so no other post's markup is reparsed.
func liftDiagrams(raw, nonce string) (string, map[string]string) {
	if !strings.Contains(strings.ToLower(raw), "<svg") {
		return raw, nil
	}
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(raw), body)
	if err != nil {
		return raw, nil
	}
	drawings := map[string]string{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode && c.Namespace == "svg" && c.Data == "svg" {
				token := "VAYUSVG" + nonce + strconv.Itoa(len(drawings)) + "ENDVAYUSVG"
				drawings[token] = sanitiseDiagram(c, len(drawings))
				n.InsertBefore(&html.Node{Type: html.TextNode, Data: token}, c)
				n.RemoveChild(c)
			} else {
				walk(c)
			}
			c = next
		}
	}
	var out strings.Builder
	for _, n := range nodes {
		holder := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
		holder.AppendChild(n)
		walk(holder)
		for c := holder.FirstChild; c != nil; c = c.NextSibling {
			if err := html.Render(&out, c); err != nil {
				return raw, nil
			}
		}
	}
	return out.String(), drawings
}

// sanitiseDiagram makes one drawing fit for the page (see the file comment)
// and holds it to svgPolicy. n is its place in the post.
func sanitiseDiagram(root *html.Node, n int) string {
	prefix := "vp-d" + strconv.Itoa(n) + "-"
	ids := map[string]bool{}
	var collect func(*html.Node)
	collect = func(e *html.Node) {
		for i := range e.Attr {
			if e.Attr[i].Key == "id" {
				ids[e.Attr[i].Val] = true
			}
		}
		for c := e.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(root)

	var background, radius string
	var fit func(*html.Node)
	fit = func(e *html.Node) {
		if e.Type == html.ElementNode {
			var kept []html.Attribute
			have := map[string]bool{}
			for _, a := range e.Attr {
				have[a.Key] = true
			}
			for _, a := range e.Attr {
				switch {
				case a.Key == "style":
					for _, decl := range strings.Split(a.Val, ";") {
						prop, val, ok := strings.Cut(decl, ":")
						prop, val = strings.ToLower(strings.TrimSpace(prop)), strings.TrimSpace(val)
						if !ok || val == "" {
							continue
						}
						switch {
						case e == root && (prop == "background" || prop == "background-color"):
							background = val
						case e == root && prop == "border-radius":
							radius = val
						case svgPresentation[prop] && !have[prop]:
							kept = append(kept, html.Attribute{Key: prop, Val: val})
							have[prop] = true
						}
					}
					continue
				case a.Key == "id" && ids[a.Val]:
					a.Val = prefix + a.Val
				default:
					a.Val = urlRefRe.ReplaceAllStringFunc(a.Val, func(m string) string {
						if id := urlRefRe.FindStringSubmatch(m)[1]; ids[id] {
							return "url(#" + prefix + id + ")"
						}
						return m
					})
				}
				if e == root && a.Key == "class" {
					continue // replaced by vp-svg below
				}
				kept = append(kept, a)
			}
			e.Attr = kept
		}
		for c := e.FirstChild; c != nil; c = c.NextSibling {
			fit(c)
		}
	}
	fit(root)
	root.Attr = append(root.Attr, html.Attribute{Key: "class", Val: "vp-svg"})
	if svgPaint.MatchString(background) && !strings.HasPrefix(background, "url(") {
		root.InsertBefore(backing(root, background, radius), root.FirstChild)
	}

	var b strings.Builder
	if err := html.Render(&b, root); err != nil {
		return ""
	}
	return svgPolicy.Sanitize(b.String())
}

// backing is the rectangle a root's background becomes: the viewBox's area,
// or the whole drawing, with the root's corner radius when it gave one.
func backing(root *html.Node, fill, radius string) *html.Node {
	x, y, w, h := "0", "0", "100%", "100%"
	for _, a := range root.Attr {
		if a.Key == "viewBox" {
			if f := strings.Fields(strings.ReplaceAll(a.Val, ",", " ")); len(f) == 4 {
				x, y, w, h = f[0], f[1], f[2], f[3]
			}
		}
	}
	attrs := []html.Attribute{{Key: "x", Val: x}, {Key: "y", Val: y}, {Key: "width", Val: w}, {Key: "height", Val: h}, {Key: "fill", Val: fill}}
	if m := radiusRe.FindStringSubmatch(radius); m != nil {
		attrs = append(attrs, html.Attribute{Key: "rx", Val: m[1]})
	}
	return &html.Node{Type: html.ElementNode, Data: "rect", Namespace: "svg", Attr: attrs}
}
