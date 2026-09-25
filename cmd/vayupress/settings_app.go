// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// The Settings app (plan §4, render 06): categories in the app sidebar, rows
// with the control on the right, a mark on every changed row, and one bar that
// saves or discards them together.
//
// A category is declared as data rather than as markup, because three things
// read the same declaration: the page, the search index in the sidebar, and
// the tests that hold every row to a key the settings store accepts. A row
// that saves nothing cannot be declared here without failing that test; three
// such rows (members.enabled, members.magic_link, smtp.from) had shipped and
// reported "Settings saved" while the store dropped them.

// settingField is one row: what it is, what it does, and its control.
type settingField struct {
	ID, Key, Label, Hint string
	// Kind picks the control: text, email, textarea, toggle, color, timezone,
	// scheme (light / dark / match system).
	Kind, Placeholder, Default string
}

// settingsGroup is one band of rows under a heading. Custom draws a band whose
// control is more than a row (the menu and footer editors, the site mark);
// Terms are what the search finds it by.
type settingsGroup struct {
	Title, Hint string
	Fields      []settingField
	Custom      func(ctx context.Context, a *App) ui.HTML
	Terms       string
}

type settingsCategory struct {
	Slug, Label, Icon, Sub string
	Group                  string // a sidebar heading that starts before this category
	Groups                 []settingsGroup
}

var settingsCategories = []settingsCategory{
	{Slug: "general", Label: "Site identity", Icon: "globe",
		Sub: "What your site is called, what it is about, and whose it is.",
		Groups: []settingsGroup{
			{Title: "Identity", Fields: []settingField{
				{ID: "s-name", Key: settings.KeySiteName, Label: "Site name", Hint: "Shown in the header, the tab and every email.", Kind: "text", Placeholder: "My Publication"},
				{ID: "s-tagline", Key: settings.KeySiteTagline, Label: "Tagline", Hint: "One line under the name.", Kind: "text", Placeholder: "A short description"},
				{ID: "s-desc", Key: settings.KeySiteDescription, Label: "Description", Hint: "Used in feeds, the sitemap and search results.", Kind: "textarea"},
				{ID: "s-author", Key: settings.KeySiteAuthor, Label: "Author", Hint: "The byline when a post names nobody else.", Kind: "text", Placeholder: "Your name"},
			}},
			{Title: "Dates and times", Fields: []settingField{
				{ID: "s-timezone", Key: settings.KeySiteTimezone, Label: "Time zone", Hint: "Everything is stored in UTC; this changes only how times read.", Kind: "timezone"},
			}},
		}},
	{Slug: "appearance", Label: "Appearance", Icon: "sun",
		Sub: "How the console looks to you. Your site’s own design lives in Site › Theme.",
		Groups: []settingsGroup{
			{Title: "Console", Fields: []settingField{
				{ID: "s-scheme", Key: "admin.theme", Label: "Colour scheme", Hint: "Match system follows your device.", Kind: "scheme", Default: "auto"},
			}},
		}},
	{Slug: "navigation", Label: "Navigation", Icon: "list",
		Sub: "The links across the top of every public page.",
		Groups: []settingsGroup{
			{Title: "Public menu", Hint: "Empty restores Home, Feed and Console", Custom: settingsNavEditor, Terms: "menu links header nav"},
		}},
	{Slug: "footer", Label: "Footer", Icon: "columns",
		Sub: "What sits at the bottom of every public page.",
		Groups: []settingsGroup{
			{Title: "Footer", Custom: settingsFooterEditor, Terms: "footer tagline copyright columns social legal privacy terms"},
		}},
	{Slug: "design", Label: "Branding", Icon: "palette",
		Sub: "Your site’s mark and colours. Layout, type and the rest are edited live in Site › Theme.",
		Groups: []settingsGroup{
			{Title: "Mark", Custom: settingsSiteMark, Terms: "logo favicon icon mark upload"},
			{Title: "Colours", Fields: []settingField{
				{ID: "s-primary-light", Key: settings.KeyThemePrimaryLight, Label: "Primary colour, light", Hint: "Links and buttons on the public site in light mode.", Kind: "color", Default: "#0f766e"},
				{ID: "s-primary-dark", Key: settings.KeyThemePrimaryDark, Label: "Primary colour, dark", Hint: "The same, in dark mode.", Kind: "color", Default: "#2dd4bf"},
			}},
			{Title: "Custom CSS", Fields: []settingField{
				{ID: "s-custom-css", Key: settings.KeyThemeCustomCSS, Label: "Stylesheet", Hint: "Added to every public page. Never loaded in the console.", Kind: "code"},
			}},
		}},
	{Slug: "members", Label: "Members", Icon: "audience",
		Sub: "How readers join. Plans and payments are in Audience › Monetization.",
		Groups: []settingsGroup{
			{Title: "Sign-up", Fields: []settingField{
				{ID: "s-member-buttons", Key: settings.KeyMembershipButtons, Label: "Sign in and Sign up buttons", Hint: "Shown in the public site’s menu.", Kind: "toggle"},
			}},
		}},
	{Slug: "email", Label: "Email delivery", Icon: "mail",
		Sub: "The server your site’s own email goes out through: sign-in links, receipts, newsletters.",
		Groups: []settingsGroup{
			{Title: "Outgoing mail", Hint: "Set in the environment", Custom: settingsEmailFacts, Terms: "smtp email server from address host port tls"},
		}},
	{Slug: "advanced", Label: "Advanced", Icon: "wrench", Group: "Install",
		Sub: "Where this install keeps its working files.",
		Groups: []settingsGroup{
			{Title: "Files", Custom: settingsAdvancedFacts, Terms: "cache directory export restore backup storage"},
		}},
}

