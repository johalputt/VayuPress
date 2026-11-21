// SPDX-License-Identifier: Apache-2.0

package main

import "github.com/johalputt/vayupress/internal/i18n"

// themeWord is one of the Theme Studio's option words (an option's label, its
// help, a choice's label) in lang. The theme package keeps them in English,
// which its tests and the saved option values are written against; a word is
// translated as it is drawn. Each case hands i18n.T a literal, so the
// catalogue test sees every one, and TestEveryThemeOptionWordIsTranslated
// fails on an option added without its case.
func themeWord(lang, s string) string {
	switch s {
	case "Layout archetype":
		return i18n.T(lang, "Layout archetype")
	case "The overall layout & structure of the blog — switches the whole look.":
		return i18n.T(lang, "The overall layout & structure of the blog — switches the whole look.")
	case "Theme default":
		return i18n.T(lang, "Theme default")
	case "Minimal":
		return i18n.T(lang, "Minimal")
	case "Classic":
		return i18n.T(lang, "Classic")
	case "Magazine":
		return i18n.T(lang, "Magazine")
	case "Editorial":
		return i18n.T(lang, "Editorial")
	case "Bold":
		return i18n.T(lang, "Bold")
	case "Typography":
		return i18n.T(lang, "Typography")
	case "The site's typographic voice — headings and body together. System fonts only, zero external requests.":
		return i18n.T(lang, "The site's typographic voice — headings and body together. System fonts only, zero external requests.")
	case "Elegant — serif headings, sans body":
		return i18n.T(lang, "Elegant — serif headings, sans body")
	case "Literary — book serif throughout":
		return i18n.T(lang, "Literary — book serif throughout")
	case "Modern — crisp geometric sans":
		return i18n.T(lang, "Modern — crisp geometric sans")
	case "Humanist — warm, open sans":
		return i18n.T(lang, "Humanist — warm, open sans")
	case "Grotesk — tight Swiss headlines":
		return i18n.T(lang, "Grotesk — tight Swiss headlines")
	case "Typewriter — mono display":
		return i18n.T(lang, "Typewriter — mono display")
	case "Color scheme":
		return i18n.T(lang, "Color scheme")
	case "Re-tint the accent across the whole site.":
		return i18n.T(lang, "Re-tint the accent across the whole site.")
	case "Indigo":
		return i18n.T(lang, "Indigo")
	case "Violet":
		return i18n.T(lang, "Violet")
	case "Cyan":
		return i18n.T(lang, "Cyan")
	case "Emerald":
		return i18n.T(lang, "Emerald")
	case "Rose":
		return i18n.T(lang, "Rose")
	case "Amber":
		return i18n.T(lang, "Amber")
	case "Crimson":
		return i18n.T(lang, "Crimson")
	case "Teal":
		return i18n.T(lang, "Teal")
	case "Slate":
		return i18n.T(lang, "Slate")
	case "Mono":
		return i18n.T(lang, "Mono")
	case "Reading width":
		return i18n.T(lang, "Reading width")
	case "Width of the content measure.":
		return i18n.T(lang, "Width of the content measure.")
	case "Narrow":
		return i18n.T(lang, "Narrow")
	case "Normal":
		return i18n.T(lang, "Normal")
	case "Wide":
		return i18n.T(lang, "Wide")
	case "Corner style":
		return i18n.T(lang, "Corner style")
	case "Roundness of cards, buttons and inputs.":
		return i18n.T(lang, "Roundness of cards, buttons and inputs.")
	case "Sharp":
		return i18n.T(lang, "Sharp")
	case "Soft":
		return i18n.T(lang, "Soft")
	case "Round":
		return i18n.T(lang, "Round")
	case "Post feed layout":
		return i18n.T(lang, "Post feed layout")
	case "How the home-page post list is arranged.":
		return i18n.T(lang, "How the home-page post list is arranged.")
	case "List":
		return i18n.T(lang, "List")
	case "Grid":
		return i18n.T(lang, "Grid")
	case "Cards":
		return i18n.T(lang, "Cards")
	case "Header alignment":
		return i18n.T(lang, "Header alignment")
	case "Alignment of the hero/title block.":
		return i18n.T(lang, "Alignment of the hero/title block.")
	case "Left":
		return i18n.T(lang, "Left")
	case "Centered":
		return i18n.T(lang, "Centered")
	case "Heading case":
		return i18n.T(lang, "Heading case")
	case "Uppercase":
		return i18n.T(lang, "Uppercase")
	case "Heading accent fill":
		return i18n.T(lang, "Heading accent fill")
	case "Paint large headings with the accent gradient.":
		return i18n.T(lang, "Paint large headings with the accent gradient.")
	case "Solid":
		return i18n.T(lang, "Solid")
	case "Gradient":
		return i18n.T(lang, "Gradient")
	case "Hero style":
		return i18n.T(lang, "Hero style")
	case "Layout of the homepage hero block.":
		return i18n.T(lang, "Layout of the homepage hero block.")
	case "Boxed":
		return i18n.T(lang, "Boxed")
	case "Hero background":
		return i18n.T(lang, "Hero background")
	case "Tint, gradient, or an uploaded image behind the hero.":
		return i18n.T(lang, "Tint, gradient, or an uploaded image behind the hero.")
	case "None":
		return i18n.T(lang, "None")
	case "Accent tint":
		return i18n.T(lang, "Accent tint")
	case "Accent gradient":
		return i18n.T(lang, "Accent gradient")
	case "Uploaded image":
		return i18n.T(lang, "Uploaded image")
	case "Hero height":
		return i18n.T(lang, "Hero height")
	case "Compact":
		return i18n.T(lang, "Compact")
	case "Regular":
		return i18n.T(lang, "Regular")
	case "Tall":
		return i18n.T(lang, "Tall")
	case "Navigation style":
		return i18n.T(lang, "Navigation style")
	case "Alignment of the top navigation bar.":
		return i18n.T(lang, "Alignment of the top navigation bar.")
	case "Brand left, links right":
		return i18n.T(lang, "Brand left, links right")
	case "Stacked":
		return i18n.T(lang, "Stacked")
	case "Post card style":
		return i18n.T(lang, "Post card style")
	case "How post cards look across the blog.":
		return i18n.T(lang, "How post cards look across the blog.")
	case "Flat":
		return i18n.T(lang, "Flat")
	case "Bordered":
		return i18n.T(lang, "Bordered")
	case "Elevated":
		return i18n.T(lang, "Elevated")
	case "Underlined":
		return i18n.T(lang, "Underlined")
	case "Link style":
		return i18n.T(lang, "Link style")
	case "Underlining of links in article content.":
		return i18n.T(lang, "Underlining of links in article content.")
	case "Always underline":
		return i18n.T(lang, "Always underline")
	case "Underline on hover":
		return i18n.T(lang, "Underline on hover")
	case "No underline":
		return i18n.T(lang, "No underline")
	case "Article header":
		return i18n.T(lang, "Article header")
	case "Alignment of the post title and meta on article pages.":
		return i18n.T(lang, "Alignment of the post title and meta on article pages.")
	case "Article meta":
		return i18n.T(lang, "Article meta")
	case "The date / read-time / tags line under a post title.":
		return i18n.T(lang, "The date / read-time / tags line under a post title.")
	case "Show all":
		return i18n.T(lang, "Show all")
	case "Hide tags":
		return i18n.T(lang, "Hide tags")
	case "Hide meta":
		return i18n.T(lang, "Hide meta")
	case "Related posts":
		return i18n.T(lang, "Related posts")
	case "The related-articles list at the end of a post.":
		return i18n.T(lang, "The related-articles list at the end of a post.")
	case "Show":
		return i18n.T(lang, "Show")
	case "Hide":
		return i18n.T(lang, "Hide")
	case "Trending posts":
		return i18n.T(lang, "Trending posts")
	case "The trending & pinned posts strip at the end of a post.":
		return i18n.T(lang, "The trending & pinned posts strip at the end of a post.")
	case "Author box":
		return i18n.T(lang, "Author box")
	case "An author card at the end of each post (uses the site author + bio).":
		return i18n.T(lang, "An author card at the end of each post (uses the site author + bio).")
	case "Featured image style":
		return i18n.T(lang, "Featured image style")
	case "How a post's cover image appears on post cards.":
		return i18n.T(lang, "How a post's cover image appears on post cards.")
	case "On top":
		return i18n.T(lang, "On top")
	case "Beside text":
		return i18n.T(lang, "Beside text")
	case "Hidden":
		return i18n.T(lang, "Hidden")
	case "Density":
		return i18n.T(lang, "Density")
	case "Vertical rhythm and section spacing.":
		return i18n.T(lang, "Vertical rhythm and section spacing.")
	case "Comfortable":
		return i18n.T(lang, "Comfortable")
	case "Spacious":
		return i18n.T(lang, "Spacious")
	case "Heading size":
		return i18n.T(lang, "Heading size")
	case "Scale of display headings.":
		return i18n.T(lang, "Scale of display headings.")
	case "Small":
		return i18n.T(lang, "Small")
	case "Medium":
		return i18n.T(lang, "Medium")
	case "Large":
		return i18n.T(lang, "Large")
	case "Extra large":
		return i18n.T(lang, "Extra large")
	case "Hero mode":
		return i18n.T(lang, "Hero mode")
	case "The rule behind the masthead. Tick rail is the signature; Search puts a full-width search bar in the hero and hides the one in the nav.":
		return i18n.T(lang, "The rule behind the masthead. Tick rail is the signature; Search puts a full-width search bar in the hero and hides the one in the nav.")
	case "Tick rail (signature)":
		return i18n.T(lang, "Tick rail (signature)")
	case "Big search bar":
		return i18n.T(lang, "Big search bar")
	case "Measured grid":
		return i18n.T(lang, "Measured grid")
	case "No ornament":
		return i18n.T(lang, "No ornament")
	case "Paper tone":
		return i18n.T(lang, "Paper tone")
	case "The light-mode paper: bright white, warm ivory, or FT-style salmon.":
		return i18n.T(lang, "The light-mode paper: bright white, warm ivory, or FT-style salmon.")
	case "Bright white":
		return i18n.T(lang, "Bright white")
	case "Warm ivory":
		return i18n.T(lang, "Warm ivory")
	case "Salmon (FT)":
		return i18n.T(lang, "Salmon (FT)")
	case "Drop cap":
		return i18n.T(lang, "Drop cap")
	case "A print-style initial letter on the first paragraph of each post.":
		return i18n.T(lang, "A print-style initial letter on the first paragraph of each post.")
	case "Story grid rules":
		return i18n.T(lang, "Story grid rules")
	case "Hairline rules between front-page stories, broadsheet style.":
		return i18n.T(lang, "Hairline rules between front-page stories, broadsheet style.")
	case "Reading progress":
		return i18n.T(lang, "Reading progress")
	case "A thin scroll-progress bar on posts (pure CSS, motion-respecting).":
		return i18n.T(lang, "A thin scroll-progress bar on posts (pure CSS, motion-respecting).")
	case "Page transitions":
		return i18n.T(lang, "Page transitions")
	case "Soft cross-page fades via CSS view transitions (motion-respecting).":
		return i18n.T(lang, "Soft cross-page fades via CSS view transitions (motion-respecting).")
	case "On":
		return i18n.T(lang, "On")
	case "Off":
		return i18n.T(lang, "Off")
	case "Home":
		return i18n.T(lang, "Home")
	case "The front page leads with the newest post; the river is a calm list with a side column; the index lists by month.":
		return i18n.T(lang, "The front page leads with the newest post; the river is a calm list with a side column; the index lists by month.")
	case "Front page":
		return i18n.T(lang, "Front page")
	case "River":
		return i18n.T(lang, "River")
	case "Index":
		return i18n.T(lang, "Index")
	case "Archive pages and topics":
		return i18n.T(lang, "Archive pages and topics")
	case "How a topic and the later pages of Home are listed.":
		return i18n.T(lang, "How a topic and the later pages of Home are listed.")
	case "Same as Home":
		return i18n.T(lang, "Same as Home")
	case "Articles":
		return i18n.T(lang, "Articles")
	case "Column centres the text with the outline to its right; Margin sets the facts and outline in a left margin.":
		return i18n.T(lang, "Column centres the text with the outline to its right; Margin sets the facts and outline in a left margin.")
	case "Column":
		return i18n.T(lang, "Column")
	case "Margin":
		return i18n.T(lang, "Margin")
	case "Reading face":
		return i18n.T(lang, "Reading face")
	case "The face articles are read in. Readers can switch it for themselves in Aa.":
		return i18n.T(lang, "The face articles are read in. Readers can switch it for themselves in Aa.")
	case "Serif (Newsreader)":
		return i18n.T(lang, "Serif (Newsreader)")
	case "Sans (Inter)":
		return i18n.T(lang, "Sans (Inter)")
	case "Text size":
		return i18n.T(lang, "Text size")
	case "The body size of an article. Readers can change it for themselves in Aa.":
		return i18n.T(lang, "The body size of an article. Readers can change it for themselves in Aa.")
	case "17 px":
		return i18n.T(lang, "17 px")
	case "18 px":
		return i18n.T(lang, "18 px")
	case "19 px":
		return i18n.T(lang, "19 px")
	case "20 px":
		return i18n.T(lang, "20 px")
	case "21 px":
		return i18n.T(lang, "21 px")
	case "22 px":
		return i18n.T(lang, "22 px")
	case "23 px":
		return i18n.T(lang, "23 px")
	case "Running text":
		return i18n.T(lang, "Running text")
	case "Sets runs of capitals (HTML, SQL, NASA) in small caps so they sit quietly in a sentence. The stored post is never changed.":
		return i18n.T(lang, "Sets runs of capitals (HTML, SQL, NASA) in small caps so they sit quietly in a sentence. The stored post is never changed.")
	case "Small caps":
		return i18n.T(lang, "Small caps")
	case "As written":
		return i18n.T(lang, "As written")
	case "Accent":
		return i18n.T(lang, "Accent")
	case "Custom uses the accent colours below; every accent must reach 4.5:1 on both schemes before it can be saved.":
		return i18n.T(lang, "Custom uses the accent colours below; every accent must reach 4.5:1 on both schemes before it can be saved.")
	case "Site colour":
		return i18n.T(lang, "Site colour")
	case "Still Air blue":
		return i18n.T(lang, "Still Air blue")
	case "Custom":
		return i18n.T(lang, "Custom")
	case "Appearance for new readers":
		return i18n.T(lang, "Appearance for new readers")
	case "Auto follows the reader's device. A reader's own choice in Aa always wins.":
		return i18n.T(lang, "Auto follows the reader's device. A reader's own choice in Aa always wins.")
	case "Auto":
		return i18n.T(lang, "Auto")
	case "Light":
		return i18n.T(lang, "Light")
	case "Dark":
		return i18n.T(lang, "Dark")
	case "Most read":
		return i18n.T(lang, "Most read")
	case "The most-read list on Home and at the end of a post.":
		return i18n.T(lang, "The most-read list on Home and at the end of a post.")
	case "Shown":
		return i18n.T(lang, "Shown")
	case "Pinned posts":
		return i18n.T(lang, "Pinned posts")
	case "Featured posts, first in the most-read list.":
		return i18n.T(lang, "Featured posts, first in the most-read list.")
	case "Topics":
		return i18n.T(lang, "Topics")
	case "The numbered topic desks on Home and the topic row on archive pages.":
		return i18n.T(lang, "The numbered topic desks on Home and the topic row on archive pages.")
	case "Subscribe":
		return i18n.T(lang, "Subscribe")
	case "The one-line subscribe on Home, shown while membership is on.":
		return i18n.T(lang, "The one-line subscribe on Home, shown while membership is on.")
	case "Outline":
		return i18n.T(lang, "Outline")
	case "An article's headings beside the text, the current one marked.":
		return i18n.T(lang, "An article's headings beside the text, the current one marked.")
	case "Selection toolbar":
		return i18n.T(lang, "Selection toolbar")
	case "Copy, quote and share a selected passage.":
		return i18n.T(lang, "Copy, quote and share a selected passage.")
	case "Phone reading dock":
		return i18n.T(lang, "Phone reading dock")
	case "Outline, Aa and back to the top, at the foot of a phone screen.":
		return i18n.T(lang, "Outline, Aa and back to the top, at the foot of a phone screen.")
	case "Comments":
		return i18n.T(lang, "Comments")
	case "Shown while comments are switched on for the site.":
		return i18n.T(lang, "Shown while comments are switched on for the site.")
	}
	return s
}
