// SPDX-License-Identifier: Apache-2.0

package main

import "github.com/johalputt/vayupress/internal/i18n"

// themeWord is one of the theme package's words in lang: an option's label,
// its help and its choices' labels, and a store theme's tagline,
// description, category and tags. The theme package keeps them in English,
// which its tests and the saved option values are written against; a word
// is translated as it is drawn. Each case hands i18n.T a literal, so the
// catalogue test sees every one, and TestEveryThemeOptionWordIsTranslated
// fails on an option or a store theme added without its cases. A theme's
// name is not here: it is the theme's own.
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
	case "The VayuPress theme: calm, quiet, for reading.":
		return i18n.T(lang, "The VayuPress theme: calm, quiet, for reading.")
	case "The theme new VayuPress sites start on, and the first to bring its own layout. A reading serif (Newsreader) on warm paper and a dusk that is never black, one quiet accent held to 4.5:1 on every surface, hairline rules instead of boxes, and large italic numerals for the topics on its front page. Home is a front page, a river or an index; articles are a centred column or a margin with the facts; topics are an index by month. Readers choose their own face, size, width and appearance (saved on their device only), follow an outline that marks where they are, and on a phone read with a dock at the foot of the screen. Code wraps and tables become labelled blocks, so nothing scrolls sideways. Comments, most read, pinned and related posts, sign-in, sign-up and the member pages all take the same design. One small script, its own fonts served from this site, no third-party request, light and dark.":
		return i18n.T(lang, "The theme new VayuPress sites start on, and the first to bring its own layout. A reading serif (Newsreader) on warm paper and a dusk that is never black, one quiet accent held to 4.5:1 on every surface, hairline rules instead of boxes, and large italic numerals for the topics on its front page. Home is a front page, a river or an index; articles are a centred column or a margin with the facts; topics are an index by month. Readers choose their own face, size, width and appearance (saved on their device only), follow an outline that marks where they are, and on a phone read with a dock at the foot of the screen. Code wraps and tables become labelled blocks, so nothing scrolls sideways. Comments, most read, pinned and related posts, sign-in, sign-up and the member pages all take the same design. One small script, its own fonts served from this site, no third-party request, light and dark.")
	case "Flagship":
		return i18n.T(lang, "Flagship")
	case "flagship":
		return i18n.T(lang, "flagship")
	case "reading":
		return i18n.T(lang, "reading")
	case "serif":
		return i18n.T(lang, "serif")
	case "editorial":
		return i18n.T(lang, "editorial")
	case "calm":
		return i18n.T(lang, "calm")
	case "layout":
		return i18n.T(lang, "layout")
	case "fast":
		return i18n.T(lang, "fast")
	case "Balanced neutral tones, ready for anything.":
		return i18n.T(lang, "Balanced neutral tones, ready for anything.")
	case "The stock VayuPress look — calm slate-and-teal palette with comfortable typography. A dependable starting point you can deploy as-is or fine-tune in the Studio.":
		return i18n.T(lang, "The stock VayuPress look — calm slate-and-teal palette with comfortable typography. A dependable starting point you can deploy as-is or fine-tune in the Studio.")
	case "neutral":
		return i18n.T(lang, "neutral")
	case "teal":
		return i18n.T(lang, "teal")
	case "balanced":
		return i18n.T(lang, "balanced")
	case "Aurora-inspired purple and teal.":
		return i18n.T(lang, "Aurora-inspired purple and teal.")
	case "Deep indigo canvas lit with violet and mint accents, evoking the northern lights. Generous line-height makes long reads feel airy.":
		return i18n.T(lang, "Deep indigo canvas lit with violet and mint accents, evoking the northern lights. Generous line-height makes long reads feel airy.")
	case "Vibrant":
		return i18n.T(lang, "Vibrant")
	case "purple":
		return i18n.T(lang, "purple")
	case "atmospheric":
		return i18n.T(lang, "atmospheric")
	case "Cool, quiet, and minimal.":
		return i18n.T(lang, "Cool, quiet, and minimal.")
	case "A restrained cool-grey palette with a single sky-blue accent. Tight radii and modest contrast keep the focus squarely on your words.":
		return i18n.T(lang, "A restrained cool-grey palette with a single sky-blue accent. Tight radii and modest contrast keep the focus squarely on your words.")
	case "grey":
		return i18n.T(lang, "grey")
	case "blue":
		return i18n.T(lang, "blue")
	case "clean":
		return i18n.T(lang, "clean")
	case "Green-on-black, pure hacker.":
		return i18n.T(lang, "Green-on-black, pure hacker.")
	case "Phosphor-green text on true black with a monospace stack throughout. Zero border radius and wide measure — a love letter to the CRT.":
		return i18n.T(lang, "Phosphor-green text on true black with a monospace stack throughout. Zero border radius and wide measure — a love letter to the CRT.")
	case "green":
		return i18n.T(lang, "green")
	case "monospace":
		return i18n.T(lang, "monospace")
	case "retro":
		return i18n.T(lang, "retro")
	case "Warm parchment for long reads.":
		return i18n.T(lang, "Warm parchment for long reads.")
	case "Soft sepia tones, a serif body face, and roomy 1.8 line-height tuned for sustained reading comfort, day or night.":
		return i18n.T(lang, "Soft sepia tones, a serif body face, and roomy 1.8 line-height tuned for sustained reading comfort, day or night.")
	case "Reading":
		return i18n.T(lang, "Reading")
	case "warm":
		return i18n.T(lang, "warm")
	case "comfortable":
		return i18n.T(lang, "comfortable")
	case "High-contrast technical dark.":
		return i18n.T(lang, "High-contrast technical dark.")
	case "An IBM-Carbon-flavoured palette: near-black surfaces, crisp blue and pink accents, and sharp corners for a precise, engineered feel.":
		return i18n.T(lang, "An IBM-Carbon-flavoured palette: near-black surfaces, crisp blue and pink accents, and sharp corners for a precise, engineered feel.")
	case "contrast":
		return i18n.T(lang, "contrast")
	case "technical":
		return i18n.T(lang, "technical")
	case "Deep-sea blues and cyans.":
		return i18n.T(lang, "Deep-sea blues and cyans.")
	case "An immersive midnight-ocean background with bright cyan highlights. Rounded corners and easy spacing give it a fluid, modern feel.":
		return i18n.T(lang, "An immersive midnight-ocean background with bright cyan highlights. Rounded corners and easy spacing give it a fluid, modern feel.")
	case "cyan":
		return i18n.T(lang, "cyan")
	case "deep":
		return i18n.T(lang, "deep")
	case "Soft cherry-blossom pinks.":
		return i18n.T(lang, "Soft cherry-blossom pinks.")
	case "Gentle rose and blush tones over a plum-dark base. Large radii and a relaxed measure make it feel light and welcoming.":
		return i18n.T(lang, "Gentle rose and blush tones over a plum-dark base. Large radii and a relaxed measure make it feel light and welcoming.")
	case "pink":
		return i18n.T(lang, "pink")
	case "soft":
		return i18n.T(lang, "soft")
	case "rounded":
		return i18n.T(lang, "rounded")
	case "Deep indigo journal.":
		return i18n.T(lang, "Deep indigo journal.")
	case "Inky indigo surfaces with indigo-violet accents and a narrow journal measure. Inter + IBM Plex Mono for a focused writing mood.":
		return i18n.T(lang, "Inky indigo surfaces with indigo-violet accents and a narrow journal measure. Inter + IBM Plex Mono for a focused writing mood.")
	case "indigo":
		return i18n.T(lang, "indigo")
	case "journal":
		return i18n.T(lang, "journal")
	case "focused":
		return i18n.T(lang, "focused")
	case "Warm rose, soft and welcoming.":
		return i18n.T(lang, "Warm rose, soft and welcoming.")
	case "A friendly rose-and-coral palette over a warm dark base. Soft corners and a cosy measure suit personal blogs and newsletters.":
		return i18n.T(lang, "A friendly rose-and-coral palette over a warm dark base. Soft corners and a cosy measure suit personal blogs and newsletters.")
	case "rose":
		return i18n.T(lang, "rose")
	case "friendly":
		return i18n.T(lang, "friendly")
	case "Fresh green, crisp and modern.":
		return i18n.T(lang, "Fresh green, crisp and modern.")
	case "Cool mint and emerald accents on a deep forest base. Tight radii and a compact measure give a clean, contemporary edge.":
		return i18n.T(lang, "Cool mint and emerald accents on a deep forest base. Tight radii and a compact measure give a clean, contemporary edge.")
	case "mint":
		return i18n.T(lang, "mint")
	case "modern":
		return i18n.T(lang, "modern")
	case "Amber-gold and energetic.":
		return i18n.T(lang, "Amber-gold and energetic.")
	case "Warm amber and gold over a dark espresso base, with a large body size and generous spacing — bold, sunny, and confident.":
		return i18n.T(lang, "Warm amber and gold over a dark espresso base, with a large body size and generous spacing — bold, sunny, and confident.")
	case "amber":
		return i18n.T(lang, "amber")
	case "gold":
		return i18n.T(lang, "gold")
	case "bold":
		return i18n.T(lang, "bold")
	case "Rich purple, sophisticated.":
		return i18n.T(lang, "Rich purple, sophisticated.")
	case "Luxe purple surfaces with orchid accents and a refined measure. A sophisticated dark theme for essays and editorial writing.":
		return i18n.T(lang, "Luxe purple surfaces with orchid accents and a refined measure. A sophisticated dark theme for essays and editorial writing.")
	case "rich":
		return i18n.T(lang, "rich")
	case "elegant":
		return i18n.T(lang, "elegant")
	case "Misty grey, calm and minimal.":
		return i18n.T(lang, "Misty grey, calm and minimal.")
	case "Muted greys with a whisper of blue. The lowest-contrast, quietest theme in the catalogue — ideal when the content is the star.":
		return i18n.T(lang, "Muted greys with a whisper of blue. The lowest-contrast, quietest theme in the catalogue — ideal when the content is the star.")
	case "muted":
		return i18n.T(lang, "muted")
	case "Golden hour, retro film.":
		return i18n.T(lang, "Golden hour, retro film.")
	case "Warm golden tones with a Georgia serif and extra-loose 1.85 line-height. Square corners lend a vintage, printed-page character.":
		return i18n.T(lang, "Warm golden tones with a Georgia serif and extra-loose 1.85 line-height. Square corners lend a vintage, printed-page character.")
	case "Forest green, grounded.":
		return i18n.T(lang, "Forest green, grounded.")
	case "Evergreen surfaces with emerald accents and a touch of gold. Natural, grounded, and easy on the eyes for daily reading.":
		return i18n.T(lang, "Evergreen surfaces with emerald accents and a touch of gold. Natural, grounded, and easy on the eyes for daily reading.")
	case "forest":
		return i18n.T(lang, "forest")
	case "natural":
		return i18n.T(lang, "natural")
	case "Soft purple, gentle and dreamy.":
		return i18n.T(lang, "Soft purple, gentle and dreamy.")
	case "Pale violet accents over a muted purple base, with rounded corners and a relaxed measure — quiet, dreamy, and soft-spoken.":
		return i18n.T(lang, "Pale violet accents over a muted purple base, with rounded corners and a relaxed measure — quiet, dreamy, and soft-spoken.")
	case "dreamy":
		return i18n.T(lang, "dreamy")
	case "True black, high-contrast editorial.":
		return i18n.T(lang, "True black, high-contrast editorial.")
	case "Pure black and white with no chrome at all — zero radius, maximum contrast, large type. Stark, confident, and unmistakably editorial.":
		return i18n.T(lang, "Pure black and white with no chrome at all — zero radius, maximum contrast, large type. Stark, confident, and unmistakably editorial.")
	case "black":
		return i18n.T(lang, "black")
	case "Green-yellow, fresh and organic.":
		return i18n.T(lang, "Green-yellow, fresh and organic.")
	case "Lime and chartreuse accents on a deep green base with a bright yellow highlight. Light, organic, and energetic.":
		return i18n.T(lang, "Lime and chartreuse accents on a deep green base with a bright yellow highlight. Light, organic, and energetic.")
	case "lime":
		return i18n.T(lang, "lime")
	case "fresh":
		return i18n.T(lang, "fresh")
	case "organic":
		return i18n.T(lang, "organic")
	case "Terracotta earth tones.":
		return i18n.T(lang, "Terracotta earth tones.")
	case "Warm terracotta and clay over a dark earthen base, paired with a Georgia serif. Grounded warmth for storytelling.":
		return i18n.T(lang, "Warm terracotta and clay over a dark earthen base, paired with a Georgia serif. Grounded warmth for storytelling.")
	case "orange":
		return i18n.T(lang, "orange")
	case "earth":
		return i18n.T(lang, "earth")
	case "Icy blue, clean and crisp.":
		return i18n.T(lang, "Icy blue, clean and crisp.")
	case "Cool glacier blues and cyans with soft corners. Crisp and refreshing, with a comfortable measure for technical writing.":
		return i18n.T(lang, "Cool glacier blues and cyans with soft corners. Crisp and refreshing, with a comfortable measure for technical writing.")
	case "icy":
		return i18n.T(lang, "icy")
	case "crisp":
		return i18n.T(lang, "crisp")
	case "Orange-pink tropical energy.":
		return i18n.T(lang, "Orange-pink tropical energy.")
	case "Vibrant coral and rose accents over a warm dark base. Lively and inviting, with rounded corners and a relaxed measure.":
		return i18n.T(lang, "Vibrant coral and rose accents over a warm dark base. Lively and inviting, with rounded corners and a relaxed measure.")
	case "coral":
		return i18n.T(lang, "coral")
	case "vibrant":
		return i18n.T(lang, "vibrant")
	case "Editorial magazine, bold typography.":
		return i18n.T(lang, "Editorial magazine, bold typography.")
	case "A full editorial layout: large hero sections, card-based grids, and bold amber accents over sophisticated dark surfaces. Ships its own layout CSS for a true magazine feel.":
		return i18n.T(lang, "A full editorial layout: large hero sections, card-based grids, and bold amber accents over sophisticated dark surfaces. Ships its own layout CSS for a true magazine feel.")
	case "magazine":
		return i18n.T(lang, "magazine")
	case "Bright, creative, open layout.":
		return i18n.T(lang, "Bright, creative, open layout.")
	case "An airy, creative theme with coral-rose accents, prominent calls-to-action, and a multi-column footer. Ships its own layout CSS for a modern marketing-site feel.":
		return i18n.T(lang, "An airy, creative theme with coral-rose accents, prominent calls-to-action, and a multi-column footer. Ships its own layout CSS for a modern marketing-site feel.")
	case "creative":
		return i18n.T(lang, "creative")
	case "Modern newsletter & email publication.":
		return i18n.T(lang, "Modern newsletter & email publication.")
	case "A modern newsletter and email-publication theme. A clean neutral canvas with a confident emerald accent and an email-width measure, shipping a complete component kit: a customizable hero with inline/section subscribe, a 'Featured in' logos strip, a story section, inbox-style featured issues, a reviews slider, a latest-issues feed with a sticky subscribe sidebar, a topic list, newsletter cards, membership tiers, archive, and web/email post formats — light & dark.":
		return i18n.T(lang, "A modern newsletter and email-publication theme. A clean neutral canvas with a confident emerald accent and an email-width measure, shipping a complete component kit: a customizable hero with inline/section subscribe, a 'Featured in' logos strip, a story section, inbox-style featured issues, a reviews slider, a latest-issues feed with a sticky subscribe sidebar, a topic list, newsletter cards, membership tiers, archive, and web/email post formats — light & dark.")
	case "Newsletter":
		return i18n.T(lang, "Newsletter")
	case "newsletter":
		return i18n.T(lang, "newsletter")
	case "email":
		return i18n.T(lang, "email")
	case "subscribe":
		return i18n.T(lang, "subscribe")
	case "emerald":
		return i18n.T(lang, "emerald")
	case "Bold, expressive magazine with color schemes.":
		return i18n.T(lang, "Bold, expressive magazine with color schemes.")
	case "A bold, expressive magazine theme with a clean grid, playful rounded shapes, an accent-colored header band, and expressive serif display headings over a modern sans body. Ships a complete component kit: a hero with five intro modes (signup form/button, search, dual CTA, or none) plus optional cover, a featured-posts slider, a popular-tags carousel, a quote widget, five featured-image aspect ratios, author/tags/contact templates, membership tiers, a multi-column footer, an announcement bar, and six swappable color schemes — light & dark.":
		return i18n.T(lang, "A bold, expressive magazine theme with a clean grid, playful rounded shapes, an accent-colored header band, and expressive serif display headings over a modern sans body. Ships a complete component kit: a hero with five intro modes (signup form/button, search, dual CTA, or none) plus optional cover, a featured-posts slider, a popular-tags carousel, a quote widget, five featured-image aspect ratios, author/tags/contact templates, membership tiers, a multi-column footer, an announcement bar, and six swappable color schemes — light & dark.")
	case "expressive":
		return i18n.T(lang, "expressive")
	case "color schemes":
		return i18n.T(lang, "color schemes")
	case "Sleek startup & company blog.":
		return i18n.T(lang, "Sleek startup & company blog.")
	case "A modern, sleek startup and company-blog theme with a clean light-first canvas, a confident royal-blue accent, and a sticky glassy header. Ships a complete component kit: a brand-accent hero with four background styles and three action styles, adaptive featured posts (1–5), pinned/sponsored pages, a featured-tags topic list in three layouts (list/section/cards), list & grid post feeds with three card styles (minimal/bordered/shadowed), a CTA section, narrow/wide post templates with a table of contents, membership tiers, archive and tags index, and a multi-column footer with a light/dark toggle.":
		return i18n.T(lang, "A modern, sleek startup and company-blog theme with a clean light-first canvas, a confident royal-blue accent, and a sticky glassy header. Ships a complete component kit: a brand-accent hero with four background styles and three action styles, adaptive featured posts (1–5), pinned/sponsored pages, a featured-tags topic list in three layouts (list/section/cards), list & grid post feeds with three card styles (minimal/bordered/shadowed), a CTA section, narrow/wide post templates with a table of contents, membership tiers, archive and tags index, and a multi-column footer with a light/dark toggle.")
	case "Business":
		return i18n.T(lang, "Business")
	case "startup":
		return i18n.T(lang, "startup")
	case "company":
		return i18n.T(lang, "company")
	case "saas":
		return i18n.T(lang, "saas")
	case "professional":
		return i18n.T(lang, "professional")
	case "Fluid, wave-styled sidebar blog.":
		return i18n.T(lang, "Fluid, wave-styled sidebar blog.")
	case "A friendly, fluid blog and magazine theme on a cool turquoise palette, whose signature is decorative wave-shaped section dividers. Ships a complete component kit: a wave-edged hero, a content + sidebar layout with a full set of widgets (about me, recent/related articles, social profiles, tag cloud, advertising), four single-post dispositions (classic, vertical, fullcover, no-sidebar), membership pricing tables with a monthly/yearly toggle, post cards with per-post accents, custom tag/author/popular-authors/404 pages, a gallery grid, and a footer with quick links — light & dark.":
		return i18n.T(lang, "A friendly, fluid blog and magazine theme on a cool turquoise palette, whose signature is decorative wave-shaped section dividers. Ships a complete component kit: a wave-edged hero, a content + sidebar layout with a full set of widgets (about me, recent/related articles, social profiles, tag cloud, advertising), four single-post dispositions (classic, vertical, fullcover, no-sidebar), membership pricing tables with a monthly/yearly toggle, post cards with per-post accents, custom tag/author/popular-authors/404 pages, a gallery grid, and a footer with quick links — light & dark.")
	case "blog":
		return i18n.T(lang, "blog")
	case "sidebar":
		return i18n.T(lang, "sidebar")
	case "waves":
		return i18n.T(lang, "waves")
	case "widgets":
		return i18n.T(lang, "widgets")
	case "Bold, creative statement theme.":
		return i18n.T(lang, "Bold, creative statement theme.")
	case "A bold, creative statement theme for those who dare to stand out — a high-contrast monochrome canvas with a punchy magenta accent, big display typography, and sharp corners. Ships a complete component kit: full-bleed background-media heroes with overlaid text (configurable button style, content position and length), an 'our selection' slider, animated floating circles for testimonials or tags, creative/default post cards with eight image aspect ratios, four post templates (default and right-aligned, each with optional featured video), text-style and heading-scale controls, page-load animations, custom account pages with background video, and authors/FAQ/archive/all-posts pages — light & dark.":
		return i18n.T(lang, "A bold, creative statement theme for those who dare to stand out — a high-contrast monochrome canvas with a punchy magenta accent, big display typography, and sharp corners. Ships a complete component kit: full-bleed background-media heroes with overlaid text (configurable button style, content position and length), an 'our selection' slider, animated floating circles for testimonials or tags, creative/default post cards with eight image aspect ratios, four post templates (default and right-aligned, each with optional featured video), text-style and heading-scale controls, page-load animations, custom account pages with background video, and authors/FAQ/archive/all-posts pages — light & dark.")
	case "Creative":
		return i18n.T(lang, "Creative")
	case "portfolio":
		return i18n.T(lang, "portfolio")
	case "animated":
		return i18n.T(lang, "animated")
	case "Community & discussion, dual sidebars.":
		return i18n.T(lang, "Community & discussion, dual sidebars.")
	case "A clean, forum-inspired community and discussion theme on a neutral canvas with a friendly indigo-violet accent. Ships a complete component kit: a three-column layout with dual toggleable sidebars, a customizable hero (six image positions and three actions), homepage feed tabs (latest/featured/updated/categories) with four switchable view styles (list/compact/cards/articles), post cards with a comment-count badge and an inline comments drawer, topic/category sections and a tag-cards template, default and full-width post templates, membership pages (sign in/up, account, tiers), an authors page, a blank landing template, and a footer with a light/dark toggle.":
		return i18n.T(lang, "A clean, forum-inspired community and discussion theme on a neutral canvas with a friendly indigo-violet accent. Ships a complete component kit: a three-column layout with dual toggleable sidebars, a customizable hero (six image positions and three actions), homepage feed tabs (latest/featured/updated/categories) with four switchable view styles (list/compact/cards/articles), post cards with a comment-count badge and an inline comments drawer, topic/category sections and a tag-cards template, default and full-width post templates, membership pages (sign in/up, account, tiers), an authors page, a blank landing template, and a footer with a light/dark toggle.")
	case "Community":
		return i18n.T(lang, "Community")
	case "community":
		return i18n.T(lang, "community")
	case "discussion":
		return i18n.T(lang, "discussion")
	case "forum":
		return i18n.T(lang, "forum")
	case "sidebars":
		return i18n.T(lang, "sidebars")
	case "The flagship do-everything theme.":
		return i18n.T(lang, "The flagship do-everything theme.")
	case "The flagship, do-everything theme — a premium, infinitely-configurable publication system on a gradient-driven indigo-violet and cyan palette. Ships the most complete component kit in the catalogue: a glass sticky header with mega-menu, four hero styles (split, centered, full-bleed media, pure-CSS gradient mesh), a signature bento grid, four feed layouts (grid/list/magazine/masonry) and several card styles, plus features, stats, logos, testimonials, pricing tiers, FAQ accordion, gallery, timeline, newsletter and CTA sections, a full reading experience (reading-progress bar, sticky table of contents, share, author box, related posts), a mega footer, and ten swappable color schemes with density (compact/comfortable/spacious) and shape (sharp/rounded/pill) modes plus an optional gradient accent — CSP-safe, WCAG-AA, light & dark.":
		return i18n.T(lang, "The flagship, do-everything theme — a premium, infinitely-configurable publication system on a gradient-driven indigo-violet and cyan palette. Ships the most complete component kit in the catalogue: a glass sticky header with mega-menu, four hero styles (split, centered, full-bleed media, pure-CSS gradient mesh), a signature bento grid, four feed layouts (grid/list/magazine/masonry) and several card styles, plus features, stats, logos, testimonials, pricing tiers, FAQ accordion, gallery, timeline, newsletter and CTA sections, a full reading experience (reading-progress bar, sticky table of contents, share, author box, related posts), a mega footer, and ten swappable color schemes with density (compact/comfortable/spacious) and shape (sharp/rounded/pill) modes plus an optional gradient accent — CSP-safe, WCAG-AA, light & dark.")
	case "premium":
		return i18n.T(lang, "premium")
	case "multipurpose":
		return i18n.T(lang, "multipurpose")
	case "bento":
		return i18n.T(lang, "bento")
	case "gradient":
		return i18n.T(lang, "gradient")
	case "configurable":
		return i18n.T(lang, "configurable")
	case "The world-newsroom flagship — a broadsheet front page.":
		return i18n.T(lang, "The world-newsroom flagship — a broadsheet front page.")
	case "A broadsheet-grade newspaper theme for serious, multinational publications. Warm paper and true-ink dark editions, serif display type over quiet sans chrome, and one rationed editorial red used only for labels and hairline rules. Ships a complete newsroom kit: a centred masthead with an uppercase section bar, a hairline story lattice with a lead-story package, a numbered most-read treatment for trending and pinned posts, breaking & live kickers, drop caps, pull quotes, standfirsts, a pure-CSS reading-progress bar and soft page transitions — plus paper-tone (ivory/bright/FT-salmon), drop-cap, column-rule, reading-progress and page-fade controls in the Theme Studio. Zero JavaScript, zero external requests, zero layout shift — 100/100 fast, light & dark.":
		return i18n.T(lang, "A broadsheet-grade newspaper theme for serious, multinational publications. Warm paper and true-ink dark editions, serif display type over quiet sans chrome, and one rationed editorial red used only for labels and hairline rules. Ships a complete newsroom kit: a centred masthead with an uppercase section bar, a hairline story lattice with a lead-story package, a numbered most-read treatment for trending and pinned posts, breaking & live kickers, drop caps, pull quotes, standfirsts, a pure-CSS reading-progress bar and soft page transitions — plus paper-tone (ivory/bright/FT-salmon), drop-cap, column-rule, reading-progress and page-fade controls in the Theme Studio. Zero JavaScript, zero external requests, zero layout shift — 100/100 fast, light & dark.")
	case "newspaper":
		return i18n.T(lang, "newspaper")
	case "news":
		return i18n.T(lang, "news")
	case "broadsheet":
		return i18n.T(lang, "broadsheet")
	case "Match your site — the vayupress.com look for your blog.":
		return i18n.T(lang, "Match your site — the vayupress.com look for your blog.")
	case "The flagship theme that mirrors vayupress.com: a deep cosmic “ink” canvas lit with bright teal and saffron, gradient display headings, glassy post cards with a teal glow-lift, an underlined nav, gradient-bordered code blocks and a refined reading experience. Deploy it in one click so your blog and website share one identity. Pure CSS with a system-font stack — no web fonts, no JavaScript, no external requests — so it stays blazing fast (100/100 PageSpeed) and fully sovereign, in light and dark.":
		return i18n.T(lang, "The flagship theme that mirrors vayupress.com: a deep cosmic “ink” canvas lit with bright teal and saffron, gradient display headings, glassy post cards with a teal glow-lift, an underlined nav, gradient-bordered code blocks and a refined reading experience. Deploy it in one click so your blog and website share one identity. Pure CSS with a system-font stack — no web fonts, no JavaScript, no external requests — so it stays blazing fast (100/100 PageSpeed) and fully sovereign, in light and dark.")
	case "saffron":
		return i18n.T(lang, "saffron")
	case "cosmic":
		return i18n.T(lang, "cosmic")
	case "brand":
		return i18n.T(lang, "brand")
	case "An observatory log, not a magazine.":
		return i18n.T(lang, "An observatory log, not a magazine.")
	case "The archive as an instrument log. Where every other dark theme reaches for a rounded, softly-lit card grid, Orbit inverts each of those decisions: one column of numbered entries, sharp corners, hairline rules instead of filled surfaces, and a mono-led chrome tracked wide over a neutral near-black. Each post's date sits in a rail of its own beside the title, the entry number is a CSS counter, and the thumbnail is a small desaturated square at the end of the row rather than a banner above it. Entries arrive on the scroll position itself — a real scroll-driven animation, guarded so a browser that lacks it shows them plainly rather than not at all — and hovering draws a rule under the row instead of lifting a card. Article headings carry a numbered § rail, tags are bracketed mono tokens, and even the 404 reads as a status line. Ships four hero modes — the signature tick rail, a measured grid field, a full-width search bar as the primary call to action, or no ornament — each emitted on its own so a reader downloads only the mode in use. No web fonts, no JavaScript and no external request of any kind; every animation is transform/opacity only and every one stops under prefers-reduced-motion, so nothing on the page can move once it has painted. Light & dark, WCAG-AA, CSP-safe.":
		return i18n.T(lang, "The archive as an instrument log. Where every other dark theme reaches for a rounded, softly-lit card grid, Orbit inverts each of those decisions: one column of numbered entries, sharp corners, hairline rules instead of filled surfaces, and a mono-led chrome tracked wide over a neutral near-black. Each post's date sits in a rail of its own beside the title, the entry number is a CSS counter, and the thumbnail is a small desaturated square at the end of the row rather than a banner above it. Entries arrive on the scroll position itself — a real scroll-driven animation, guarded so a browser that lacks it shows them plainly rather than not at all — and hovering draws a rule under the row instead of lifting a card. Article headings carry a numbered § rail, tags are bracketed mono tokens, and even the 404 reads as a status line. Ships four hero modes — the signature tick rail, a measured grid field, a full-width search bar as the primary call to action, or no ornament — each emitted on its own so a reader downloads only the mode in use. No web fonts, no JavaScript and no external request of any kind; every animation is transform/opacity only and every one stops under prefers-reduced-motion, so nothing on the page can move once it has painted. Light & dark, WCAG-AA, CSP-safe.")
	case "dark":
		return i18n.T(lang, "dark")
	case "minimal":
		return i18n.T(lang, "minimal")
	case "mono":
		return i18n.T(lang, "mono")
	case "search":
		return i18n.T(lang, "search")
	}
	return s
}