// savedSetting reads a saved value, or "" where there is no store (a test
// rendering the pages, a boot that failed to open the database).
func (a *App) savedSetting(ctx context.Context, key string) string {
	if a == nil || a.siteSettings == nil {
		return ""
	}
	return a.siteSettings.Get(ctx, settings.ForPrimary(), key)
}

// settingsHref is where a category lives. Site identity is the Settings app's
// front page, so the app's own link lands on it.
func settingsHref(slug string) string {
	if slug == "general" {
		return "/os/settings"
	}
	return "/os/settings/" + slug
}

// settingsSections are the categories as sidebar sections of the Settings app.
func settingsSections() []saSection {
	out := make([]saSection, 0, len(settingsCategories))
	for _, c := range settingsCategories {
		out = append(out, saSection{Label: c.Label, Href: settingsHref(c.Slug), Icon: c.Icon, Group: c.Group})
	}
	return out
}

func settingsCategoryFor(slug string) (settingsCategory, bool) {
	for _, c := range settingsCategories {
		if c.Slug == slug {
			return c, true
		}
	}
	return settingsCategory{}, false
}

// settingControl draws a field's control, holding the saved value.
func settingControl(f settingField, value string) ui.HTML {
	if value == "" {
		value = f.Default
	}
	id, key, v := html.EscapeString(f.ID), html.EscapeString(f.Key), html.EscapeString(value)
	attrs := `id="` + id + `" data-setting-key="` + key + `"`
	switch f.Kind {
	case "textarea":
		return ui.HTML(`<textarea class="textarea" rows="3" ` + attrs + `>` + v + `</textarea>`)
	case "code":
		return ui.HTML(`<textarea class="textarea font-mono" rows="6" spellcheck="false" placeholder="/* Your CSS */" ` + attrs + `>` + v + `</textarea>`)
	case "toggle":
		on := ""
		if value == "true" {
			on = " checked"
		}
		return ui.HTML(`<input type="checkbox" class="toggle" ` + attrs + on + `>`)
	case "color":
		return ui.HTML(`<input type="color" class="settings-color" ` + attrs + ` value="` + v + `">`)
	case "timezone":
		return ui.HTML(`<select class="input" ` + attrs + `>` + timezoneOptionsHTML(value) + `</select>`)
	case "scheme":
		// A segmented control writing one hidden value, so it saves and
		// discards like every other row. Not data-sa-theme: that is the
		// account menu's control, which applies at once.
		var b strings.Builder
		b.WriteString(`<div class="sa-seg" role="radiogroup" aria-label="` + html.EscapeString(f.Label) + `" data-seg-for="` + id + `">`)
		for _, o := range [][2]string{{"light", "Light"}, {"dark", "Dark"}, {"auto", "Match system"}} {
			on := "false"
			if o[0] == value {
				on = "true"
			}
			b.WriteString(`<button type="button" class="sa-seg__opt" role="radio" aria-checked="` + on + `" data-seg-value="` + o[0] + `">` + o[1] + `</button>`)
		}
		b.WriteString(`</div><input type="hidden" ` + attrs + ` value="` + v + `">`)
		return ui.HTML(b.String())
	}
	t := "text"
	if f.Kind == "email" {
		t = "email"
	}
	ph := ""
	if f.Placeholder != "" {
		ph = ` placeholder="` + html.EscapeString(f.Placeholder) + `"`
	}
	return ui.HTML(`<input class="input" type="` + t + `" ` + attrs + ` value="` + v + `"` + ph + `>`)
}

// settingsPageBody renders one category.
func settingsPageBody(ctx context.Context, a *App, c settingsCategory) ui.HTML {
	parts := []ui.HTML{`<div class="settings-page">`, ui.Page(c.Label, c.Sub, "")}
	for _, g := range c.Groups {
		var body ui.HTML
		if g.Custom != nil {
			body = g.Custom(ctx, a)
		} else {
			rows := make([]ui.Row, 0, len(g.Fields))
			for _, f := range g.Fields {
				value := a.savedSetting(ctx, f.Key)
				hint := f.Hint
				if f.Kind == "timezone" {
					hint += " Now: " + currentSiteTimeLine() + "."
				}
				rows = append(rows, ui.Row{Label: f.Label, Hint: hint, Control: settingControl(f, value), ID: f.ID})
			}
			body = ui.Rows(rows...)
		}
		parts = append(parts, ui.Section(g.Title, g.Hint, body))
	}
	// The one bar that saves or discards every changed row. It rises when the
	// first row changes; aria-live says how many are waiting.
	parts = append(parts, `<div class="settings-bar" data-settings-bar hidden><span class="settings-bar__count" data-settings-count aria-live="polite"></span>`+
		`<button type="button" class="btn btn--ghost btn--sm" data-settings-discard>Discard</button>`+
		`<button type="button" class="btn btn--primary btn--sm" data-settings-save>Save changes</button></div></div>`)
	return ui.Join(parts...)
}

// settingsSearchIndex is what "Search settings" looks through: every row and
// band of every category, and the Settings app's other pages by name. It is a
// list of links, so it reads without script too.
func settingsSearchIndex(app *saApp) string {
	var b strings.Builder
	item := func(href, label, where, terms string) {
		b.WriteString(`<li><a class="sa-find__item" href="` + html.EscapeString(href) + `" data-terms="` +
			html.EscapeString(strings.ToLower(label+" "+where+" "+terms)) + `"><span class="sa-find__label">` + html.EscapeString(label) +
			`</span><span class="sa-find__where">` + html.EscapeString(where) + `</span></a></li>`)
	}
	for _, c := range settingsCategories {
		for _, g := range c.Groups {
			if g.Custom != nil {
				where := c.Label
				if g.Title == c.Label {
					where = "Settings" // "Footer, in Footer" says nothing
				}
				item(settingsHref(c.Slug), g.Title, where, g.Hint+" "+g.Terms)
			}
			for _, f := range g.Fields {
				item(settingsHref(c.Slug)+"#"+f.ID, f.Label, c.Label, f.Hint)
			}
		}
	}
	for _, s := range app.Sections {
		if !strings.HasPrefix(s.Href, "/os/settings") {
			item(s.Href, s.Label, "Settings", s.Group)
		}
	}
	return `<div class="sa-find">` + saIcon("search") + `<input type="search" class="input sa-find__input" placeholder="Search settings" aria-label="Search settings" data-settings-search>` +
		`<ul class="sa-find__results" data-settings-index hidden>` + b.String() + `</ul>` +
		`<p class="sa-find__none" data-settings-none hidden>No setting matches that.</p></div>`
}

func settingsNavEditor(ctx context.Context, a *App) ui.HTML {
	navJSON := a.savedSetting(ctx, settings.KeyNavItems)
	if strings.TrimSpace(navJSON) == "" {
		// The editor starts from the menu readers see now, not a blank list.
		navJSON = `[{"label":"Home","href":"/"},{"label":"Feed","href":"/feed.xml"},{"label":"Console","href":"/admin"}]`
	}
	return ui.HTML(`<div class="settings-panel"><p class="settings-panel__lead">Point a link at a page of yours (<code>/about</code>), a feed, or another site.</p>
  <div id="nav-editor" data-nav-json="` + html.EscapeString(navJSON) + `"></div>
  <button type="button" class="btn btn--sm" id="nav-add-btn">Add link</button>
  <input type="hidden" id="nav-json-input" data-setting-key="` + settings.KeyNavItems + `" value="` + html.EscapeString(navJSON) + `"></div>`)
}

// defaultFooterSeed starts an unconfigured footer from a complete layout (a
// link column, Privacy and Terms, a copyright line) rather than a blank one.
const defaultFooterSeed = `{"tagline":"","copyright":"© {year} {site}. All rights reserved.","columns":[{"title":"Explore","links":[{"label":"Home","href":"/"},{"label":"Feed","href":"/feed.xml"}]}],"social":[],"legal":[{"label":"Privacy","href":"/privacy"},{"label":"Terms","href":"/terms"}]}`

func settingsFooterEditor(ctx context.Context, a *App) ui.HTML {
	footerJSON := a.savedSetting(ctx, settings.KeyFooterConfig)
	if strings.TrimSpace(footerJSON) == "" {
		footerJSON = defaultFooterSeed
	}
	esc := html.EscapeString(footerJSON)
	return ui.Join(
		ui.Rows(
			ui.Row{Label: "Tagline", Hint: "A line under your brand.", ID: "footer-tagline",
				Control: `<input id="footer-tagline" class="input" type="text" placeholder="A short line">`},
			ui.Row{Label: "Copyright", Hint: "{year} is this year, {site} your site’s name.", ID: "footer-copyright",
				Control: `<input id="footer-copyright" class="input" type="text" placeholder="© {year} {site}. All rights reserved.">`},
		),
		ui.HTML(`<div class="settings-panel">
  <h3 class="settings-panel__title">Link columns</h3><div id="footer-cols"></div>
  <button type="button" class="btn btn--sm" id="footer-add-col">Add column</button>
  <h3 class="settings-panel__title">Social links</h3><div id="footer-social"></div>
  <button type="button" class="btn btn--sm" id="footer-add-social">Add social link</button>
  <h3 class="settings-panel__title">Legal links</h3><p class="settings-panel__lead">Beside the copyright: Privacy, Terms, Cookies.</p><div id="footer-legal"></div>
  <button type="button" class="btn btn--sm" id="footer-add-legal">Add legal link</button>
  <input type="hidden" id="footer-json-input" data-setting-key="`+settings.KeyFooterConfig+`" data-footer-seed="`+esc+`" value="`+esc+`"></div>`),
	)
}

func settingsSiteMark(ctx context.Context, a *App) ui.HTML {
	state := "Using the default mark."
	if a.savedSetting(ctx, settings.KeyBrandFavicon) != "" {
		state = "Your own mark, stored in the database."
	}
	return ui.Rows(ui.Row{Label: "Site mark", Hint: "The browser tab icon and the logo in the public menu. PNG or ICO, square, up to 256 KB. Applies at once.",
		Control: ui.HTML(`<div class="settings-mark"><img id="brand-favicon-img" class="settings-mark__img" src="/favicon.ico?t=` + strconv.FormatInt(time.Now().Unix(), 10) + `" alt="Current site mark" width="40" height="40">` +
			`<span class="settings-mark__state" id="brand-favicon-state">` + html.EscapeString(state) + `</span>` +
			`<input type="file" id="brand-favicon-file" class="settings-mark__file" accept="image/png,image/x-icon,.png,.ico" aria-label="Choose a mark">` +
			`<button type="button" class="btn btn--sm" id="brand-favicon-upload">Upload</button>` +
			`<button type="button" class="btn btn--ghost btn--sm" id="brand-favicon-remove">Use the default</button>` +
			`<span id="brand-favicon-status" class="settings-mark__status" role="status" aria-live="polite"></span></div>`)})
}

// settingsEmailFacts says how this install's own mail actually leaves it:
// through SMTP_HOST when one is set, otherwise through the built-in VayuMail
// engine (main.go wires it as the mailer's fallback), otherwise not at all.
func settingsEmailFacts(_ context.Context, a *App) ui.HTML {
	mono := func(v string) ui.HTML { return `<span class="mono">` + ui.Text(v) + `</span>` }
	var facts []ui.Fact
	switch {
	case config.Cfg.SMTPHost != "":
		security := config.Cfg.SMTPTLS
		if security == "" {
			security = "starttls"
		}
		signin := ui.Text("Nobody: the server takes mail without a sign-in")
		if config.Cfg.SMTPUsername != "" {
			signin = mono(config.Cfg.SMTPUsername)
		}
		facts = []ui.Fact{
			{Key: "Sent through", Value: mono(config.Cfg.SMTPHost + ":" + strconv.Itoa(config.Cfg.SMTPPort))},
			{Key: "From", Value: mono(config.Cfg.SMTPFrom)},
			{Key: "Signs in as", Value: signin},
			{Key: "Security", Value: mono(security)},
		}
	case a != nil && a.vayuMail != nil && a.vayuMail.Config().Enabled:
		facts = []ui.Fact{
			{Key: "Sent through", Value: ui.Text("This server’s own mail engine, VayuMail")},
			{Key: "From", Value: mono(a.transactionalFrom())},
		}
	default:
		facts = []ui.Fact{{Key: "Sent through", Value: ui.Text("Nothing: sign-in links and receipts cannot be sent")}}
	}
	return ui.Join(ui.Facts(facts...),
		ui.HTML(`<p class="settings-panel__lead">To send through another server, set <code>SMTP_HOST</code>, <code>SMTP_PORT</code>, <code>SMTP_USERNAME</code>, <code>SMTP_PASSWORD</code>, <code>SMTP_FROM</code> and <code>SMTP_TLS</code>, then restart. Without them, setting <code>DOMAIN</code> sends through VayuMail.</p>`),
	)
}

func settingsAdvancedFacts(_ context.Context, _ *App) ui.HTML {
	return ui.Join(
		ui.Facts(ui.Fact{Key: "Cache", Value: `<span class="mono">` + ui.Text(config.Cfg.CacheDir) + `</span>`}),
		ui.HTML(`<p class="settings-panel__lead">Exports, imports and restores are in <a href="/os/vayukeep">Backups</a>; clearing the caches is in <a href="/os/storage">Storage</a>.</p>`),
	)
}

// settingsPageScript runs a Settings page. Rows are compared with the value
// they had when the page loaded, so the bar counts real changes (typing a
// letter and deleting it is no change) and Save sends only those, one at a
// time (one SQLite writer). The menu and footer editors keep a hidden input in
// step, and take part like any other row.
const settingsPageScript = `function csrf(){return window.vpCsrf();} // admin-os.js loads after this script
var bar=document.querySelector('[data-settings-bar]');
var countEl=document.querySelector('[data-settings-count]');
function fields(){return Array.prototype.slice.call(document.querySelectorAll('[data-setting-key]'));}
function valueOf(el){return el.type==='checkbox'?(el.checked?'true':'false'):el.value;}
fields().forEach(function(el){el.dataset.initial=valueOf(el);});
function rowOf(el){return el.closest('.settings-row, .settings-panel');}
function changed(){return fields().filter(function(el){return valueOf(el)!==el.dataset.initial;});}
function refresh(){
  var c=changed();
  fields().forEach(function(el){var row=rowOf(el);if(row)row.classList.toggle('is-changed',c.indexOf(el)>=0);});
  if(!bar)return;
  if(c.length){countEl.textContent=c.length===1?'1 unsaved change':c.length+' unsaved changes';bar.hidden=false;requestAnimationFrame(function(){bar.classList.add('is-up');});}
  else{bar.classList.remove('is-up');bar.hidden=true;}
}
window.vpSettingsRefresh=refresh;
document.addEventListener('input',function(){setTimeout(refresh,0);});
document.addEventListener('change',function(){setTimeout(refresh,0);});
document.addEventListener('click',function(){setTimeout(refresh,0);});
// Segmented controls write their hidden value.
document.querySelectorAll('[data-seg-for]').forEach(function(seg){
  var input=document.getElementById(seg.getAttribute('data-seg-for'));
  var opts=Array.prototype.slice.call(seg.querySelectorAll('[data-seg-value]'));
  function show(v){opts.forEach(function(o){o.setAttribute('aria-checked',o.getAttribute('data-seg-value')===v?'true':'false');});}
  opts.forEach(function(o){o.addEventListener('click',function(){input.value=o.getAttribute('data-seg-value');show(input.value);refresh();});});
  seg.addEventListener('vp-reset',function(){show(input.value);});
});
function saveOne(el){
  return fetch('/os/api/settings',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
    body:JSON.stringify({key:el.dataset.settingKey,value:valueOf(el)})}).then(function(r){
    if(r.ok)return;
    return r.json().catch(function(){return {};}).then(function(e){
      var row=el.closest('.settings-row'),sec=el.closest('section'),lab=(row&&row.querySelector('.settings-row-label'))||(sec&&sec.querySelector('.section-head__title')),label=lab?lab.textContent:el.dataset.settingKey;
      throw new Error(label+': '+((e.error&&e.error.message)||('HTTP '+r.status)));
    });
  });
}
var saveBtn=document.querySelector('[data-settings-save]');
var discardBtn=document.querySelector('[data-settings-discard]');
if(saveBtn)saveBtn.addEventListener('click',function(){
  var c=changed();if(!c.length)return;
  saveBtn.disabled=true;
  c.reduce(function(chain,el){return chain.then(function(){return saveOne(el).then(function(){el.dataset.initial=valueOf(el);});});},Promise.resolve())
  .then(function(){
    saveBtn.disabled=false;refresh();
    vpToast(c.length===1?'Saved':'Saved '+c.length+' changes','ok');
    var scheme=c.filter(function(el){return el.dataset.settingKey==='admin.theme';})[0];
    if(scheme){document.body.dataset.theme=scheme.value;try{localStorage.setItem('vp-os-theme',scheme.value);}catch(e){}}
  }).catch(function(e){saveBtn.disabled=false;refresh();vpToast(e.message,'error');});
});
if(discardBtn)discardBtn.addEventListener('click',function(){
  var c=changed();
  // The menu and footer editors draw their rows from JSON once; putting the
  // JSON back is not enough to redraw them, so a change there reloads.
  if(c.some(function(el){return el.type==='hidden'&&!el.closest('.sa-seg, .settings-row-control');})){location.reload();return;}
  c.forEach(function(el){if(el.type==='checkbox')el.checked=el.dataset.initial==='true';else el.value=el.dataset.initial;});
  document.querySelectorAll('[data-seg-for]').forEach(function(seg){seg.dispatchEvent(new Event('vp-reset'));});
  refresh();
});
window.addEventListener('beforeunload',function(e){if(changed().length){e.preventDefault();e.returnValue='';}});
// A search result lands on its row: show which one.
if(location.hash){var t=document.querySelector(location.hash);var tr=t&&rowOf(t);if(tr){tr.classList.add('is-found');t.focus({preventScroll:false});}}
function moveRow(row,dir,sync){
  if(dir<0){var p=row.previousElementSibling;if(p)row.parentNode.insertBefore(row,p);}
  else{var n=row.nextElementSibling;if(n)row.parentNode.insertBefore(n,row);}
  if(sync)sync();
}
function iconButton(icon,label,onClick){
  var b=document.createElement('button');b.type='button';b.className='btn btn--ghost btn--icon btn--sm';b.setAttribute('aria-label',label);b.title=label;
  b.appendChild(vpIcon(icon));b.addEventListener('click',onClick);return b;
}
function reorderBtns(row,sync){
  var up=iconButton('chev-d','Move up',function(){moveRow(row,-1,sync);});up.classList.add('is-up');
  return[up,iconButton('chev-d','Move down',function(){moveRow(row,1,sync);})];
}
function linkRow(label,href,hrefHint,sync,attrs){
  var row=document.createElement('div');row.className='settings-link';row.setAttribute(attrs.row,'');
  var li=document.createElement('input');li.className='input settings-link__label';li.type='text';li.placeholder='Label';li.value=label||'';li.setAttribute(attrs.label,'');li.setAttribute('aria-label','Link label');
  var hi=document.createElement('input');hi.className='input settings-link__href';hi.type='text';hi.placeholder=hrefHint;hi.value=href||'';hi.setAttribute(attrs.href,'');hi.setAttribute('aria-label','Link address');
  li.addEventListener('input',sync);hi.addEventListener('input',sync);
  row.appendChild(li);row.appendChild(hi);
  var rb=reorderBtns(row,sync);row.appendChild(rb[0]);row.appendChild(rb[1]);
  row.appendChild(iconButton('x','Remove link',function(){row.remove();sync();}));
  return row;
}
// Public menu.
var navEditor=document.getElementById('nav-editor');
var navHidden=document.getElementById('nav-json-input');
if(navEditor&&navHidden){
  var navAttrs={row:'data-nav-row',label:'data-nav-label',href:'data-nav-href'};
  var navSync=function(){
    var out=[];
    navEditor.querySelectorAll('[data-nav-row]').forEach(function(row){
      var l=row.querySelector('[data-nav-label]').value.trim(),h=row.querySelector('[data-nav-href]').value.trim();
      if(l&&h)out.push({label:l,href:h});
    });
    navHidden.value=JSON.stringify(out);refresh();
  };
  var seed=[];try{seed=JSON.parse(navEditor.getAttribute('data-nav-json')||'[]');}catch(e){seed=[];}
  seed.forEach(function(it){navEditor.appendChild(linkRow(it.label,it.href,'/path or https://…',navSync,navAttrs));});
  navSync();navHidden.dataset.initial=navHidden.value;
  var navAdd=document.getElementById('nav-add-btn');
  if(navAdd)navAdd.addEventListener('click',function(){navEditor.appendChild(linkRow('','','/path or https://…',navSync,navAttrs));});
}
// Site mark. The endpoint and the preview follow the page's scope, taken from
// the image the server rendered: a hard-coded /os/api/branding/favicon made a
// hosted domain's save change the install-wide mark.
var favFile=document.getElementById('brand-favicon-file');
var favUp=document.getElementById('brand-favicon-upload');
var favRm=document.getElementById('brand-favicon-remove');
var favImg=document.getElementById('brand-favicon-img');
var favState=document.getElementById('brand-favicon-state');
var favBase=(favImg&&favImg.getAttribute('src')||'/favicon.ico').split('?')[0];
var favPost=favBase==='/favicon.ico'?'/os/api/branding/favicon':favBase.replace('/branding/mark','/api/branding/favicon');
function favSend(fd,btn,done){
  btn.disabled=true;
  fetch(favPost,{method:'POST',headers:{'X-CSRF-Token':csrf()},body:fd})
    .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
    .then(function(res){btn.disabled=false;if(res.ok){if(favImg)favImg.src=favBase+'?t='+Date.now();done();}else{vpToast(res.d.error||res.d.detail||'The mark could not be changed','error');}})
    .catch(function(e){btn.disabled=false;vpToast('The mark could not be changed: '+e,'error');});
}
if(favUp)favUp.addEventListener('click',function(){
  var f=favFile&&favFile.files&&favFile.files[0];
  if(!f){vpToast('Choose a PNG or ICO first','warn');return;}
  var fd=new FormData();fd.append('favicon',f);
  favSend(fd,favUp,function(){if(favState)favState.textContent='Your own mark, stored in the database.';vpToast('Mark updated','ok');});
});
if(favRm)favRm.addEventListener('click',function(){
  var fd=new FormData();fd.append('remove','1');
  favSend(fd,favRm,function(){if(favState)favState.textContent='Using the default mark.';vpToast('Default mark restored','ok');});
});
// Footer.
var footerInput=document.getElementById('footer-json-input');
if(footerInput){
  var fTagline=document.getElementById('footer-tagline');
  var fCopyright=document.getElementById('footer-copyright');
  var fCols=document.getElementById('footer-cols');
  var fSocial=document.getElementById('footer-social');
  var fLegal=document.getElementById('footer-legal');
  var fAttrs={row:'data-f-link',label:'data-f-label',href:'data-f-href'};
  var fHint='/path, mailto: or https://…';
  var footerSync;
  var fColCard=function(title,links){
    var card=document.createElement('div');card.className='settings-col';card.setAttribute('data-f-col','');
    var head=document.createElement('div');head.className='settings-link';
    var ti=document.createElement('input');ti.className='input settings-link__label';ti.type='text';ti.placeholder='Column title';ti.value=title||'';ti.setAttribute('data-f-col-title','');ti.setAttribute('aria-label','Column title');
    ti.addEventListener('input',function(){footerSync();});
    head.appendChild(ti);
    var cb=reorderBtns(card,function(){footerSync();});head.appendChild(cb[0]);head.appendChild(cb[1]);
    head.appendChild(iconButton('x','Remove column',function(){card.remove();footerSync();}));
    var linksWrap=document.createElement('div');linksWrap.setAttribute('data-f-col-links','');
    (links||[]).forEach(function(l){linksWrap.appendChild(linkRow(l.label,l.href,fHint,function(){footerSync();},fAttrs));});
    var addL=document.createElement('button');addL.type='button';addL.className='btn btn--sm';addL.textContent='Add link';
    addL.addEventListener('click',function(){linksWrap.appendChild(linkRow('','',fHint,function(){footerSync();},fAttrs));footerSync();});
    card.appendChild(head);card.appendChild(linksWrap);card.appendChild(addL);
    return card;
  };
  var fCollect=function(wrap){
    var out=[];if(!wrap)return out;
    wrap.querySelectorAll('[data-f-link]').forEach(function(row){
      var l=row.querySelector('[data-f-label]').value.trim(),h=row.querySelector('[data-f-href]').value.trim();
      if(l&&h)out.push({label:l,href:h});
    });
    return out;
  };
  footerSync=function(){
    var cols=[];
    if(fCols)fCols.querySelectorAll('[data-f-col]').forEach(function(card){
      var t=card.querySelector('[data-f-col-title]').value.trim(),links=fCollect(card.querySelector('[data-f-col-links]'));
      if(t||links.length)cols.push({title:t,links:links});
    });
    footerInput.value=JSON.stringify({tagline:fTagline?fTagline.value.trim():'',copyright:fCopyright?fCopyright.value.trim():'',columns:cols,social:fCollect(fSocial),legal:fCollect(fLegal)});
    refresh();
  };
  var fseed={};try{fseed=JSON.parse(footerInput.getAttribute('data-footer-seed')||'{}');}catch(e){fseed={};}
  if(fTagline){fTagline.value=fseed.tagline||'';fTagline.addEventListener('input',function(){footerSync();});}
  if(fCopyright){fCopyright.value=fseed.copyright||'';fCopyright.addEventListener('input',function(){footerSync();});}
  if(fCols)(fseed.columns||[]).forEach(function(c){fCols.appendChild(fColCard(c.title,c.links));});
  if(fSocial)(fseed.social||[]).forEach(function(l){fSocial.appendChild(linkRow(l.label,l.href,fHint,function(){footerSync();},fAttrs));});
  if(fLegal)(fseed.legal||[]).forEach(function(l){fLegal.appendChild(linkRow(l.label,l.href,fHint,function(){footerSync();},fAttrs));});
  footerSync();footerInput.dataset.initial=footerInput.value;
  var addCol=document.getElementById('footer-add-col');
  if(addCol)addCol.addEventListener('click',function(){fCols.appendChild(fColCard('',[]));footerSync();});
  var addSocial=document.getElementById('footer-add-social');
  if(addSocial)addSocial.addEventListener('click',function(){fSocial.appendChild(linkRow('','',fHint,function(){footerSync();},fAttrs));footerSync();});
  var addLegal=document.getElementById('footer-add-legal');
  if(addLegal)addLegal.addEventListener('click',function(){fLegal.appendChild(linkRow('','',fHint,function(){footerSync();},fAttrs));footerSync();});
}
refresh();`
